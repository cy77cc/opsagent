package collector

import (
	"sync"
	"time"
)

type accumulator struct {
	mu      sync.Mutex
	metrics []*Metric
	maxSize int
}

// NewAccumulator creates an Accumulator with the given max buffer size.
// When full, new metrics are dropped (DropNewest policy).
func NewAccumulator(maxSize int) Accumulator {
	return &accumulator{
		metrics: make([]*Metric, 0, maxSize),
		maxSize: maxSize,
	}
}

func (a *accumulator) AddFields(name string, tags map[string]string, fields map[string]interface{}) {
	a.add(name, tags, fields, Gauge, time.Now())
}

func (a *accumulator) AddGauge(name string, tags map[string]string, fields map[string]interface{}) {
	a.add(name, tags, fields, Gauge, time.Now())
}

func (a *accumulator) AddCounter(name string, tags map[string]string, fields map[string]interface{}) {
	a.add(name, tags, fields, Counter, time.Now())
}

func (a *accumulator) AddFieldsWithTimestamp(name string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	a.add(name, tags, fields, Gauge, ts)
}

func (a *accumulator) add(name string, tags map[string]string, fields map[string]interface{}, mt MetricType, ts time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.metrics) >= a.maxSize {
		return // DropNewest
	}
	a.metrics = append(a.metrics, NewMetric(name, tags, fields, mt, ts))
}

// Collect returns accumulated metrics and resets the buffer.
func (a *accumulator) Collect() []*Metric {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := a.metrics
	a.metrics = make([]*Metric, 0, a.maxSize)
	return result
}
