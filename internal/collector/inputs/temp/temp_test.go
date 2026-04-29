package temp

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestTempInputInit(t *testing.T) {
	input := &TempInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestTempInputSampleConfig(t *testing.T) {
	input := &TempInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}

func TestTempInputGatherUnavailable(t *testing.T) {
	input := &TempInput{available: false}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics when unavailable, got %d", len(metrics))
	}
}
