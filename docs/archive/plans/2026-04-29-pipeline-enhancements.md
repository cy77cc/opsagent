# Pipeline Enhancements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Delta/Rate Processor, Min/Max Aggregator, and Percentile Aggregator to the collector pipeline.

**Architecture:** Three independent plugin components following existing processor/aggregator patterns. Each lives in its own package under `internal/collector/processors/` or `internal/collector/aggregators/`, self-registers via `init()`, and is triggered by blank imports in `agent.go`.

**Tech Stack:** Go, sync.Mutex for concurrency, `collector.Metric` / `collector.Accumulator` interfaces, `collector.RegisterProcessor` / `collector.RegisterAggregator` registry.

---

## File Structure

| File | Responsibility |
|------|---------------|
| `internal/collector/processors/delta/delta.go` | Delta/Rate processor: tracks previous metric values, outputs delta or rate |
| `internal/collector/processors/delta/delta_test.go` | Tests for delta processor |
| `internal/collector/aggregators/minmax/minmax.go` | Min/Max aggregator: tracks window min and max per field |
| `internal/collector/aggregators/minmax/minmax_test.go` | Tests for minmax aggregator |
| `internal/collector/aggregators/percentile/percentile.go` | Percentile aggregator: collects values, computes p50/p95/p99 on push |
| `internal/collector/aggregators/percentile/percentile_test.go` | Tests for percentile aggregator |
| `internal/app/agent.go` | Add blank imports for new plugins |
| `configs/config.yaml` | Add example processor/aggregator configs |

---

### Task 1: Delta/Rate Processor

**Files:**
- Create: `internal/collector/processors/delta/delta_test.go`
- Create: `internal/collector/processors/delta/delta.go`

- [ ] **Step 1: Write failing test — first collection outputs 0**

```go
// internal/collector/processors/delta/delta_test.go
package delta

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestFirstCollectionOutputsZero(t *testing.T) {
	p := New(Config{
		Fields: []string{"read_bytes"},
		Output: "delta",
	})

	m := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1000)},
		collector.Counter, time.Now())

	result := p.Apply([]*collector.Metric{m})
	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	fields := result[0].Fields()
	val, ok := fields["read_bytes"]
	if !ok {
		t.Fatal("expected read_bytes field")
	}
	if val.(int64) != 0 {
		t.Errorf("expected 0 on first collection, got %v", val)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestFirstCollectionOutputsZero`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/processors/delta/delta.go
package delta

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// Config holds the delta processor configuration.
type Config struct {
	Fields          []string `mapstructure:"fields"`
	Output          string   `mapstructure:"output"`
	MaxStaleSeconds int64    `mapstructure:"max_stale_seconds"`
}

type metricSnapshot struct {
	fields    map[string]interface{}
	timestamp time.Time
}

// Processor computes delta or rate from cumulative counter metrics.
type Processor struct {
	fields          []string
	output          string
	maxStaleSeconds int64
	previous        map[string]*metricSnapshot
	mu              sync.Mutex
}

// New creates a new delta Processor from the given config.
func New(cfg Config) *Processor {
	output := cfg.Output
	if output == "" {
		output = "rate"
	}
	stale := cfg.MaxStaleSeconds
	if stale == 0 {
		stale = 300
	}
	return &Processor{
		fields:          cfg.Fields,
		output:          output,
		maxStaleSeconds: stale,
		previous:        make(map[string]*metricSnapshot),
	}
}

// Init parses configuration from a map.
func (p *Processor) Init(cfg map[string]interface{}) error {
	if raw, ok := cfg["fields"]; ok {
		fieldList, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("delta: \"fields\" must be a list, got %T", raw)
		}
		p.fields = make([]string, 0, len(fieldList))
		for i, entry := range fieldList {
			name, ok := entry.(string)
			if !ok {
				return fmt.Errorf("delta: field entry %d must be a string, got %T", i, entry)
			}
			p.fields = append(p.fields, name)
		}
	}
	if raw, ok := cfg["output"]; ok {
		p.output, _ = raw.(string)
	}
	if p.output == "" {
		p.output = "rate"
	}
	if raw, ok := cfg["max_stale_seconds"]; ok {
		switch v := raw.(type) {
		case int64:
			p.maxStaleSeconds = v
		case int:
			p.maxStaleSeconds = int64(v)
		}
	}
	if p.maxStaleSeconds == 0 {
		p.maxStaleSeconds = 300
	}
	p.previous = make(map[string]*metricSnapshot)
	return nil
}

// metricKey computes a unique key from metric name and sorted tags.
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

// toFloat64 converts int64 or float64 to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

// Apply computes delta or rate for configured fields.
func (p *Processor) Apply(in []*collector.Metric) []*collector.Metric {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]*collector.Metric, 0, len(in))

	for _, m := range in {
		key := metricKey(m.Name(), m.Tags())
		fields := m.Fields()
		now := m.Timestamp()

		// Build output fields — start with pass-through.
		outFields := make(map[string]interface{}, len(fields))
		for fname, val := range fields {
			outFields[fname] = val
		}

		for _, fname := range p.fields {
			val, ok := fields[fname]
			if !ok {
				continue
			}

			prev, exists := p.previous[key]
			if !exists {
				// First collection — output 0.
				outFields[fname] = int64(0)
				continue
			}

			prevVal, prevOK := prev.fields[fname]
			if !prevOK {
				outFields[fname] = int64(0)
				continue
			}

			curFloat, curOK := toFloat64(val)
			prevFloat, prevOK2 := toFloat64(prevVal)
			if !curOK || !prevOK2 {
				continue
			}

			delta := curFloat - prevFloat
			if delta < 0 {
				// Counter wrap — output 0.
				delta = 0
			}

			if p.output == "rate" {
				elapsed := now.Sub(prev.timestamp).Seconds()
				if elapsed > 0 {
					outFields[fname] = delta / elapsed
				} else {
					outFields[fname] = float64(0)
				}
			} else {
				// delta mode — preserve int64 type when both are int64.
				if _, ok := val.(int64); ok {
					if _, ok := prevVal.(int64); ok {
						outFields[fname] = int64(delta)
					} else {
						outFields[fname] = delta
					}
				} else {
					outFields[fname] = delta
				}
			}
		}

		// Update snapshot.
		newSnapshot := &metricSnapshot{
			fields:    make(map[string]interface{}, len(fields)),
			timestamp: now,
		}
		for fname, val := range fields {
			newSnapshot.fields[fname] = val
		}
		p.previous[key] = newSnapshot

		out := collector.NewMetric(m.Name(), m.Tags(), outFields, m.Type(), m.Timestamp())
		result = append(result, out)
	}

	return result
}

// cleanupStale removes entries older than maxStaleSeconds.
func (p *Processor) cleanupStale(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, snap := range p.previous {
		if now.Sub(snap.timestamp).Seconds() > float64(p.maxStaleSeconds) {
			delete(p.previous, key)
		}
	}
}

func (p *Processor) SampleConfig() string {
	return `
fields = ["read_bytes", "write_bytes"]
output = "rate"
max_stale_seconds = 300
`
}

func init() {
	collector.RegisterProcessor("delta", func() collector.Processor {
		return &Processor{}
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestFirstCollectionOutputsZero`
Expected: PASS

- [ ] **Step 5: Write test — consecutive collections output correct delta**

```go
func TestConsecutiveCollectionsDelta(t *testing.T) {
	p := New(Config{
		Fields: []string{"read_bytes"},
		Output: "delta",
	})

	m1 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1000)},
		collector.Counter, time.Now())
	p.Apply([]*collector.Metric{m1})

	m2 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1500)},
		collector.Counter, time.Now())
	result := p.Apply([]*collector.Metric{m2})

	fields := result[0].Fields()
	val := fields["read_bytes"].(int64)
	if val != 500 {
		t.Errorf("expected delta 500, got %v", val)
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestConsecutiveCollectionsDelta`
Expected: PASS

- [ ] **Step 7: Write test — rate mode outputs delta/elapsed**

```go
func TestConsecutiveCollectionsRate(t *testing.T) {
	p := New(Config{
		Fields: []string{"read_bytes"},
		Output: "rate",
	})

	now := time.Now()
	m1 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1000)},
		collector.Counter, now)
	p.Apply([]*collector.Metric{m1})

	// Simulate 2 seconds later.
	m2 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1500)},
		collector.Counter, now.Add(2*time.Second))
	result := p.Apply([]*collector.Metric{m2})

	fields := result[0].Fields()
	val := fields["read_bytes"].(float64)
	if val != 250.0 {
		t.Errorf("expected rate 250.0, got %v", val)
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestConsecutiveCollectionsRate`
Expected: PASS

- [ ] **Step 9: Write test — counter wrap outputs 0**

```go
func TestCounterWrapOutputsZero(t *testing.T) {
	p := New(Config{
		Fields: []string{"read_bytes"},
		Output: "delta",
	})

	m1 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(1000)},
		collector.Counter, time.Now())
	p.Apply([]*collector.Metric{m1})

	// Counter wraps to a smaller value.
	m2 := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, time.Now())
	result := p.Apply([]*collector.Metric{m2})

	fields := result[0].Fields()
	val := fields["read_bytes"].(int64)
	if val != 0 {
		t.Errorf("expected 0 on counter wrap, got %v", val)
	}
}
```

- [ ] **Step 10: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestCounterWrapOutputsZero`
Expected: PASS

- [ ] **Step 11: Write test — mixed int64/float64 types**

```go
func TestMixedTypes(t *testing.T) {
	p := New(Config{
		Fields: []string{"bytes"},
		Output: "delta",
	})

	m1 := collector.NewMetric("net",
		map[string]string{"iface": "eth0"},
		map[string]interface{}{"bytes": int64(100)},
		collector.Counter, time.Now())
	p.Apply([]*collector.Metric{m1})

	m2 := collector.NewMetric("net",
		map[string]string{"iface": "eth0"},
		map[string]interface{}{"bytes": float64(250.5)},
		collector.Counter, time.Now())
	result := p.Apply([]*collector.Metric{m2})

	fields := result[0].Fields()
	val := fields["bytes"].(float64)
	if val != 150.5 {
		t.Errorf("expected 150.5, got %v", val)
	}
}
```

- [ ] **Step 12: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestMixedTypes`
Expected: PASS

- [ ] **Step 13: Write test — missing field is skipped**

```go
func TestMissingFieldSkipped(t *testing.T) {
	p := New(Config{
		Fields: []string{"read_bytes", "write_bytes"},
		Output: "delta",
	})

	m := collector.NewMetric("diskio",
		map[string]string{"device": "sda"},
		map[string]interface{}{"read_bytes": int64(100)},
		collector.Counter, time.Now())

	result := p.Apply([]*collector.Metric{m})
	fields := result[0].Fields()

	// read_bytes should be 0 (first collection).
	if fields["read_bytes"].(int64) != 0 {
		t.Errorf("expected read_bytes=0, got %v", fields["read_bytes"])
	}
	// write_bytes not present — should not be in output.
	if _, ok := fields["write_bytes"]; ok {
		t.Error("expected write_bytes to be absent")
	}
}
```

- [ ] **Step 14: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestMissingFieldSkipped`
Expected: PASS

- [ ] **Step 15: Write test — stale entry cleanup**

```go
func TestStaleEntryCleanup(t *testing.T) {
	p := New(Config{
		Fields:          []string{"val"},
		Output:          "delta",
		MaxStaleSeconds: 1, // 1 second for testing
	})

	m1 := collector.NewMetric("test",
		map[string]string{"k": "v"},
		map[string]interface{}{"val": int64(10)},
		collector.Counter, time.Now())
	p.Apply([]*collector.Metric{m1})

	// Verify entry exists.
	p.mu.Lock()
	if len(p.previous) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(p.previous))
	}
	p.mu.Unlock()

	// Trigger cleanup.
	p.cleanupStale(time.Now().Add(2 * time.Second))

	p.mu.Lock()
	if len(p.previous) != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", len(p.previous))
	}
	p.mu.Unlock()
}
```

Add the `cleanupStale` method to `delta.go`:

```go
// cleanupStale removes entries older than maxStaleSeconds.
func (p *Processor) cleanupStale(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, snap := range p.previous {
		if now.Sub(snap.timestamp).Seconds() > float64(p.maxStaleSeconds) {
			delete(p.previous, key)
		}
	}
}
```

- [ ] **Step 16: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/processors/delta/ -v -run TestStaleEntryCleanup`
Expected: PASS

- [ ] **Step 17: Write test — concurrent safety**

```go
func TestConcurrentSafety(t *testing.T) {
	p := New(Config{
		Fields: []string{"val"},
		Output: "delta",
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m := collector.NewMetric("test",
				map[string]string{"goroutine": "g"},
				map[string]interface{}{"val": int64(n * 100)},
				collector.Counter, time.Now())
			p.Apply([]*collector.Metric{m})
		}(i)
	}
	wg.Wait()
	// No panic = pass.
}
```

- [ ] **Step 18: Run full test suite**

Run: `cd /root/project/opsagent && go test -race ./internal/collector/processors/delta/ -v`
Expected: All PASS with no data races

- [ ] **Step 19: Commit**

```bash
cd /root/project/opsagent && git add internal/collector/processors/delta/ && git commit -m "feat(collector): add delta/rate processor"
```

---

### Task 2: Min/Max Aggregator

**Files:**
- Create: `internal/collector/aggregators/minmax/minmax_test.go`
- Create: `internal/collector/aggregators/minmax/minmax.go`

- [ ] **Step 1: Write failing test — single value window**

```go
// internal/collector/aggregators/minmax/minmax_test.go
package minmax

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestSingleValueMinMax(t *testing.T) {
	a := New(Config{Fields: []string{"cpu"}})

	m := collector.NewMetric("host",
		map[string]string{"host": "s1"},
		map[string]interface{}{"cpu": float64(42)},
		collector.Gauge, time.Now())
	a.Add(m)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 2 {
		t.Fatalf("expected 2 metrics (min+max), got %d", len(collected))
	}

	names := map[string]float64{}
	for _, c := range collected {
		fields := c.Fields()
		for _, v := range fields {
			names[c.Name()] = v.(float64)
		}
	}
	if names["host_min"] != 42.0 {
		t.Errorf("expected host_min=42, got %v", names["host_min"])
	}
	if names["host_max"] != 42.0 {
		t.Errorf("expected host_max=42, got %v", names["host_max"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/project/opsagent && go test ./internal/collector/aggregators/minmax/ -v -run TestSingleValueMinMax`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write implementation**

```go
// internal/collector/aggregators/minmax/minmax.go
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

// Aggregator tracks min and max values per field over a window.
type Aggregator struct {
	mu     sync.Mutex
	fields []string
	min    map[string]float64
	max    map[string]float64
	tags   map[string]string
	name   string
}

// New creates a new minmax Aggregator from the given config.
func New(cfg Config) *Aggregator {
	return &Aggregator{
		fields: cfg.Fields,
		min:    make(map[string]float64),
		max:    make(map[string]float64),
		tags:   make(map[string]string),
	}
}

// Init parses configuration from a map.
func (a *Aggregator) Init(cfg map[string]interface{}) error {
	a.min = make(map[string]float64)
	a.max = make(map[string]float64)
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

// Add accumulates min/max from the given metric.
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

		if cur, exists := a.min[fname]; !exists || fv < cur {
			a.min[fname] = fv
		}
		if cur, exists := a.max[fname]; !exists || fv > cur {
			a.max[fname] = fv
		}
	}

	// Capture name and tags from first matching metric.
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

// Push emits min and max values to the accumulator.
func (a *Aggregator) Push(acc collector.Accumulator) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.min) == 0 {
		return
	}

	minFields := make(map[string]interface{})
	maxFields := make(map[string]interface{})
	for _, fname := range a.fields {
		if v, ok := a.min[fname]; ok {
			minFields[fname] = v
		}
		if v, ok := a.max[fname]; ok {
			maxFields[fname] = v
		}
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

	a.min = make(map[string]float64)
	a.max = make(map[string]float64)
	a.tags = make(map[string]string)
	a.name = ""
}

// SampleConfig returns a sample configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["cpu_usage_percent", "memory_used_percent"]
`
}

func init() {
	collector.RegisterAggregator("minmax", func() collector.Aggregator {
		return &Aggregator{}
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/aggregators/minmax/ -v -run TestSingleValueMinMax`
Expected: PASS

- [ ] **Step 5: Write test — multi-value window identifies extremes**

```go
func TestMultiValueMinMax(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	for _, v := range []float64{50, 10, 90, 30, 70} {
		m := collector.NewMetric("metric",
			map[string]string{"k": "v"},
			map[string]interface{}{"val": v},
			collector.Gauge, time.Now())
		a.Add(m)
	}

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	names := map[string]float64{}
	for _, c := range collected {
		fields := c.Fields()
		for _, v := range fields {
			names[c.Name()] = v.(float64)
		}
	}
	if names["metric_min"] != 10.0 {
		t.Errorf("expected min=10, got %v", names["metric_min"])
	}
	if names["metric_max"] != 90.0 {
		t.Errorf("expected max=90, got %v", names["metric_max"])
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/aggregators/minmax/ -v -run TestMultiValueMinMax`
Expected: PASS

- [ ] **Step 7: Write test — reset clears state**

```go
func TestReset(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	m := collector.NewMetric("m",
		map[string]string{"k": "v"},
		map[string]interface{}{"val": float64(42)},
		collector.Gauge, time.Now())
	a.Add(m)
	a.Reset()

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics after reset, got %d", len(collected))
	}
}
```

- [ ] **Step 8: Write test — empty aggregator push emits nothing**

```go
func TestPushEmpty(t *testing.T) {
	a := New(Config{Fields: []string{"val"}})

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics from empty aggregator, got %d", len(collected))
	}
}
```

- [ ] **Step 9: Write test — registry registration**

```go
func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetAggregator("minmax")
	if !ok {
		t.Fatal("minmax aggregator not registered in default registry")
	}
	a := f()
	if a == nil {
		t.Fatal("expected non-nil aggregator from factory")
	}
}
```

- [ ] **Step 10: Run full test suite**

Run: `cd /root/project/opsagent && go test -race ./internal/collector/aggregators/minmax/ -v`
Expected: All PASS

- [ ] **Step 11: Commit**

```bash
cd /root/project/opsagent && git add internal/collector/aggregators/minmax/ && git commit -m "feat(collector): add min/max aggregator"
```

---

### Task 3: Percentile Aggregator

**Files:**
- Create: `internal/collector/aggregators/percentile/percentile_test.go`
- Create: `internal/collector/aggregators/percentile/percentile.go`

- [ ] **Step 1: Write failing test — known distribution p50/p95/p99**

```go
// internal/collector/aggregators/percentile/percentile_test.go
package percentile

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestKnownDistribution(t *testing.T) {
	a := New(Config{
		Fields:      []string{"latency"},
		Percentiles: []float64{50, 95, 99},
	})

	// 100 values: 1, 2, 3, ..., 100
	for i := 1; i <= 100; i++ {
		m := collector.NewMetric("req",
			map[string]string{"svc": "api"},
			map[string]interface{}{"latency": float64(i)},
			collector.Gauge, time.Now())
		a.Add(m)
	}

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(collected))
	}

	fields := collected[0].Fields()

	p50 := fields["latency_p50"].(float64)
	if p50 < 49.5 || p50 > 50.5 {
		t.Errorf("expected p50 ~50, got %v", p50)
	}

	p95 := fields["latency_p95"].(float64)
	if p95 < 94.5 || p95 > 95.5 {
		t.Errorf("expected p95 ~95, got %v", p95)
	}

	p99 := fields["latency_p99"].(float64)
	if p99 < 98.5 || p99 > 99.5 {
		t.Errorf("expected p99 ~99, got %v", p99)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/project/opsagent && go test ./internal/collector/aggregators/percentile/ -v -run TestKnownDistribution`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write implementation**

```go
// internal/collector/aggregators/percentile/percentile.go
package percentile

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// Config holds the percentile aggregator configuration.
type Config struct {
	Fields      []string  `mapstructure:"fields"`
	Percentiles []float64 `mapstructure:"percentiles"`
}

// Aggregator collects values and computes percentiles on push.
type Aggregator struct {
	mu          sync.Mutex
	fields      []string
	percentiles []float64
	values      map[string][]float64
	tags        map[string]string
	name        string
}

// New creates a new percentile Aggregator from the given config.
func New(cfg Config) *Aggregator {
	pcts := cfg.Percentiles
	if len(pcts) == 0 {
		pcts = []float64{50, 95, 99}
	}
	return &Aggregator{
		fields:      cfg.Fields,
		percentiles: pcts,
		values:      make(map[string][]float64),
		tags:        make(map[string]string),
	}
}

// Init parses configuration from a map.
func (a *Aggregator) Init(cfg map[string]interface{}) error {
	a.values = make(map[string][]float64)
	a.tags = make(map[string]string)

	if raw, ok := cfg["fields"]; ok {
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
	}

	if raw, ok := cfg["percentiles"]; ok {
		pctList, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("percentile: \"percentiles\" must be a list, got %T", raw)
		}
		a.percentiles = make([]float64, 0, len(pctList))
		for i, entry := range pctList {
			switch v := entry.(type) {
			case float64:
				a.percentiles = append(a.percentiles, v)
			case int:
				a.percentiles = append(a.percentiles, float64(v))
			case int64:
				a.percentiles = append(a.percentiles, float64(v))
			default:
				return fmt.Errorf("percentile: percentile entry %d must be a number, got %T", i, entry)
			}
		}
	}

	if len(a.percentiles) == 0 {
		a.percentiles = []float64{50, 95, 99}
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

	// Capture name and tags from first matching metric.
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

// Push computes percentiles and emits them to the accumulator.
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

// percentile computes the p-th percentile from a sorted slice using linear interpolation.
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

// SampleConfig returns a sample configuration.
func (a *Aggregator) SampleConfig() string {
	return `
fields = ["response_time_ms", "latency_ms"]
percentiles = [50, 95, 99]
`
}

func init() {
	collector.RegisterAggregator("percentile", func() collector.Aggregator {
		return &Aggregator{}
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/project/opsagent && go test ./internal/collector/aggregators/percentile/ -v -run TestKnownDistribution`
Expected: PASS

- [ ] **Step 5: Write test — empty window outputs nothing**

```go
func TestEmptyWindow(t *testing.T) {
	a := New(Config{
		Fields:      []string{"val"},
		Percentiles: []float64{50},
	})

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	if len(collected) != 0 {
		t.Errorf("expected 0 metrics from empty window, got %d", len(collected))
	}
}
```

- [ ] **Step 6: Write test — single value all percentiles equal**

```go
func TestSingleValue(t *testing.T) {
	a := New(Config{
		Fields:      []string{"val"},
		Percentiles: []float64{50, 95, 99},
	})

	m := collector.NewMetric("m",
		map[string]string{"k": "v"},
		map[string]interface{}{"val": float64(42)},
		collector.Gauge, time.Now())
	a.Add(m)

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	fields := collected[0].Fields()
	for _, key := range []string{"val_p50", "val_p95", "val_p99"} {
		v := fields[key].(float64)
		if v != 42.0 {
			t.Errorf("expected %s=42, got %v", key, v)
		}
	}
}
```

- [ ] **Step 7: Write test — large dataset performance and correctness**

```go
func TestLargeDataset(t *testing.T) {
	a := New(Config{
		Fields:      []string{"val"},
		Percentiles: []float64{50, 95, 99},
	})

	for i := 0; i < 10000; i++ {
		m := collector.NewMetric("m",
			map[string]string{"k": "v"},
			map[string]interface{}{"val": float64(i)},
			collector.Gauge, time.Now())
		a.Add(m)
	}

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	fields := collected[0].Fields()

	p50 := fields["val_p50"].(float64)
	if p50 < 4990 || p50 > 5010 {
		t.Errorf("expected p50 ~5000, got %v", p50)
	}
	p99 := fields["val_p99"].(float64)
	if p99 < 9890 || p99 > 9910 {
		t.Errorf("expected p99 ~9900, got %v", p99)
	}
}
```

- [ ] **Step 8: Write test — custom percentiles**

```go
func TestCustomPercentiles(t *testing.T) {
	a := New(Config{
		Fields:      []string{"val"},
		Percentiles: []float64{25, 75},
	})

	for i := 1; i <= 100; i++ {
		m := collector.NewMetric("m",
			map[string]string{"k": "v"},
			map[string]interface{}{"val": float64(i)},
			collector.Gauge, time.Now())
		a.Add(m)
	}

	acc := collector.NewAccumulator(10)
	a.Push(acc)

	collected := acc.Collect()
	fields := collected[0].Fields()

	if _, ok := fields["val_p50"]; ok {
		t.Error("expected no p50 field with custom percentiles")
	}

	p25 := fields["val_p25"].(float64)
	if p25 < 24 || p25 > 26 {
		t.Errorf("expected p25 ~25, got %v", p25)
	}
}
```

- [ ] **Step 9: Write test — registry registration**

```go
func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetAggregator("percentile")
	if !ok {
		t.Fatal("percentile aggregator not registered in default registry")
	}
	a := f()
	if a == nil {
		t.Fatal("expected non-nil aggregator from factory")
	}
}
```

- [ ] **Step 10: Run full test suite**

Run: `cd /root/project/opsagent && go test -race ./internal/collector/aggregators/percentile/ -v`
Expected: All PASS

- [ ] **Step 11: Commit**

```bash
cd /root/project/opsagent && git add internal/collector/aggregators/percentile/ && git commit -m "feat(collector): add percentile aggregator"
```

---

### Task 4: Registration and Integration

**Files:**
- Modify: `internal/app/agent.go` (add 3 blank imports)
- Modify: `configs/config.yaml` (add example processor/aggregator configs)

- [ ] **Step 1: Add blank imports to agent.go**

Add after line 30 (after the `sum` import):

```go
_ "github.com/cy77cc/opsagent/internal/collector/aggregators/minmax"
_ "github.com/cy77cc/opsagent/internal/collector/aggregators/percentile"
```

Add after line 45 (after the `tagger` import):

```go
_ "github.com/cy77cc/opsagent/internal/collector/processors/delta"
```

- [ ] **Step 2: Add example configs to config.yaml**

Replace `processors: []` and `aggregators: []` with:

```yaml
  processors: []
    # Example: delta/rate processor for disk IO counters
    # - type: delta
    #   config:
    #     fields: ["read_bytes", "write_bytes"]
    #     output: "rate"
    #     max_stale_seconds: 300
  aggregators: []
    # Example: min/max aggregator for CPU usage
    # - type: minmax
    #   config:
    #     fields: ["cpu_usage_percent"]
    # Example: percentile aggregator for latency
    # - type: percentile
    #   config:
    #     fields: ["response_time_ms"]
    #     percentiles: [50, 95, 99]
```

- [ ] **Step 3: Verify build compiles**

Run: `cd /root/project/opsagent && go build ./...`
Expected: No errors

- [ ] **Step 4: Run all tests**

Run: `cd /root/project/opsagent && go test -race ./internal/collector/processors/delta/ ./internal/collector/aggregators/minmax/ ./internal/collector/aggregators/percentile/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
cd /root/project/opsagent && git add internal/app/agent.go configs/config.yaml && git commit -m "feat(app): register new pipeline plugins and add example configs"
```
