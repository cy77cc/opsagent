package cpu

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestCPUInputInit(t *testing.T) {
	input := &CPUInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if !input.totalCPU {
		t.Error("totalCPU should default to true")
	}
	if input.perCPU {
		t.Error("perCPU should default to false")
	}
}

func TestCPUInputInitConfig(t *testing.T) {
	input := &CPUInput{}
	cfg := map[string]interface{}{
		"percpu":   true,
		"totalcpu": false,
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if !input.perCPU {
		t.Error("perCPU should be true after config")
	}
	if input.totalCPU {
		t.Error("totalCPU should be false after config")
	}
}

func TestCPUInputInitInvalidConfig(t *testing.T) {
	input := &CPUInput{}
	cfg := map[string]interface{}{
		"percpu": "notabool",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid percpu type")
	}
}

func TestCPUInputGatherTotal(t *testing.T) {
	input := &CPUInput{}
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

	found := false
	for _, m := range metrics {
		if m.Name() == "cpu" {
			tags := m.Tags()
			if tags["cpu"] == "cpu-total" {
				found = true
				fields := m.Fields()
				if _, ok := fields["usage_percent"]; !ok {
					t.Error("missing 'usage_percent' field")
				}
				pct, ok := fields["usage_percent"].(float64)
				if !ok {
					t.Errorf("usage_percent should be float64, got %T", fields["usage_percent"])
				}
				if pct < 0 || pct > 100 {
					t.Errorf("usage_percent out of range: %f", pct)
				}
			}
		}
	}
	if !found {
		t.Error("expected 'cpu' metric with cpu=cpu-total tag")
	}
}

func TestCPUInputGatherPerCPU(t *testing.T) {
	input := &CPUInput{}
	cfg := map[string]interface{}{
		"percpu":   true,
		"totalcpu": false,
	}
	if err := input.Init(cfg); err != nil {
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

	for _, m := range metrics {
		if m.Name() != "cpu" {
			t.Errorf("unexpected metric name: %q", m.Name())
		}
		tags := m.Tags()
		if tags["cpu"] == "" {
			t.Error("missing 'cpu' tag")
		}
		fields := m.Fields()
		if _, ok := fields["usage_percent"]; !ok {
			t.Error("missing 'usage_percent' field")
		}
	}
}

func TestCPUInputSampleConfig(t *testing.T) {
	input := &CPUInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
