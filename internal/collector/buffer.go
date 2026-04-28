package collector

import "sync"

// DropPolicy determines what happens when the buffer is full.
type DropPolicy int

const (
	DropNewest DropPolicy = iota
	DropOldest
)

// Buffer is a bounded, thread-safe metric buffer with batch retrieval.
type Buffer struct {
	mu         sync.Mutex
	metrics    []*Metric
	maxSize    int
	batchSize  int
	dropPolicy DropPolicy
}

// NewBuffer creates a Buffer with the given capacity, batch size, and drop policy.
func NewBuffer(maxSize, batchSize int, policy DropPolicy) *Buffer {
	return &Buffer{
		metrics:    make([]*Metric, 0, maxSize),
		maxSize:    maxSize,
		batchSize:  batchSize,
		dropPolicy: policy,
	}
}

// Add inserts a metric into the buffer, applying the drop policy when full.
func (b *Buffer) Add(m *Metric) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.metrics) >= b.maxSize {
		switch b.dropPolicy {
		case DropNewest:
			return // drop incoming metric
		case DropOldest:
			b.metrics = b.metrics[1:] // remove oldest
		}
	}
	b.metrics = append(b.metrics, m)
}

// Batch returns up to batchSize metrics and removes them from the buffer.
func (b *Buffer) Batch() []*Metric {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := b.batchSize
	if n > len(b.metrics) {
		n = len(b.metrics)
	}

	batch := make([]*Metric, n)
	copy(batch, b.metrics[:n])
	b.metrics = b.metrics[n:]
	return batch
}

// Len returns the number of metrics currently in the buffer.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.metrics)
}
