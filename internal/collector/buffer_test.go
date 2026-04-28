package collector

import (
	"testing"
	"time"
)

func TestBufferAddAndBatch(t *testing.T) {
	buf := NewBuffer(100, 3, DropNewest)

	for i := 0; i < 5; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	if buf.Len() != 5 {
		t.Fatalf("Len() = %d, want 5", buf.Len())
	}

	// First batch: batchSize=3
	batch := buf.Batch()
	if len(batch) != 3 {
		t.Fatalf("first Batch() len = %d, want 3", len(batch))
	}
	if buf.Len() != 2 {
		t.Fatalf("Len() after first batch = %d, want 2", buf.Len())
	}

	// Second batch: remaining 2
	batch = buf.Batch()
	if len(batch) != 2 {
		t.Fatalf("second Batch() len = %d, want 2", len(batch))
	}
	if buf.Len() != 0 {
		t.Fatalf("Len() after second batch = %d, want 0", buf.Len())
	}
}

func TestBufferDropNewest(t *testing.T) {
	buf := NewBuffer(3, 10, DropNewest)

	for i := 0; i < 5; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	if buf.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 (maxSize)", buf.Len())
	}

	batch := buf.Batch()
	// Should have first 3 (0, 1, 2); 3 and 4 dropped
	if len(batch) != 3 {
		t.Fatalf("Batch() len = %d, want 3", len(batch))
	}
	for i, m := range batch {
		if m.Fields()["v"] != float64(i) {
			t.Errorf("batch[%d].Fields[v] = %v, want %v", i, m.Fields()["v"], float64(i))
		}
	}
}

func TestBufferDropOldest(t *testing.T) {
	buf := NewBuffer(3, 10, DropOldest)

	for i := 0; i < 5; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	if buf.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 (maxSize)", buf.Len())
	}

	batch := buf.Batch()
	// Should have last 3 (2, 3, 4); 0 and 1 dropped
	if len(batch) != 3 {
		t.Fatalf("Batch() len = %d, want 3", len(batch))
	}
	for i, m := range batch {
		expected := float64(i + 2)
		if m.Fields()["v"] != expected {
			t.Errorf("batch[%d].Fields[v] = %v, want %v", i, m.Fields()["v"], expected)
		}
	}
}

func TestBufferLen(t *testing.T) {
	buf := NewBuffer(10, 5, DropNewest)

	if buf.Len() != 0 {
		t.Fatalf("initial Len() = %d, want 0", buf.Len())
	}

	m := NewMetric("test", nil, nil, Gauge, time.Now())
	buf.Add(m)

	if buf.Len() != 1 {
		t.Fatalf("Len() after Add = %d, want 1", buf.Len())
	}
}

func TestBufferEmptyBatch(t *testing.T) {
	buf := NewBuffer(10, 5, DropNewest)

	batch := buf.Batch()
	if len(batch) != 0 {
		t.Fatalf("Batch() on empty buffer len = %d, want 0", len(batch))
	}
}
