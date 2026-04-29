package sum

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestNew(t *testing.T) {
	a := New(Config{Fields: []string{"value"}})
	if a == nil {
		t.Fatal("expected non-nil aggregator")
	}
}

func TestAddAndPushFloat64(t *testing.T) {
	a := New(Config{Fields: []string{"value"}})

	m1 := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(10)},
		collector.Gauge, time.Now())
	m2 := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(20)},
		collector.Gauge, time.Now())
	m3 := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(30)},
		collector.Gauge, time.Now())

	a.Add(m1)
	a.Add(m2)
	a.Add(m3)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(collected))
	}

	emitted := collected[0]
	if emitted.Name() != "cpu_sum" {
		t.Errorf("expected name cpu_sum, got %q", emitted.Name())
	}

	fields := emitted.Fields()
	val, ok := fields["value"]
	if !ok {
		t.Fatal("expected 'value' field in emitted metric")
	}
	fv, ok := val.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", val)
	}
	if fv != 60.0 {
		t.Errorf("expected sum 60.0, got %f", fv)
	}
}

func TestAddAndPushInt64(t *testing.T) {
	a := New(Config{Fields: []string{"count"}})

	m1 := collector.NewMetric("requests",
		map[string]string{"host": "server"},
		map[string]interface{}{"count": int64(100)},
		collector.Counter, time.Now())
	m2 := collector.NewMetric("requests",
		map[string]string{"host": "server"},
		map[string]interface{}{"count": int64(200)},
		collector.Counter, time.Now())

	a.Add(m1)
	a.Add(m2)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(collected))
	}

	fields := collected[0].Fields()
	val, ok := fields["count"]
	if !ok {
		t.Fatal("expected 'count' field")
	}
	iv, ok := val.(int64)
	if !ok {
		t.Fatalf("expected int64, got %T", val)
	}
	if iv != 300 {
		t.Errorf("expected sum 300, got %d", iv)
	}
}

func TestAddMixedIntAndFloat(t *testing.T) {
	a := New(Config{Fields: []string{"value"}})

	// Start with int64, then add float64 - should promote to float64.
	m1 := collector.NewMetric("mixed",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": int64(10)},
		collector.Gauge, time.Now())
	m2 := collector.NewMetric("mixed",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(5.5)},
		collector.Gauge, time.Now())

	a.Add(m1)
	a.Add(m2)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(collected))
	}

	fields := collected[0].Fields()
	val, ok := fields["value"]
	if !ok {
		t.Fatal("expected 'value' field")
	}
	fv, ok := val.(float64)
	if !ok {
		t.Fatalf("expected float64 after promotion, got %T", val)
	}
	if fv != 15.5 {
		t.Errorf("expected sum 15.5, got %f", fv)
	}
}

func TestAddMultipleFields(t *testing.T) {
	a := New(Config{Fields: []string{"bytes", "packets"}})

	m1 := collector.NewMetric("network",
		map[string]string{"iface": "eth0"},
		map[string]interface{}{"bytes": int64(1000), "packets": int64(10)},
		collector.Counter, time.Now())
	m2 := collector.NewMetric("network",
		map[string]string{"iface": "eth0"},
		map[string]interface{}{"bytes": int64(2000), "packets": int64(20)},
		collector.Counter, time.Now())

	a.Add(m1)
	a.Add(m2)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(collected))
	}

	fields := collected[0].Fields()

	bytesVal, ok := fields["bytes"]
	if !ok {
		t.Fatal("expected 'bytes' field")
	}
	if bytesVal.(int64) != 3000 {
		t.Errorf("expected bytes sum 3000, got %d", bytesVal.(int64))
	}

	packetsVal, ok := fields["packets"]
	if !ok {
		t.Fatal("expected 'packets' field")
	}
	if packetsVal.(int64) != 30 {
		t.Errorf("expected packets sum 30, got %d", packetsVal.(int64))
	}
}

func TestPushEmptyAggregator(t *testing.T) {
	a := New(Config{Fields: []string{"value"}})

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics from empty aggregator, got %d", len(collected))
	}
}

func TestReset(t *testing.T) {
	a := New(Config{Fields: []string{"value"}})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())
	a.Add(m)

	a.Reset()

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics after reset, got %d", len(collected))
	}
}

func TestAddSkipsNonMatchingFields(t *testing.T) {
	a := New(Config{Fields: []string{"nonexistent"}})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())

	a.Add(m)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics for non-matching fields, got %d", len(collected))
	}
}

func TestSampleConfig(t *testing.T) {
	a := New(Config{})
	cfg := a.SampleConfig()
	if cfg == "" {
		t.Error("expected non-empty sample config")
	}
}

func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetAggregator("sum")
	if !ok {
		t.Fatal("sum aggregator not registered in default registry")
	}
	a := f()
	if a == nil {
		t.Fatal("expected non-nil aggregator from factory")
	}
}
