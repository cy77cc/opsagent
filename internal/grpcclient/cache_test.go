package grpcclient

import (
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func makeMetric(name string) *collector.Metric {
	return collector.NewMetric(
		name,
		map[string]string{"host": "test"},
		map[string]interface{}{"value": float64(1.0)},
		collector.Gauge,
		time.Now(),
	)
}

func TestCacheAddAndDrain(t *testing.T) {
	cache := NewMetricCache(5)

	m1 := makeMetric("cpu")
	m2 := makeMetric("mem")
	cache.Add(m1)
	cache.Add(m2)

	if got := cache.Len(); got != 2 {
		t.Fatalf("expected Len()=2, got %d", got)
	}

	drained := cache.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 drained, got %d", len(drained))
	}
	if drained[0].Name() != "cpu" || drained[1].Name() != "mem" {
		t.Fatalf("unexpected order: %s, %s", drained[0].Name(), drained[1].Name())
	}

	// After drain, cache should be empty.
	if got := cache.Len(); got != 0 {
		t.Fatalf("expected Len()=0 after drain, got %d", got)
	}
	if d := cache.Drain(); d != nil {
		t.Fatalf("expected nil from empty drain, got %v", d)
	}
}

func TestCacheOverflow(t *testing.T) {
	cache := NewMetricCache(3)

	cache.Add(makeMetric("a"))
	cache.Add(makeMetric("b"))
	cache.Add(makeMetric("c"))
	cache.Add(makeMetric("d")) // overwrites "a"

	if got := cache.Len(); got != 3 {
		t.Fatalf("expected Len()=3, got %d", got)
	}

	drained := cache.Drain()
	if len(drained) != 3 {
		t.Fatalf("expected 3 drained, got %d", len(drained))
	}
	// Oldest "a" should be gone; order should be b, c, d.
	names := []string{drained[0].Name(), drained[1].Name(), drained[2].Name()}
	expected := []string{"b", "c", "d"}
	for i, n := range names {
		if n != expected[i] {
			t.Fatalf("index %d: expected %q, got %q", i, expected[i], n)
		}
	}
}

func TestCacheLen(t *testing.T) {
	cache := NewMetricCache(10)
	if got := cache.Len(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	cache.Add(makeMetric("x"))
	if got := cache.Len(); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestCacheSingleCapacity(t *testing.T) {
	cache := NewMetricCache(1)
	cache.Add(makeMetric("first"))
	cache.Add(makeMetric("second")) // overwrites first

	drained := cache.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 drained, got %d", len(drained))
	}
	if drained[0].Name() != "second" {
		t.Fatalf("expected 'second', got %q", drained[0].Name())
	}
}

func TestCacheZeroCapacityDefaultsToOne(t *testing.T) {
	cache := NewMetricCache(0)
	if cache.maxSize != 1 {
		t.Fatalf("expected maxSize=1 for zero input, got %d", cache.maxSize)
	}
}
