package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/cy77cc/nodeagentx/internal/collector"

	// Blank imports to trigger plugin registration via init().
	_ "github.com/cy77cc/nodeagentx/internal/collector/inputs/cpu"
	_ "github.com/cy77cc/nodeagentx/internal/collector/inputs/memory"
	_ "github.com/cy77cc/nodeagentx/internal/collector/processors/tagger"
)

// captureOutput implements collector.Output and stores all written metrics for testing.
type captureOutput struct {
	mu      sync.Mutex
	metrics []collector.Metric
}

func newCaptureOutput() *captureOutput {
	return &captureOutput{}
}

func (o *captureOutput) Init(_ map[string]interface{}) error {
	return nil
}

func (o *captureOutput) Write(metrics []collector.Metric) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.metrics = append(o.metrics, metrics...)
	return nil
}

func (o *captureOutput) Close() error {
	return nil
}

func (o *captureOutput) SampleConfig() string {
	return ""
}

func (o *captureOutput) snapshot() []collector.Metric {
	o.mu.Lock()
	defer o.mu.Unlock()
	cp := make([]collector.Metric, len(o.metrics))
	copy(cp, o.metrics)
	return cp
}

// collectMetrics drains metric batches from the channel until we have at least
// minCount metrics or the timeout expires.
func collectMetrics(ch <-chan []*collector.Metric, minCount int, timeout time.Duration) []*collector.Metric {
	var all []*collector.Metric
	deadline := time.After(timeout)
	for {
		if len(all) >= minCount {
			return all
		}
		select {
		case batch, ok := <-ch:
			if !ok {
				return all
			}
			all = append(all, batch...)
		case <-deadline:
			return all
		}
	}
}

// TestPipelineIntegration verifies that inputs registered in the DefaultRegistry
// can be created, run through a Scheduler, and that a processor (tagger) applies
// tags correctly to the collected metrics.
func TestPipelineIntegration(t *testing.T) {
	reg := collector.DefaultRegistry

	// Verify cpu input is registered.
	cpuFactory, ok := reg.GetInput("cpu")
	if !ok {
		t.Fatal("cpu input not found in DefaultRegistry")
	}

	// Verify memory input is registered.
	memFactory, ok := reg.GetInput("memory")
	if !ok {
		t.Fatal("memory input not found in DefaultRegistry")
	}

	// Verify tagger processor is registered.
	procFactory, ok := reg.GetProcessor("tagger")
	if !ok {
		t.Fatal("tagger processor not found in DefaultRegistry")
	}

	// Create inputs from factories.
	cpuInput := cpuFactory()
	if err := cpuInput.Init(nil); err != nil {
		t.Fatalf("cpu Init failed: %v", err)
	}

	memInput := memFactory()
	if err := memInput.Init(nil); err != nil {
		t.Fatalf("memory Init failed: %v", err)
	}

	// Build scheduled inputs with a short interval.
	scheduledInputs := []collector.ScheduledInput{
		{
			Input:    cpuInput,
			Interval: 200 * time.Millisecond,
			Tags:     map[string]string{"source": "cpu"},
		},
		{
			Input:    memInput,
			Interval: 200 * time.Millisecond,
			Tags:     map[string]string{"source": "memory"},
		},
	}

	logger := zerolog.Nop()
	scheduler := collector.NewScheduler(scheduledInputs, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch := scheduler.Start(ctx)

	// Collect metrics from the channel until we have at least 2 or timeout.
	allMetrics := collectMetrics(ch, 2, 3*time.Second)

	// Stop the scheduler and drain remaining.
	scheduler.Stop()

	if len(allMetrics) == 0 {
		t.Fatal("no metrics collected from scheduler")
	}

	t.Logf("collected %d raw metrics from scheduler", len(allMetrics))

	// Verify that scheduler-applied static tags are present on the metrics.
	for _, m := range allMetrics {
		tags := m.Tags()
		switch m.Name() {
		case "cpu":
			if tags["source"] != "cpu" {
				t.Errorf("expected source=cpu tag on cpu metric, got tags: %v", tags)
			}
		case "memory":
			if tags["source"] != "memory" {
				t.Errorf("expected source=memory tag on memory metric, got tags: %v", tags)
			}
		}
	}

	// Apply the tagger processor (from registry factory — empty config is fine).
	proc := procFactory()
	tagProc, ok := proc.(interface {
		Apply(in []*collector.Metric) []*collector.Metric
	})
	if !ok {
		t.Fatal("processor does not implement Apply method")
	}

	tagged := tagProc.Apply(allMetrics)
	if len(tagged) != len(allMetrics) {
		t.Errorf("tagger changed metric count: got %d, want %d", len(tagged), len(allMetrics))
	}

	// Verify the captureOutput helper works correctly.
	output := newCaptureOutput()
	if err := output.Init(nil); err != nil {
		t.Fatalf("captureOutput Init failed: %v", err)
	}

	// Convert []*Metric to []Metric for the output interface.
	flat := make([]collector.Metric, len(allMetrics))
	for i, m := range allMetrics {
		flat[i] = *m
	}
	if err := output.Write(flat); err != nil {
		t.Fatalf("captureOutput Write failed: %v", err)
	}

	snap := output.snapshot()
	if len(snap) != len(flat) {
		t.Errorf("captureOutput stored %d metrics, expected %d", len(snap), len(flat))
	}

	if err := output.Close(); err != nil {
		t.Fatalf("captureOutput Close failed: %v", err)
	}

	t.Logf("pipeline integration test passed: collected %d metrics with tags applied", len(allMetrics))
}
