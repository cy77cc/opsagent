package memory

import (
	"context"
	"testing"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

func TestMemoryInputInit(t *testing.T) {
	input := &MemoryInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestMemoryInputGather(t *testing.T) {
	input := &MemoryInput{}
	if err := input.Init(nil); err != nil {
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

	// Find the memory metric
	var memMetric *collector.Metric
	for _, m := range metrics {
		if m.Name() == "memory" {
			memMetric = m
			break
		}
	}
	if memMetric == nil {
		t.Fatal("expected 'memory' metric")
	}

	expectedFields := []string{"total_bytes", "available_bytes", "used_bytes", "used_percent", "free_bytes"}
	fields := memMetric.Fields()
	for _, f := range expectedFields {
		if _, ok := fields[f]; !ok {
			t.Errorf("missing field %q in memory metric", f)
		}
	}

	// Verify types
	if _, ok := fields["total_bytes"].(int64); !ok {
		t.Errorf("total_bytes should be int64, got %T", fields["total_bytes"])
	}
	if _, ok := fields["available_bytes"].(int64); !ok {
		t.Errorf("available_bytes should be int64, got %T", fields["available_bytes"])
	}
	if _, ok := fields["used_bytes"].(int64); !ok {
		t.Errorf("used_bytes should be int64, got %T", fields["used_bytes"])
	}
	if _, ok := fields["used_percent"].(float64); !ok {
		t.Errorf("used_percent should be float64, got %T", fields["used_percent"])
	}
	if _, ok := fields["free_bytes"].(int64); !ok {
		t.Errorf("free_bytes should be int64, got %T", fields["free_bytes"])
	}

	// Verify non-negative values
	if fields["total_bytes"].(int64) <= 0 {
		t.Error("total_bytes should be positive")
	}
}

func TestMemoryInputGatherSwap(t *testing.T) {
	input := &MemoryInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// Swap may or may not be available depending on environment
	for _, m := range metrics {
		if m.Name() == "swap" {
			fields := m.Fields()
			swapFields := []string{"total_bytes", "used_bytes", "free_bytes", "used_percent"}
			for _, f := range swapFields {
				if _, ok := fields[f]; !ok {
					t.Errorf("missing field %q in swap metric", f)
				}
			}
		}
	}
}

func TestMemoryInputSampleConfig(t *testing.T) {
	input := &MemoryInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
