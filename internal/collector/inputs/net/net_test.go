package net

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestNetInputInit(t *testing.T) {
	input := &NetInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestNetInputGather(t *testing.T) {
	input := &NetInput{}
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

	// Find a net metric
	var netMetric *collector.Metric
	for _, m := range metrics {
		if m.Name() == "net" {
			netMetric = m
			break
		}
	}
	if netMetric == nil {
		t.Fatal("expected 'net' metric")
	}

	// Verify tag
	tags := netMetric.Tags()
	if tags["interface"] == "" {
		t.Error("missing 'interface' tag")
	}

	// Verify all expected fields
	expectedFields := []string{
		"bytes_sent", "bytes_recv",
		"packets_sent", "packets_recv",
		"err_in", "err_out",
		"drop_in", "drop_out",
	}
	fields := netMetric.Fields()
	for _, f := range expectedFields {
		if _, ok := fields[f]; !ok {
			t.Errorf("missing field %q in net metric", f)
		}
	}

	// Verify all fields are int64
	for _, f := range expectedFields {
		if _, ok := fields[f].(int64); !ok {
			t.Errorf("field %q should be int64, got %T", f, fields[f])
		}
	}
}

func TestNetInputGatherMultipleInterfaces(t *testing.T) {
	input := &NetInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// All metrics should be "net" with Counter type
	for _, m := range metrics {
		if m.Name() != "net" {
			t.Errorf("unexpected metric name: %q", m.Name())
		}
		if m.Type() != collector.Counter {
			t.Errorf("metric type should be Counter, got %v", m.Type())
		}
	}
}

func TestNetInputSampleConfig(t *testing.T) {
	input := &NetInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
