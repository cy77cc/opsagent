package minmax

import (
	"fmt"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// Config holds the minmax aggregator configuration.
type Config struct {
	Fields []string `mapstructure:"fields"`
}

// Aggregator tracks minimum and maximum values for specified fields.
type Aggregator struct {
	mu     sync.Mutex
	fields []string
	mins   map[string]float64
	maxs   map[string]float64
	tags   map[string]string
	name   string
}

// New creates a new minmax Aggregator from the given config.
func New(cfg Config) *Aggregator {
	return &Aggregator{
		fields: cfg.Fields,
		mins:   make(map[string]float64),
		maxs:   make(map[string]float64),
		tags:   make(map[string]string),
	}
}

// Init parses configuration from a map (e.g. from YAML unmarshaling).
// Expects "fields" as a []interface{} of field name strings.
func (a *Aggregator) Init(cfg map[string]interface{}) error {
	a.mins = make(map[string]float64)
	a.maxs = make(map[string]float64)
	a.tags = make(map[string]string)

	raw, ok := cfg["fields"]
	if !ok {
		return nil
	}
	fieldList, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("minmax: \"fields\" must be a list, got %T", raw)
	}
	a.fields = make([]string, 0, len(fieldList))
	for i, entry := range fieldList {
		name, ok := entry.(string)
		if !ok {
			return fmt.Errorf("minmax: field entry %d must be a string, got %T", i, entry)
		}
		a.fields = append(a.fields, name)
	}
	return nil
}

// Add accumulates min/max values from the given metric.
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

		if _, exists := a.mins[fname]; !exists {
			a.mins[fname] = fv
			a.maxs[fname] = fv
		} else {
			if fv < a.mins[fname] {
				a.mins[fname] = fv
			}
			if fv > a.maxs[fname] {
				a.maxs[fname] = fv
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

// Push emits min and max values to the accumulator as separate metrics.
func (a *Aggregator) Push(acc collector.Accumulator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.mins) == 0 {
		return
	}

	minFields := make(map[string]interface{})
	maxFields := make(map[string]interface{})
	for _, fname := range a.fields {
		minVal, ok := a.mins[fname]
		if !ok {
			continue
		}
		minFields[fname] = minVal
		maxFields[fname] = a.maxs[fname]
	}

	if len(minFields) > 0 {
		acc.AddGaugeWithTimestamp(a.name+"_min", a.tags, minFields, time.Now())
	}
	if len(maxFields) > 0 {
		acc.AddGaugeWithTimestamp(a.name+"_max", a.tags, maxFields, time.Now())
	}
}

// Reset clears all accumulated state.
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.mins = make(map[string]float64)
	a.maxs = make(map[string]float64)
	a.tags = make(map[string]string)
	a.name = ""
}

// SampleConfig returns a sample TOML configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["cpu_usage_percent", "memory_used_percent"]
`
}

func init() {
	collector.RegisterAggregator("minmax", func() collector.Aggregator {
		return &Aggregator{
			mins: make(map[string]float64),
			maxs: make(map[string]float64),
			tags: make(map[string]string),
		}
	})
}
