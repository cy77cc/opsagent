package collector

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// testInput is an Input that records Gather calls and emits a fixed metric.
type testInput struct {
	callCount int32
	metricName string
	tags       map[string]string
	fields     map[string]interface{}
}

func newTestInput(name string, tags map[string]string, fields map[string]interface{}) *testInput {
	return &testInput{
		metricName: name,
		tags:       tags,
		fields:     fields,
	}
}

func (t *testInput) Init(_ map[string]interface{}) error { return nil }
func (t *testInput) Gather(_ context.Context, acc Accumulator) error {
	atomic.AddInt32(&t.callCount, 1)
	acc.AddFields(t.metricName, t.tags, t.fields)
	return nil
}
func (t *testInput) SampleConfig() string { return "" }

func (t *testInput) CallCount() int32 {
	return atomic.LoadInt32(&t.callCount)
}

func TestSchedulerRunsInput(t *testing.T) {
	input := newTestInput("cpu", map[string]string{"host": "s1"}, map[string]interface{}{"usage": 50.0})

	si := ScheduledInput{
		Input:    input,
		Interval: 50 * time.Millisecond,
		Tags:     map[string]string{"env": "test"},
	}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Wait for at least one batch
	select {
	case batch := <-ch:
		if len(batch) == 0 {
			t.Fatal("received empty batch")
		}
		m := batch[0]
		if m.Name() != "cpu" {
			t.Errorf("Name() = %q, want %q", m.Name(), "cpu")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for metrics")
	}

	sched.Stop()
}

func TestSchedulerMultipleInputs(t *testing.T) {
	input1 := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	input2 := newTestInput("mem", nil, map[string]interface{}{"v": 2.0})

	si1 := ScheduledInput{Input: input1, Interval: 50 * time.Millisecond}
	si2 := ScheduledInput{Input: input2, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si1, si2}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	seen := make(map[string]bool)
	timeout := time.After(2 * time.Second)

	for len(seen) < 2 {
		select {
		case batch := <-ch:
			for _, m := range batch {
				seen[m.Name()] = true
			}
		case <-timeout:
			t.Fatalf("timed out, seen: %v", seen)
		}
	}

	sched.Stop()
}

func TestSchedulerStop(t *testing.T) {
	input := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx := context.Background()
	ch := sched.Start(ctx)

	// Drain one metric
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	sched.Stop()

	// Channel should be closed after Stop
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after Stop()")
	}
}

func TestSchedulerReload(t *testing.T) {
	input1 := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input1, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Wait for at least one gather
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial metrics")
	}

	// Reload with empty config — should stop all inputs
	err := sched.Reload(ctx, ReloadConfig{})
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Wait a bit then stop
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after reload with empty config and stop")
	}
}

func TestSchedulerHealthStatus_LastCollection(t *testing.T) {
	input := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Before start, last_collection should be absent.
	hs := sched.HealthStatus()
	if _, ok := hs.Details["last_collection"]; ok {
		t.Error("expected no last_collection before start")
	}

	ch := sched.Start(ctx)

	// Wait for at least one gather.
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for metrics")
	}

	hs = sched.HealthStatus()
	lc, ok := hs.Details["last_collection"]
	if !ok {
		t.Fatal("expected last_collection after gather")
	}
	if lc == "" {
		t.Error("expected non-empty last_collection")
	}

	sched.Stop()
}

func TestSchedulerAppliesStaticTags(t *testing.T) {
	input := newTestInput("cpu", map[string]string{"host": "s1"}, map[string]interface{}{"v": 1.0})

	si := ScheduledInput{
		Input:    input,
		Interval: 50 * time.Millisecond,
		Tags:     map[string]string{"env": "prod", "region": "us-east"},
	}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	select {
	case batch := <-ch:
		if len(batch) == 0 {
			t.Fatal("empty batch")
		}
		m := batch[0]
		tags := m.Tags()
		if tags["env"] != "prod" {
			t.Errorf("Tags[env] = %q, want %q", tags["env"], "prod")
		}
		if tags["region"] != "us-east" {
			t.Errorf("Tags[region] = %q, want %q", tags["region"], "us-east")
		}
		if tags["host"] != "s1" {
			t.Errorf("Tags[host] = %q, want %q (original tag preserved)", tags["host"], "s1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}

	sched.Stop()
}

// errorInput is an Input whose Gather always returns an error.
type errorInput struct {
	err error
}

func (e *errorInput) Init(_ map[string]interface{}) error         { return nil }
func (e *errorInput) Gather(_ context.Context, _ Accumulator) error { return e.err }
func (e *errorInput) SampleConfig() string                         { return "" }

func TestSchedulerGatherOnce_ErrorPath(t *testing.T) {
	input := &errorInput{err: fmt.Errorf("gather failure")}
	si := ScheduledInput{
		Input:    input,
		Interval: 50 * time.Millisecond,
	}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Wait for the gather to run; since it errors, no metrics should appear.
	// We just verify the scheduler doesn't crash and can be stopped cleanly.
	time.Sleep(150 * time.Millisecond)
	sched.Stop()

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after Stop()")
	}
}

// pushAggregator is an Aggregator that tracks Push calls and emits a metric.
type pushAggregator struct {
	pushCount int32
}

func (p *pushAggregator) Init(_ map[string]interface{}) error { return nil }
func (p *pushAggregator) Add(_ *Metric)                       {}
func (p *pushAggregator) Push(acc Accumulator) {
	atomic.AddInt32(&p.pushCount, 1)
	acc.AddFields("agg_metric", nil, map[string]interface{}{"v": 1.0})
}
func (p *pushAggregator) Reset()               {}
func (p *pushAggregator) SampleConfig() string   { return "" }

func TestSchedulerAggregatorPush(t *testing.T) {
	input := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input, Interval: 50 * time.Millisecond}

	agg := &pushAggregator{}

	sched := NewScheduler([]ScheduledInput{si}, nil, []Aggregator{agg}, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Drain at least one input metric to confirm the scheduler is running.
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input metrics")
	}

	sched.Stop()

	// After Stop, runAggregatorPush does a final push. Verify push was called.
	count := atomic.LoadInt32(&agg.pushCount)
	if count == 0 {
		t.Error("expected pushAggregator.Push to be called at least once")
	}
}

func TestSchedulerReload_WithAggregator(t *testing.T) {
	orig := DefaultRegistry
	DefaultRegistry = NewRegistry()
	defer func() { DefaultRegistry = orig }()

	RegisterInput("test-input", func() Input { return &mockInput{name: "test-input"} })

	agg := &pushAggregator{}

	// Reload on a stopped scheduler (no Start called) exercises the
	// aggregator-push-on-teardown path without the WaitGroup race
	// that Start's closer goroutine would cause.
	sched := NewScheduler(nil, nil, []Aggregator{agg}, nil, zerolog.Nop())

	err := sched.Reload(context.Background(), ReloadConfig{
		Inputs: []PluginConfig{
			{Type: "test-input", Config: map[string]interface{}{}},
		},
	})
	if err != nil {
		t.Fatalf("Reload() error: %v", err)
	}

	// The aggregator Push should have been called during teardown.
	count := atomic.LoadInt32(&agg.pushCount)
	if count == 0 {
		t.Error("expected pushAggregator.Push to be called during Reload teardown")
	}
}
