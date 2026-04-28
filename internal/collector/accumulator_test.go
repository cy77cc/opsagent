package collector

import (
	"testing"
	"time"
)

func TestAccumulatorAddFields(t *testing.T) {
	acc := NewAccumulator(100)
	acc.AddFields("cpu", map[string]string{"host": "s1"}, map[string]interface{}{"usage": 75.5})

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("Collect() len = %d, want 1", len(metrics))
	}
	m := metrics[0]
	if m.Name() != "cpu" {
		t.Errorf("Name() = %q, want %q", m.Name(), "cpu")
	}
	if m.Tags()["host"] != "s1" {
		t.Errorf("Tags[host] = %q, want %q", m.Tags()["host"], "s1")
	}
	if m.Fields()["usage"] != 75.5 {
		t.Errorf("Fields[usage] = %v, want 75.5", m.Fields()["usage"])
	}
	if m.Type() != Gauge {
		t.Errorf("Type() = %v, want %v (Gauge)", m.Type(), Gauge)
	}
}

func TestAccumulatorAddCounter(t *testing.T) {
	acc := NewAccumulator(100)
	acc.AddCounter("requests", map[string]string{"path": "/api"}, map[string]interface{}{"count": int64(10)})

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("Collect() len = %d, want 1", len(metrics))
	}
	m := metrics[0]
	if m.Type() != Counter {
		t.Errorf("Type() = %v, want %v (Counter)", m.Type(), Counter)
	}
	if m.Fields()["count"] != int64(10) {
		t.Errorf("Fields[count] = %v, want 10", m.Fields()["count"])
	}
}

func TestAccumulatorOverflow(t *testing.T) {
	acc := NewAccumulator(3)

	acc.AddFields("a", nil, map[string]interface{}{"v": 1})
	acc.AddFields("b", nil, map[string]interface{}{"v": 2})
	acc.AddFields("c", nil, map[string]interface{}{"v": 3})
	acc.AddFields("d", nil, map[string]interface{}{"v": 4}) // should be dropped

	metrics := acc.Collect()
	if len(metrics) != 3 {
		t.Fatalf("Collect() len = %d, want 3 (drop newest)", len(metrics))
	}
	// First three should be a, b, c (drop newest policy)
	if metrics[0].Name() != "a" {
		t.Errorf("metrics[0].Name() = %q, want 'a'", metrics[0].Name())
	}
	if metrics[1].Name() != "b" {
		t.Errorf("metrics[1].Name() = %q, want 'b'", metrics[1].Name())
	}
	if metrics[2].Name() != "c" {
		t.Errorf("metrics[2].Name() = %q, want 'c'", metrics[2].Name())
	}
}

func TestAccumulatorCustomTimestamp(t *testing.T) {
	acc := NewAccumulator(100)
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	acc.AddFieldsWithTimestamp("cpu", nil, map[string]interface{}{"v": 1}, ts)

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("Collect() len = %d, want 1", len(metrics))
	}
	if !metrics[0].Timestamp().Equal(ts) {
		t.Errorf("Timestamp() = %v, want %v", metrics[0].Timestamp(), ts)
	}
}

func TestAccumulatorCollectResets(t *testing.T) {
	acc := NewAccumulator(100)
	acc.AddFields("a", nil, map[string]interface{}{"v": 1})

	metrics1 := acc.Collect()
	if len(metrics1) != 1 {
		t.Fatalf("first Collect() len = %d, want 1", len(metrics1))
	}

	metrics2 := acc.Collect()
	if len(metrics2) != 0 {
		t.Fatalf("second Collect() len = %d, want 0 (buffer should be reset)", len(metrics2))
	}
}
