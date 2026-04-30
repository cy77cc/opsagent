package minmax

import (
	"sync"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestSingleValueMinMax(t *testing.T) {
	a := New(Config{Fields: []string{"cpu_usage_percent"}})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"cpu_usage_percent": float64(42.5)},
		collector.Gauge, time.Now())

	a.Add(m)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 2 {
		t.Fatalf("expected 2 metrics (min+max), got %d", len(collected))
	}

	names := map[string]float64{}
	for _, c := range collected {
		fields := c.Fields()
		for _, v := range fields {
			fv, ok := v.(float64)
			if !ok {
				t.Fatalf("expected float64, got %T", v)
			}
			names[c.Name()] = fv
		}
	}
	if names["cpu_min"] != 42.5 {
		t.Errorf("expected cpu_min=42.5, got %v", names["cpu_min"])
	}
	if names["cpu_max"] != 42.5 {
		t.Errorf("expected cpu_max=42.5, got %v", names["cpu_max"])
	}
}

func TestMultiValueMinMax(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	for _, v := range []float64{50, 10, 90, 30, 70} {
		m := collector.NewMetric("metric",
			map[string]string{"k": "v"},
			map[string]interface{}{"val": v},
			collector.Gauge, time.Now())
		a.Add(m)
	}

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(collected))
	}

	names := map[string]float64{}
	for _, c := range collected {
		fields := c.Fields()
		for _, v := range fields {
			fv, ok := v.(float64)
			if !ok {
				t.Fatalf("expected float64, got %T", v)
			}
			names[c.Name()] = fv
		}
	}
	if names["metric_min"] != 10.0 {
		t.Errorf("expected min=10, got %v", names["metric_min"])
	}
	if names["metric_max"] != 90.0 {
		t.Errorf("expected max=90, got %v", names["metric_max"])
	}
}

func TestReset(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	m := collector.NewMetric("m",
		map[string]string{"k": "v"},
		map[string]interface{}{"val": float64(42)},
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

func TestPushEmpty(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics from empty aggregator, got %d", len(collected))
	}
}

func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetAggregator("minmax")
	if !ok {
		t.Fatal("minmax aggregator not registered in default registry")
	}
	a := f()
	if a == nil {
		t.Fatal("expected non-nil aggregator from factory")
	}
}

func TestConcurrentSafety(t *testing.T) {
	a := New(Config{Fields: []string{"cpu_usage_percent"}})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m := collector.NewMetric("cpu",
				map[string]string{"host": "server-01"},
				map[string]interface{}{"cpu_usage_percent": float64(n * 10)},
				collector.Gauge, time.Now())
			a.Add(m)
		}(i)
	}
	wg.Wait()
	// No panic = pass.
}
