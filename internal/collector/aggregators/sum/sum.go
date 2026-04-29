package sum

import (
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// Config holds the sum aggregator configuration.
type Config struct {
	Fields []string `mapstructure:"fields"`
}

// Aggregator computes running sums for specified fields.
type Aggregator struct {
	mu     sync.Mutex
	fields []string
	sums   map[string]interface{}
	isInt  map[string]bool
	tags   map[string]string
	name   string
}

// New creates a new sum Aggregator from the given config.
func New(cfg Config) *Aggregator {
	return &Aggregator{
		fields: cfg.Fields,
		sums:   make(map[string]interface{}),
		isInt:  make(map[string]bool),
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
		switch v := val.(type) {
		case float64:
			if existing, exists := a.sums[fname]; exists {
				switch ev := existing.(type) {
				case float64:
					a.sums[fname] = ev + v
				case int64:
					a.sums[fname] = float64(ev) + v
					a.isInt[fname] = false
				}
			} else {
				a.sums[fname] = v
				a.isInt[fname] = false
			}
		case int64:
			if existing, exists := a.sums[fname]; exists {
				switch ev := existing.(type) {
				case int64:
					a.sums[fname] = ev + v
				case float64:
					a.sums[fname] = ev + float64(v)
					a.isInt[fname] = false
				}
			} else {
				a.sums[fname] = v
				a.isInt[fname] = true
			}
		}
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

// Push emits summed values to the accumulator as counters.
func (a *Aggregator) Push(acc collector.Accumulator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.sums) == 0 {
		return
	}

	fields := make(map[string]interface{})
	for _, fname := range a.fields {
		val, ok := a.sums[fname]
		if !ok {
			continue
		}
		fields[fname] = val
	}

	if len(fields) > 0 {
		acc.AddCounterWithTimestamp(a.name+"_sum", a.tags, fields, time.Now())
	}
}

// Reset clears all accumulated state.
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sums = make(map[string]interface{})
	a.isInt = make(map[string]bool)
	a.tags = make(map[string]string)
	a.name = ""
}

// SampleConfig returns a sample TOML configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["bytes_sent", "request_count"]
`
}

func init() {
	collector.RegisterAggregator("sum", func() collector.Aggregator {
		return New(Config{})
	})
}
