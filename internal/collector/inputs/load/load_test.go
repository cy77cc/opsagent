package load

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestLoadInputInit(t *testing.T) {
	input := &LoadInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestLoadInputInitIgnoresExtraFields(t *testing.T) {
	input := &LoadInput{}
	cfg := map[string]interface{}{
		"unknown_field": "value",
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() should ignore extra fields, got error: %v", err)
	}
}

func TestLoadInputSampleConfig(t *testing.T) {
	input := &LoadInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}

func TestLoadInputGather(t *testing.T) {
	input := &LoadInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("Gather() produced 0 metrics, want at least 1")
	}

	m := metrics[0]
	if m.Name() != "load" {
		t.Errorf("metric name = %q, want %q", m.Name(), "load")
	}

	fields := m.Fields()
	for _, key := range []string{"load1", "load5", "load15"} {
		v, ok := fields[key]
		if !ok {
			t.Errorf("missing field %q", key)
			continue
		}
		if _, ok := v.(float64); !ok {
			t.Errorf("field %q should be float64, got %T", key, v)
		}
	}
}
