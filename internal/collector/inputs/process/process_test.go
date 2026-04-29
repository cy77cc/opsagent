package process

import (
	"context"
	"testing"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

func TestProcessInputInit(t *testing.T) {
	input := &ProcessInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if input.topN != 10 {
		t.Errorf("topN = %d, want 10 (default)", input.topN)
	}
}

func TestProcessInputInitWithTopN(t *testing.T) {
	input := &ProcessInput{}
	cfg := map[string]interface{}{
		"top_n": 5,
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if input.topN != 5 {
		t.Errorf("topN = %d, want 5", input.topN)
	}
}

func TestProcessInputInitTopNFloat64(t *testing.T) {
	input := &ProcessInput{}
	cfg := map[string]interface{}{
		"top_n": float64(3),
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if input.topN != 3 {
		t.Errorf("topN = %d, want 3", input.topN)
	}
}

func TestProcessInputInitInvalidTopN(t *testing.T) {
	input := &ProcessInput{}
	cfg := map[string]interface{}{
		"top_n": "notanint",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid top_n type")
	}
}

func TestProcessInputInitNegativeTopN(t *testing.T) {
	input := &ProcessInput{}
	cfg := map[string]interface{}{
		"top_n": -1,
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with negative top_n")
	}
}

func TestProcessInputGather(t *testing.T) {
	input := &ProcessInput{}
	if err := input.Init(map[string]interface{}{"top_n": 3}); err != nil {
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

	// Find the process_summary metric
	var summaryFound bool
	for _, m := range metrics {
		if m.Name() == "process_summary" {
			summaryFound = true
			fields := m.Fields()
			totalCount, ok := fields["total_count"].(int64)
			if !ok {
				t.Errorf("total_count should be int64, got %T", fields["total_count"])
			}
			if totalCount <= 0 {
				t.Errorf("total_count should be positive, got %d", totalCount)
			}
			break
		}
	}
	if !summaryFound {
		t.Error("expected 'process_summary' metric")
	}

	// Count process metrics
	processCount := 0
	for _, m := range metrics {
		if m.Name() == "process" {
			processCount++
			tags := m.Tags()
			if tags["pid"] == "" {
				t.Error("missing 'pid' tag")
			}
			if tags["name"] == "" {
				t.Error("missing 'name' tag")
			}
			fields := m.Fields()
			if _, ok := fields["cpu_percent"].(float64); !ok {
				t.Errorf("cpu_percent should be float64, got %T", fields["cpu_percent"])
			}
			if _, ok := fields["mem_percent"].(float64); !ok {
				t.Errorf("mem_percent should be float64, got %T", fields["mem_percent"])
			}
			if _, ok := fields["mem_rss_bytes"].(int64); !ok {
				t.Errorf("mem_rss_bytes should be int64, got %T", fields["mem_rss_bytes"])
			}
		}
	}
	if processCount == 0 {
		t.Error("expected at least 1 'process' metric")
	}
	if processCount > 3 {
		t.Errorf("expected at most 3 process metrics (top_n=3), got %d", processCount)
	}
}

func TestProcessInputGatherSortedByCPU(t *testing.T) {
	input := &ProcessInput{}
	if err := input.Init(map[string]interface{}{"top_n": 5}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	var cpuValues []float64
	for _, m := range metrics {
		if m.Name() == "process" {
			fields := m.Fields()
			if pct, ok := fields["cpu_percent"].(float64); ok {
				cpuValues = append(cpuValues, pct)
			}
		}
	}

	// Verify descending order
	for i := 1; i < len(cpuValues); i++ {
		if cpuValues[i] > cpuValues[i-1] {
			t.Errorf("process metrics not sorted by CPU: index %d (%.2f) > index %d (%.2f)",
				i, cpuValues[i], i-1, cpuValues[i-1])
		}
	}
}

func TestProcessInputSampleConfig(t *testing.T) {
	input := &ProcessInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
