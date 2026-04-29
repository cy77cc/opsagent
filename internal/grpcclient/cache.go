package grpcclient

import (
	"sync"

	"github.com/cy77cc/opsagent/internal/collector"
)

// MetricCache is a fixed-size ring buffer that stores metrics.
// When full, new entries overwrite the oldest.
type MetricCache struct {
	mu      sync.Mutex
	buf     []*collector.Metric
	maxSize int
	head    int // next write position
	tail    int // next read position
	count   int
}

// NewMetricCache creates a ring buffer cache with the given capacity.
func NewMetricCache(maxSize int) *MetricCache {
	if maxSize <= 0 {
		maxSize = 1
	}
	return &MetricCache{
		buf:     make([]*collector.Metric, maxSize),
		maxSize: maxSize,
	}
}

// Add inserts a metric into the ring buffer. If full, the oldest entry is overwritten.
func (c *MetricCache) Add(m *collector.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.buf[c.head] = m
	c.head = (c.head + 1) % c.maxSize

	if c.count == c.maxSize {
		// Buffer full — advance tail to drop oldest.
		c.tail = (c.tail + 1) % c.maxSize
	} else {
		c.count++
	}
}

// Drain returns all buffered metrics in FIFO order and clears the buffer.
func (c *MetricCache) Drain() []*collector.Metric {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.count == 0 {
		return nil
	}

	result := make([]*collector.Metric, c.count)
	for i := 0; i < c.count; i++ {
		idx := (c.tail + i) % c.maxSize
		result[i] = c.buf[idx]
		c.buf[idx] = nil // allow GC
	}

	c.head = 0
	c.tail = 0
	c.count = 0

	return result
}

// Len returns the number of metrics currently in the cache.
func (c *MetricCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}
