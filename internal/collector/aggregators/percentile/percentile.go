package percentile

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// defaultPercentiles are used when none are specified in config.
var defaultPercentiles = []float64{50, 95, 99}

// Aggregator computes percentiles for specified fields.
type Aggregator struct {
	mu          sync.Mutex
	fields      []string
	percentiles []float64
	values      map[string][]float64
	tags        map[string]string
	name        string
}

// Init parses configuration from a map (e.g. from YAML unmarshaling).
// Expects "fields" as a []interface{} of field name strings,
// and optionally "percentiles" as a []interface{} of numeric percentile values.
func (a *Aggregator) Init(cfg map[string]interface{}) error {
	a.values = make(map[string][]float64)
	a.tags = make(map[string]string)
	a.percentiles = make([]float64, len(defaultPercentiles))
	copy(a.percentiles, defaultPercentiles)

	// Parse fields.
	raw, ok := cfg["fields"]
	if !ok {
		return nil
	}
	fieldList, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("percentile: \"fields\" must be a list, got %T", raw)
	}
	a.fields = make([]string, 0, len(fieldList))
	for i, entry := range fieldList {
		name, ok := entry.(string)
		if !ok {
			return fmt.Errorf("percentile: field entry %d must be a string, got %T", i, entry)
		}
		a.fields = append(a.fields, name)
	}

	// Parse percentiles.
	if raw, ok := cfg["percentiles"]; ok {
		pList, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("percentile: \"percentiles\" must be a list, got %T", raw)
		}
		a.percentiles = make([]float64, 0, len(pList))
		for i, entry := range pList {
			var pv float64
			switch v := entry.(type) {
			case float64:
				pv = v
			case int64:
				pv = float64(v)
			case int:
				pv = float64(v)
			default:
				return fmt.Errorf("percentile: percentile entry %d must be a number, got %T", i, entry)
			}
			if pv < 0 || pv > 100 {
				return fmt.Errorf("percentile: value at index %d must be between 0 and 100, got %v", i, pv)
			}
			if pv != float64(int(pv)) {
				return fmt.Errorf("percentile: value at index %d must be a whole number, got %v", i, pv)
			}
			a.percentiles = append(a.percentiles, pv)
		}
	}

	return nil
}

// Add collects values from the given metric.
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
		a.values[fname] = append(a.values[fname], fv)
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

// Push computes percentiles and emits them to the accumulator as gauge metrics.
func (a *Aggregator) Push(acc collector.Accumulator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.values) == 0 {
		return
	}

	fields := make(map[string]interface{})
	for _, fname := range a.fields {
		vals, ok := a.values[fname]
		if !ok || len(vals) == 0 {
			continue
		}
		sort.Float64s(vals)
		for _, p := range a.percentiles {
			key := fmt.Sprintf("%s_p%d", fname, int(p))
			fields[key] = percentile(vals, p)
		}
	}

	if len(fields) > 0 {
		acc.AddGaugeWithTimestamp(a.name, a.tags, fields, time.Now())
	}
}

// Reset clears all accumulated state.
func (a *Aggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.values = make(map[string][]float64)
	a.tags = make(map[string]string)
	a.name = ""
}

// SampleConfig returns a sample configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["response_time_ms", "latency_ms"]
percentiles = [50, 95, 99]
`
}

// percentile computes the p-th percentile of a sorted slice using linear interpolation.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	index := p / 100 * float64(len(sorted)-1)
	lower := int(math.Floor(index))
	upper := int(math.Ceil(index))
	if lower == upper {
		return sorted[lower]
	}
	return sorted[lower] + (sorted[upper]-sorted[lower])*(index-float64(lower))
}

func init() {
	collector.RegisterAggregator("percentile", func() collector.Aggregator {
		return &Aggregator{}
	})
}
