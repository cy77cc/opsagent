package delta

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// entry holds the previous values and timestamp for a metric series.
type entry struct {
	values    map[string]interface{}
	timestamp time.Time
}

// Processor computes delta or rate from cumulative counter metrics.
type Processor struct {
	fields          map[string]bool
	outputMode      string // "delta" or "rate"
	maxStaleSeconds int
	prev            map[string]entry
	mu              sync.Mutex
}

// metricKey builds a unique key from the metric name and sorted tags.
func metricKey(name string, tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(tags[k])
	}
	return b.String()
}

// Init parses configuration from a map (e.g. from YAML unmarshaling).
func (p *Processor) Init(cfg map[string]interface{}) error {
	p.prev = make(map[string]entry)
	p.outputMode = "rate"
	p.maxStaleSeconds = 300

	if rawFields, ok := cfg["fields"]; ok {
		fieldList, ok := rawFields.([]interface{})
		if !ok {
			return fmt.Errorf("delta: \"fields\" must be a list, got %T", rawFields)
		}
		p.fields = make(map[string]bool, len(fieldList))
		for i, f := range fieldList {
			name, ok := f.(string)
			if !ok {
				return fmt.Errorf("delta: field entry %d must be a string, got %T", i, f)
			}
			p.fields[name] = true
		}
	} else {
		return fmt.Errorf("delta: \"fields\" is required")
	}

	if rawOutput, ok := cfg["output"]; ok {
		mode, ok := rawOutput.(string)
		if !ok {
			return fmt.Errorf("delta: \"output\" must be a string, got %T", rawOutput)
		}
		if mode != "delta" && mode != "rate" {
			return fmt.Errorf("delta: \"output\" must be \"delta\" or \"rate\", got %q", mode)
		}
		p.outputMode = mode
	}

	if rawStale, ok := cfg["max_stale_seconds"]; ok {
		switch v := rawStale.(type) {
		case int:
			p.maxStaleSeconds = v
		case int64:
			p.maxStaleSeconds = int(v)
		case float64:
			p.maxStaleSeconds = int(v)
		default:
			return fmt.Errorf("delta: \"max_stale_seconds\" must be a number, got %T", rawStale)
		}
	}

	return nil
}

// Apply transforms metrics by computing delta or rate for configured fields.
func (p *Processor) Apply(in []*collector.Metric) []*collector.Metric {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Use the latest incoming timestamp as the reference for stale cleanup.
	var latestTS time.Time
	for _, m := range in {
		if m.Timestamp().After(latestTS) {
			latestTS = m.Timestamp()
		}
	}
	if !latestTS.IsZero() {
		p.cleanupStale(latestTS)
	}

	result := make([]*collector.Metric, 0, len(in))
	for _, m := range in {
		key := metricKey(m.Name(), m.Tags())
		prev, exists := p.prev[key]

		fields := m.Fields()
		newFields := make(map[string]interface{}, len(fields))
		for k, v := range fields {
			newFields[k] = v
		}

		for fieldName := range p.fields {
			curVal, ok := fields[fieldName]
			if !ok {
				// Field not present in this metric; skip.
				continue
			}

			if !exists {
				// First collection: output zero.
				newFields[fieldName] = zeroOfSameType(curVal)
				continue
			}

			prevVal, ok := prev.values[fieldName]
			if !ok {
				// Field appeared for the first time.
				newFields[fieldName] = zeroOfSameType(curVal)
				continue
			}

			delta := computeDelta(prevVal, curVal)
			if delta == nil {
				// Counter wrap or incompatible types: output 0.
				newFields[fieldName] = zeroOfSameType(curVal)
				continue
			}

			if p.outputMode == "rate" {
				elapsed := m.Timestamp().Sub(prev.timestamp).Seconds()
				if elapsed <= 0 {
					newFields[fieldName] = float64(0)
				} else {
					newFields[fieldName] = *delta / elapsed
				}
			} else {
				// Delta mode: preserve int64 type when both are int64.
				if _, ok1 := prevVal.(int64); ok1 {
					if _, ok2 := curVal.(int64); ok2 {
						newFields[fieldName] = int64(*delta)
						continue
					}
				}
				newFields[fieldName] = *delta
			}
		}

		// Store current values for next iteration (AFTER computing result).
		storedValues := make(map[string]interface{}, len(fields))
		for k, v := range fields {
			storedValues[k] = v
		}
		p.prev[key] = entry{
			values:    storedValues,
			timestamp: m.Timestamp(),
		}

		updated := collector.NewMetric(m.Name(), m.Tags(), newFields, m.Type(), m.Timestamp())
		result = append(result, updated)
	}

	return result
}

// cleanupStale removes entries that haven't been updated within maxStaleSeconds.
func (p *Processor) cleanupStale(now time.Time) {
	threshold := now.Add(-time.Duration(p.maxStaleSeconds) * time.Second)
	for k, e := range p.prev {
		if e.timestamp.Before(threshold) {
			delete(p.prev, k)
		}
	}
}

// computeDelta computes the difference between two numeric values.
// Returns nil if the values are not numeric or if current < previous (counter wrap).
func computeDelta(prev, cur interface{}) *float64 {
	prevF, ok1 := toFloat64(prev)
	curF, ok2 := toFloat64(cur)
	if !ok1 || !ok2 {
		return nil
	}
	if curF < prevF {
		return nil // Counter wrap.
	}
	d := curF - prevF
	return &d
}

// toFloat64 converts a numeric value to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case int64:
		return float64(val), true
	case float64:
		return val, true
	case int:
		return float64(val), true
	case float32:
		return float64(val), true
	default:
		return 0, false
	}
}

// zeroOfSameType returns a zero value of the same type as the input.
func zeroOfSameType(v interface{}) interface{} {
	switch v.(type) {
	case int64:
		return int64(0)
	case float64:
		return float64(0)
	case int:
		return int(0)
	case float32:
		return float32(0)
	default:
		return float64(0)
	}
}

// SampleConfig returns a sample configuration string.
func (p *Processor) SampleConfig() string {
	return `
[[processors.delta]]
  fields = ["read_bytes", "write_bytes"]
  output = "rate"              # "delta" | "rate", default "rate"
  max_stale_seconds = 300      # entries older than this are cleaned up, default 300
`
}

func init() {
	collector.RegisterProcessor("delta", func() collector.Processor {
		return &Processor{}
	})
}
