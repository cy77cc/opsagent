package connections

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestConnectionsInputInit(t *testing.T) {
	input := &ConnectionsInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.states) != 0 {
		t.Errorf("states should be empty by default, got %v", input.states)
	}
}

func TestConnectionsInputInitWithStates(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{"ESTABLISHED", "LISTEN"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.states) != 2 {
		t.Errorf("states len = %d, want 2", len(input.states))
	}
}

func TestConnectionsInputInitInvalidStates(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": "notalist",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid states type")
	}
}

func TestConnectionsInputInitInvalidStateItem(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{123},
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with non-string state item")
	}
}

func TestConnectionsInputSampleConfig(t *testing.T) {
	input := &ConnectionsInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}

func TestConnectionsInputGather(t *testing.T) {
	input := &ConnectionsInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// May be 0 if no connections, that's ok
	for _, m := range metrics {
		if m.Name() != "connections" {
			t.Errorf("metric name = %q, want %q", m.Name(), "connections")
		}
		tags := m.Tags()
		if tags["state"] == "" {
			t.Error("missing 'state' tag")
		}
		if tags["protocol"] == "" {
			t.Error("missing 'protocol' tag")
		}
		fields := m.Fields()
		if _, ok := fields["count_by_state"]; !ok {
			t.Error("missing 'count_by_state' field")
		}
	}
}

func TestConnectionsInputGatherFilterState(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{"ESTABLISHED"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	for _, m := range metrics {
		tags := m.Tags()
		if tags["state"] != "ESTABLISHED" {
			t.Errorf("unexpected state: %q, expected ESTABLISHED", tags["state"])
		}
	}
}
