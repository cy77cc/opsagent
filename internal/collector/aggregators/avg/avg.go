package avg

import (
	"sync"
	"time"

	"nodeagentx/internal/collector"
)

// Config holds the avg aggregator configuration.
type Config struct {
	Fields []string `mapstructure:"fields"`
}

// Aggregator computes running averages for specified fields.
type Aggregator struct {
	mu     sync.Mutex
	fields []string
	sums   map[string]float64
	counts map[string]int
	tags   map[string]string
	name   string
}

// New creates a new avg Aggregator from the given config.
func New(cfg Config) *Aggregator {
	return &Aggregator{
		fields: cfg.Fields,
		sums:   make(map[string]float64),
		counts: make(map[string]int),
		tags:   make(map[string]string),
	}
}

// Add accumulates values from the given metric.
func (a *Aggregator) Add(in *collector.Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()

	metricFields := in.Fields()
	for _, fname := range a.fields {
		val, ok := metricFields[fname]
		if !ok {
			continue
		}
		var fv float64
		switch v := val.(type) {
		case float64:
			fv = v
		case int64:
			fv = float64(v)
		default:
			continue
		}
		a.sums[fname] += fv
		a.counts[fname]++
	}

	// Capture name and tags from the first metric that has matching fields.
	if a.name == "" {
		for _, fname := range a.fields {
			if _, ok := metricFields[fname]; ok {
				a.name = in.Name()
				tags := in.Tags()
				for k, v := range tags {
					a.tags[k] = v
				}
				break
			}
		}
	}
}

// Push emits averaged values to the accumulator.
func (a *Aggregator) Push(acc collector.Accumulator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.counts) == 0 {
		return
	}

	fields := make(map[string]interface{})
	for _, fname := range a.fields {
		count, ok := a.counts[fname]
		if !ok || count == 0 {
			continue
		}
		fields[fname] = a.sums[fname] / float64(count)
	}

	if len(fields) > 0 {
		acc.AddGaugeWithTimestamp(a.name+"_avg", a.tags, fields, time.Now())
	}
}

// Reset clears all accumulated state.
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sums = make(map[string]float64)
	a.counts = make(map[string]int)
	a.tags = make(map[string]string)
	a.name = ""
}

// SampleConfig returns a sample TOML configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["value", "latency"]
`
}

func init() {
	collector.RegisterAggregator("avg", func() collector.Aggregator {
		return New(Config{})
	})
}
