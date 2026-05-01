package temp

import (
	"context"
	"strings"
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
	if !strings.Contains(sc, "temperature") {
		t.Error("SampleConfig should mention temperature")
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

func TestRegisteredInDefaultRegistry(t *testing.T) {
	factory, ok := collector.DefaultRegistry.GetInput("temp")
	if !ok {
		t.Fatal("temp input not registered in DefaultRegistry")
	}
	input := factory()
	if input == nil {
		t.Fatal("factory returned nil input")
	}
	if _, ok := input.(*TempInput); !ok {
		t.Errorf("expected *TempInput, got %T", input)
	}
}

func TestSampleConfigContainsNoConfigRequired(t *testing.T) {
	input := &TempInput{}
	sc := input.SampleConfig()
	if !strings.Contains(sc, "No configuration required") {
		t.Error("SampleConfig should contain 'No configuration required'")
	}
}

func TestTempInputInitNilConfig(t *testing.T) {
	input := &TempInput{}
	// Init with nil config should not panic.
	if err := input.Init(nil); err != nil {
		t.Fatalf("Init(nil) error: %v", err)
	}
}

func TestTempInputInitConsistency(t *testing.T) {
	input := &TempInput{}
	// Calling Init twice should be safe and consistent.
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("first Init() error: %v", err)
	}
	firstAvailable := input.available

	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("second Init() error: %v", err)
	}
	if input.available != firstAvailable {
		t.Errorf("available changed between Init calls: first=%v, second=%v", firstAvailable, input.available)
	}
}

func TestTempInputGatherUnavailableWithCancelledContext(t *testing.T) {
	input := &TempInput{available: false}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	acc := collector.NewAccumulator(100)
	if err := input.Gather(ctx, acc); err != nil {
		t.Fatalf("Gather() with cancelled context should not error when unavailable: %v", err)
	}
	if metrics := acc.Collect(); len(metrics) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(metrics))
	}
}

func TestSampleConfigDeterministic(t *testing.T) {
	input := &TempInput{}
	first := input.SampleConfig()
	second := input.SampleConfig()
	if first != second {
		t.Error("SampleConfig() should return consistent content across calls")
	}
}

func TestTempInputGatherAvailable(t *testing.T) {
	input := &TempInput{}
	// Run Init to determine actual sensor availability.
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	if !input.available {
		t.Skip("temperature sensors not available in this environment, skipping Gather test")
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Error("expected at least 1 metric when sensors are available")
	}

	// Verify each metric has the expected structure.
	for _, m := range metrics {
		if m.Name() != "temp" {
			t.Errorf("metric name = %q, want temp", m.Name())
		}
		tags := m.Tags()
		if _, ok := tags["sensor"]; !ok {
			t.Error("metric missing 'sensor' tag")
		}
		fields := m.Fields()
		if _, ok := fields["temperature"]; !ok {
			t.Error("metric missing 'temperature' field")
		}
	}
}
