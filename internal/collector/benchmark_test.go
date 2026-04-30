package collector

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// benchInput is a minimal Input implementation for benchmarks.
type benchInput struct {
	name string
}

func (b *benchInput) Init(_ map[string]interface{}) error { return nil }

func (b *benchInput) Gather(_ context.Context, acc Accumulator) error {
	acc.AddFields(b.name,
		nil,
		map[string]interface{}{"value": float64(42)},
	)
	return nil
}

func (b *benchInput) SampleConfig() string { return "" }

// benchProcessor is a minimal Processor implementation for benchmarks.
type benchProcessor struct {
	tags map[string]string
}

func (p *benchProcessor) Init(cfg map[string]interface{}) error {
	if rawTags, ok := cfg["tags"].(map[string]interface{}); ok {
		p.tags = make(map[string]string, len(rawTags))
		for k, v := range rawTags {
			p.tags[k] = v.(string)
		}
	}
	return nil
}

func (p *benchProcessor) Apply(in []*Metric) []*Metric {
	for _, m := range in {
		for k, v := range p.tags {
			m.AddTag(k, v)
		}
	}
	return in
}

func (p *benchProcessor) SampleConfig() string { return "" }

func BenchmarkMetricCollection(b *testing.B) {
	scheduled := []ScheduledInput{
		{Input: &benchInput{name: "cpu"}, Interval: 10 * time.Millisecond},
		{Input: &benchInput{name: "memory"}, Interval: 10 * time.Millisecond},
	}

	proc := &benchProcessor{}
	proc.Init(map[string]interface{}{"tags": map[string]interface{}{"bench": "true"}})

	sched := NewScheduler(scheduled, []Processor{proc}, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		<-ch
	}
	b.StopTimer()
	sched.Stop()
}

func BenchmarkPipelineProcessing(b *testing.B) {
	proc := &benchProcessor{}
	proc.Init(map[string]interface{}{"tags": map[string]interface{}{"bench": "true"}})

	metrics := make([]*Metric, 100)
	for i := range metrics {
		metrics[i] = NewMetric("test_metric", nil, map[string]interface{}{"value": float64(i)}, Gauge, time.Now())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proc.Apply(metrics)
	}
}
