package delta

import (
	"sync"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestFirstCollectionOutputsZero(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes", "write_bytes"},
		"output": "delta",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	now := time.Now()
	m := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{
			"read_bytes":  int64(1000),
			"write_bytes": int64(500),
			"other_field": int64(999),
		},
		collector.Counter, now,
	)

	result := p.Apply([]*collector.Metric{m})
	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	fields := result[0].Fields()
	if v, ok := fields["read_bytes"]; !ok {
		t.Fatal("expected read_bytes field")
	} else if v.(int64) != 0 {
		t.Errorf("expected read_bytes=0 on first collection, got %v", v)
	}
	if v, ok := fields["write_bytes"]; !ok {
		t.Fatal("expected write_bytes field")
	} else if v.(int64) != 0 {
		t.Errorf("expected write_bytes=0 on first collection, got %v", v)
	}
	// Non-configured fields should pass through unchanged.
	if v, ok := fields["other_field"]; !ok {
		t.Fatal("expected other_field to pass through")
	} else if v.(int64) != 999 {
		t.Errorf("expected other_field=999, got %v", v)
	}
}

func TestConsecutiveCollectionsDelta(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes"},
		"output": "delta",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	m0 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, t0,
	)
	result := p.Apply([]*collector.Metric{m0})
	if result[0].Fields()["read_bytes"].(int64) != 0 {
		t.Fatalf("first collection should be 0, got %v", result[0].Fields()["read_bytes"])
	}

	m1 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(250)},
		collector.Counter, t1,
	)
	result = p.Apply([]*collector.Metric{m1})
	if result[0].Fields()["read_bytes"].(int64) != 150 {
		t.Errorf("expected delta=150, got %v", result[0].Fields()["read_bytes"])
	}
}

func TestConsecutiveCollectionsRate(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes"},
		"output": "rate",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	m0 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, t0,
	)
	p.Apply([]*collector.Metric{m0})

	m1 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(200)},
		collector.Counter, t1,
	)
	result := p.Apply([]*collector.Metric{m1})
	// delta=100, elapsed=10s => rate=10.0
	expected := float64(10.0)
	actual := result[0].Fields()["read_bytes"].(float64)
	if actual != expected {
		t.Errorf("expected rate=%v, got %v", expected, actual)
	}
}

func TestCounterWrapOutputsZero(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes"},
		"output": "delta",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(10 * time.Second)

	m0 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(1000)},
		collector.Counter, t0,
	)
	p.Apply([]*collector.Metric{m0})

	// Counter wraps to a lower value.
	m1 := collector.NewMetric("diskio",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"read_bytes": int64(50)},
		collector.Counter, t1,
	)
	result := p.Apply([]*collector.Metric{m1})
	if result[0].Fields()["read_bytes"].(int64) != 0 {
		t.Errorf("expected 0 on counter wrap, got %v", result[0].Fields()["read_bytes"])
	}
}

func TestMixedTypes(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"int_field", "float_field"},
		"output": "delta",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(5 * time.Second)

	m0 := collector.NewMetric("mixed",
		map[string]string{},
		map[string]interface{}{
			"int_field":   int64(10),
			"float_field": float64(1.5),
		},
		collector.Counter, t0,
	)
	result := p.Apply([]*collector.Metric{m0})
	if result[0].Fields()["int_field"].(int64) != 0 {
		t.Errorf("expected int_field=0 on first collection, got %v", result[0].Fields()["int_field"])
	}
	if result[0].Fields()["float_field"].(float64) != 0 {
		t.Errorf("expected float_field=0 on first collection, got %v", result[0].Fields()["float_field"])
	}

	m1 := collector.NewMetric("mixed",
		map[string]string{},
		map[string]interface{}{
			"int_field":   int64(30),
			"float_field": float64(4.5),
		},
		collector.Counter, t1,
	)
	result = p.Apply([]*collector.Metric{m1})
	if result[0].Fields()["int_field"].(int64) != 20 {
		t.Errorf("expected int_field delta=20, got %v", result[0].Fields()["int_field"])
	}
	if result[0].Fields()["float_field"].(float64) != 3.0 {
		t.Errorf("expected float_field delta=3.0, got %v", result[0].Fields()["float_field"])
	}
}

func TestMissingFieldSkipped(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes", "write_bytes"},
		"output": "delta",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(5 * time.Second)

	// First collection with only read_bytes.
	m0 := collector.NewMetric("diskio",
		map[string]string{"host": "s1"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, t0,
	)
	p.Apply([]*collector.Metric{m0})

	// Second collection with only write_bytes (read_bytes missing).
	m1 := collector.NewMetric("diskio",
		map[string]string{"host": "s1"},
		map[string]interface{}{"write_bytes": int64(50)},
		collector.Counter, t1,
	)
	result := p.Apply([]*collector.Metric{m1})
	fields := result[0].Fields()

	// read_bytes is missing, so it should not appear in output.
	if _, ok := fields["read_bytes"]; ok {
		t.Error("expected read_bytes to be absent (field missing in input)")
	}
	// write_bytes is present for the first time, so output should be 0.
	if v, ok := fields["write_bytes"]; !ok {
		t.Fatal("expected write_bytes field")
	} else if v.(int64) != 0 {
		t.Errorf("expected write_bytes=0 on first appearance, got %v", v)
	}
}

func TestStaleEntryCleanup(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields":            []interface{}{"read_bytes"},
		"output":            "delta",
		"max_stale_seconds": 1,
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t0 := time.Now()
	t1 := t0.Add(2 * time.Second) // Beyond max_stale_seconds=1

	m0 := collector.NewMetric("diskio",
		map[string]string{"host": "s1"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, t0,
	)
	result := p.Apply([]*collector.Metric{m0})
	if result[0].Fields()["read_bytes"].(int64) != 0 {
		t.Fatalf("first collection should be 0")
	}

	// Verify internal state has the entry.
	p.mu.Lock()
	entryCount := len(p.prev)
	p.mu.Unlock()
	if entryCount != 1 {
		t.Fatalf("expected 1 tracked entry, got %d", entryCount)
	}

	// Apply a metric from a different series to trigger stale cleanup.
	m1 := collector.NewMetric("diskio",
		map[string]string{"host": "s2"},
		map[string]interface{}{"read_bytes": int64(50)},
		collector.Counter, t1,
	)
	p.Apply([]*collector.Metric{m1})

	// The stale entry from s1 should have been cleaned up.
	p.mu.Lock()
	_, staleExists := p.prev[metricKey("diskio", map[string]string{"host": "s1"})]
	_, freshExists := p.prev[metricKey("diskio", map[string]string{"host": "s2"})]
	p.mu.Unlock()

	if staleExists {
		t.Error("expected stale entry to be cleaned up")
	}
	if !freshExists {
		t.Error("expected fresh entry to still exist")
	}
}

func TestConcurrentSafety(t *testing.T) {
	p := &Processor{}
	if err := p.Init(map[string]interface{}{
		"fields": []interface{}{"read_bytes"},
		"output": "rate",
	}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	var wg sync.WaitGroup
	baseTime := time.Now()

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ts := baseTime.Add(time.Duration(i) * time.Second)
			m := collector.NewMetric("diskio",
				map[string]string{"host": "server-01"},
				map[string]interface{}{"read_bytes": int64(100 * (i + 1))},
				collector.Counter, ts,
			)
			p.Apply([]*collector.Metric{m})
		}(i)
	}

	wg.Wait()
	// If we get here without panic, the test passes.
}
