package avg

import (
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func TestNew(t *testing.T) {
	a := New(Config{Fields: []string{"value", "latency"}})
	if a == nil {
		t.Fatal("expected non-nil aggregator")
	}
}

func TestAddAndPushSingleField(t *testing.T) {
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
	if emitted.Name() != "cpu_avg" {
		t.Errorf("expected name cpu_avg, got %q", emitted.Name())
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
	if fv != 20.0 {
		t.Errorf("expected average 20.0, got %f", fv)
	}
}

func TestAddAndPushMultipleFields(t *testing.T) {
	a := New(Config{Fields: []string{"value", "count"}})

	m1 := collector.NewMetric("req",
		map[string]string{"env": "prod"},
		map[string]interface{}{"value": float64(100), "count": int64(5)},
		collector.Gauge, time.Now())
	m2 := collector.NewMetric("req",
		map[string]string{"env": "prod"},
		map[string]interface{}{"value": float64(200), "count": int64(15)},
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

	valValue, ok := fields["value"]
	if !ok {
		t.Fatal("expected 'value' field")
	}
	if valValue.(float64) != 150.0 {
		t.Errorf("expected value average 150.0, got %f", valValue.(float64))
	}

	valCount, ok := fields["count"]
	if !ok {
		t.Fatal("expected 'count' field")
	}
	if valCount.(float64) != 10.0 {
		t.Errorf("expected count average 10.0, got %f", valCount.(float64))
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
		map[string]string{"host": "server-01"},
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

func TestAddInt64FieldValues(t *testing.T) {
	a := New(Config{Fields: []string{"count"}})

	m1 := collector.NewMetric("requests",
		map[string]string{"host": "server"},
		map[string]interface{}{"count": int64(100)},
		collector.Counter, time.Now())
	m2 := collector.NewMetric("requests",
		map[string]string{"host": "server"},
		map[string]interface{}{"count": int64(300)},
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
	fv := val.(float64)
	if fv != 200.0 {
		t.Errorf("expected average 200.0, got %f", fv)
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
	f, ok := collector.DefaultRegistry.GetAggregator("avg")
	if !ok {
		t.Fatal("avg aggregator not registered in default registry")
	}
	a := f()
	if a == nil {
		t.Fatal("expected non-nil aggregator from factory")
	}
}
