# Sub-Plan 5: Processor & Aggregator Plugins

> **Parent:** [OpsAgent Full Implementation Plan](../2026-04-28-opsagent-full-implementation.md)
> **Depends on:** [Sub-Plan 2: Collector Pipeline Core](02-collector-pipeline.md)

**Goal:** Implement built-in Processor (regex, tagger) and Aggregator (avg, sum) plugins.

**Files:**
- Create: `internal/collector/processors/regex/regex.go`, `regex_test.go`
- Create: `internal/collector/processors/tagger/tagger.go`, `tagger_test.go`
- Create: `internal/collector/aggregators/avg/avg.go`, `avg_test.go`
- Create: `internal/collector/aggregators/sum/sum.go`, `sum_test.go`

---

## Task 5.1: Regex Processor

- [ ] **Step 1: Write failing test**

Create `internal/collector/processors/regex/regex_test.go`:

```go
package regex

import (
	"testing"
	"time"

	"opsagent/internal/collector"
)

func TestRegexReplaceTag(t *testing.T) {
	proc := &RegexProcessor{}
	proc.Init(map[string]interface{}{
		"tags": []interface{}{
			map[string]interface{}{
				"key":         "host",
				"pattern":     `^ip-(\d+)-(\d+)-(\d+)-(\d+)$`,
				"replacement": "node-${1}${2}${3}${4}",
			},
		},
	})

	metrics := []*collector.Metric{
		collector.NewMetric("test", map[string]string{"host": "ip-10-0-0-1"}, map[string]interface{}{"v": 1}, collector.Gauge, time.Now()),
	}

	result := proc.Apply(metrics)

	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}
	if result[0].Tags()["host"] != "node-10001" {
		t.Fatalf("expected host=node-10001, got %s", result[0].Tags()["host"])
	}
}

func TestRegexNoMatch(t *testing.T) {
	proc := &RegexProcessor{}
	proc.Init(map[string]interface{}{
		"tags": []interface{}{
			map[string]interface{}{
				"key":         "host",
				"pattern":     `^ip-(\d+)$`,
				"replacement": "node-${1}",
			},
		},
	})

	metrics := []*collector.Metric{
		collector.NewMetric("test", map[string]string{"host": "webserver-01"}, map[string]interface{}{"v": 1}, collector.Gauge, time.Now()),
	}

	result := proc.Apply(metrics)

	if result[0].Tags()["host"] != "webserver-01" {
		t.Fatalf("expected unchanged host, got %s", result[0].Tags()["host"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/processors/regex/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Regex processor**

Create `internal/collector/processors/regex/regex.go`:

```go
package regex

import (
	"regexp"

	"opsagent/internal/collector"
)

type tagRule struct {
	key         string
	re          *regexp.Regexp
	replacement string
}

// RegexProcessor applies regex-based transformations to metric tags.
type RegexProcessor struct {
	rules []tagRule
}

func (r *RegexProcessor) Init(cfg map[string]interface{}) error {
	if tags, ok := cfg["tags"].([]interface{}); ok {
		for _, t := range tags {
			tagCfg, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			key, _ := tagCfg["key"].(string)
			pattern, _ := tagCfg["pattern"].(string)
			replacement, _ := tagCfg["replacement"].(string)
			if key == "" || pattern == "" {
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return err
			}
			r.rules = append(r.rules, tagRule{key: key, re: re, replacement: replacement})
		}
	}
	return nil
}

func (r *RegexProcessor) Apply(in []*collector.Metric) []*collector.Metric {
	for _, m := range in {
		for _, rule := range r.rules {
			if val, ok := m.Tags()[rule.key]; ok {
				newVal := rule.re.ReplaceAllString(val, rule.replacement)
				if newVal != val {
					m.AddTag(rule.key, newVal)
				}
			}
		}
	}
	return in
}

func (r *RegexProcessor) SampleConfig() string {
	return `# [[processors.regex.tags]]
#   key = "host"
#   pattern = "^ip-(\\d+)-(\\d+)-(\\d+)-(\\d+)$"
#   replacement = "node-\${1}\${2}\${3}\${4}"`
}

func init() {
	collector.DefaultRegistry.RegisterProcessor("regex", func() collector.Processor { return &RegexProcessor{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/processors/regex/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/processors/regex/
git commit -m "feat(collector): add Regex processor plugin for tag transformation"
```

---

## Task 5.2: Tagger Processor

- [ ] **Step 1: Write failing test**

Create `internal/collector/processors/tagger/tagger_test.go`:

```go
package tagger

import (
	"testing"
	"time"

	"opsagent/internal/collector"
)

func TestTaggerAddStaticTag(t *testing.T) {
	proc := &TaggerProcessor{}
	proc.Init(map[string]interface{}{
		"tags": map[string]interface{}{
			"env":  "production",
			"team": "platform",
		},
	})

	metrics := []*collector.Metric{
		collector.NewMetric("test", nil, map[string]interface{}{"v": 1}, collector.Gauge, time.Now()),
	}

	result := proc.Apply(metrics)

	if result[0].Tags()["env"] != "production" {
		t.Fatalf("expected env=production, got %s", result[0].Tags()["env"])
	}
	if result[0].Tags()["team"] != "platform" {
		t.Fatalf("expected team=platform, got %s", result[0].Tags()["team"])
	}
}

func TestTaggerConditionalAdd(t *testing.T) {
	proc := &TaggerProcessor{}
	proc.Init(map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"tag":      "level",
				"value":    "critical",
				"when_name": "error_count",
			},
		},
	})

	metrics := []*collector.Metric{
		collector.NewMetric("error_count", nil, map[string]interface{}{"v": 1}, collector.Counter, time.Now()),
		collector.NewMetric("request_count", nil, map[string]interface{}{"v": 1}, collector.Counter, time.Now()),
	}

	result := proc.Apply(metrics)

	if result[0].Tags()["level"] != "critical" {
		t.Fatal("expected level=critical on error_count")
	}
	if _, ok := result[1].Tags()["level"]; ok {
		t.Fatal("expected no level tag on request_count")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/processors/tagger/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Tagger processor**

Create `internal/collector/processors/tagger/tagger.go`:

```go
package tagger

import "opsagent/internal/collector"

type condition struct {
	tag      string
	value    string
	whenName string
}

// TaggerProcessor adds static or conditional tags to metrics.
type TaggerProcessor struct {
	staticTags map[string]string
	conditions []condition
}

func (t *TaggerProcessor) Init(cfg map[string]interface{}) error {
	t.staticTags = make(map[string]string)

	if tags, ok := cfg["tags"].(map[string]interface{}); ok {
		for k, v := range tags {
			if s, ok := v.(string); ok {
				t.staticTags[k] = s
			}
		}
	}

	if conds, ok := cfg["conditions"].([]interface{}); ok {
		for _, c := range conds {
			condCfg, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			tag, _ := condCfg["tag"].(string)
			value, _ := condCfg["value"].(string)
			whenName, _ := condCfg["when_name"].(string)
			if tag != "" && whenName != "" {
				t.conditions = append(t.conditions, condition{tag: tag, value: value, whenName: whenName})
			}
		}
	}

	return nil
}

func (t *TaggerProcessor) Apply(in []*collector.Metric) []*collector.Metric {
	for _, m := range in {
		for k, v := range t.staticTags {
			m.AddTag(k, v)
		}
		for _, cond := range t.conditions {
			if m.Name() == cond.whenName {
				m.AddTag(cond.tag, cond.value)
			}
		}
	}
	return in
}

func (t *TaggerProcessor) SampleConfig() string {
	return `# [processors.tagger.tags]
#   env = "production"
#   team = "platform"
# [[processors.tagger.conditions]]
#   tag = "level"
#   value = "critical"
#   when_name = "error_count"`
}

func init() {
	collector.DefaultRegistry.RegisterProcessor("tagger", func() collector.Processor { return &TaggerProcessor{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/processors/tagger/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/processors/tagger/
git commit -m "feat(collector): add Tagger processor plugin for static/conditional tags"
```

---

## Task 5.3: Average Aggregator

- [ ] **Step 1: Write failing test**

Create `internal/collector/aggregators/avg/avg_test.go`:

```go
package avg

import (
	"testing"
	"time"

	"opsagent/internal/collector"
)

func TestAvgAggregator(t *testing.T) {
	agg := &AvgAggregator{}
	agg.Init(map[string]interface{}{
		"fields": []interface{}{"value"},
	})

	agg.Add(collector.NewMetric("cpu", nil, map[string]interface{}{"value": 80.0}, collector.Gauge, time.Now()))
	agg.Add(collector.NewMetric("cpu", nil, map[string]interface{}{"value": 90.0}, collector.Gauge, time.Now()))
	agg.Add(collector.NewMetric("cpu", nil, map[string]interface{}{"value": 70.0}, collector.Gauge, time.Now()))

	acc := collector.NewAccumulator(100)
	agg.Push(acc)

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 aggregated metric, got %d", len(metrics))
	}

	avg := metrics[0].Fields()["value"].(float64)
	if avg < 79.9 || avg > 80.1 {
		t.Fatalf("expected avg ~80.0, got %f", avg)
	}
}

func TestAvgAggregatorReset(t *testing.T) {
	agg := &AvgAggregator{}
	agg.Init(map[string]interface{}{
		"fields": []interface{}{"value"},
	})

	agg.Add(collector.NewMetric("cpu", nil, map[string]interface{}{"value": 100.0}, collector.Gauge, time.Now()))

	agg.Reset()

	acc := collector.NewAccumulator(100)
	agg.Push(acc)

	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Fatalf("expected 0 metrics after reset, got %d", len(metrics))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/aggregators/avg/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Average aggregator**

Create `internal/collector/aggregators/avg/avg.go`:

```go
package avg

import (
	"time"

	"opsagent/internal/collector"
)

// AvgAggregator computes the average of specified fields over a time window.
type AvgAggregator struct {
	fields []string
	sums   map[string]float64
	counts map[string]int
	name   string
	tags   map[string]string
}

func (a *AvgAggregator) Init(cfg map[string]interface{}) error {
	a.sums = make(map[string]float64)
	a.counts = make(map[string]int)

	if fields, ok := cfg["fields"].([]interface{}); ok {
		for _, f := range fields {
			if s, ok := f.(string); ok {
				a.fields = append(a.fields, s)
			}
		}
	}
	return nil
}

func (a *AvgAggregator) Add(in *collector.Metric) {
	if a.name == "" {
		a.name = in.Name()
		a.tags = in.Tags()
	}

	for _, field := range a.fields {
		if v, ok := in.Fields()[field]; ok {
			switch val := v.(type) {
			case float64:
				a.sums[field] += val
				a.counts[field]++
			case int64:
				a.sums[field] += float64(val)
				a.counts[field]++
			}
		}
	}
}

func (a *AvgAggregator) Push(acc collector.Accumulator) {
	if len(a.sums) == 0 {
		return
	}

	fields := make(map[string]interface{}, len(a.sums))
	for k, sum := range a.sums {
		if count := a.counts[k]; count > 0 {
			fields[k] = sum / float64(count)
		}
	}

	acc.AddGauge(a.name+"_avg", a.tags, fields, time.Now())
	a.Reset()
}

func (a *AvgAggregator) Reset() {
	a.sums = make(map[string]float64)
	a.counts = make(map[string]int)
	a.name = ""
	a.tags = nil
}

func (a *AvgAggregator) SampleConfig() string {
	return `# fields = ["value"]`
}

func init() {
	collector.DefaultRegistry.RegisterAggregator("avg", func() collector.Aggregator { return &AvgAggregator{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/aggregators/avg/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/aggregators/avg/
git commit -m "feat(collector): add Average aggregator plugin"
```

---

## Task 5.4: Sum Aggregator

- [ ] **Step 1: Write failing test**

Create `internal/collector/aggregators/sum/sum_test.go`:

```go
package sum

import (
	"testing"
	"time"

	"opsagent/internal/collector"
)

func TestSumAggregator(t *testing.T) {
	agg := &SumAggregator{}
	agg.Init(map[string]interface{}{
		"fields": []interface{}{"count"},
	})

	agg.Add(collector.NewMetric("requests", nil, map[string]interface{}{"count": int64(10)}, collector.Counter, time.Now()))
	agg.Add(collector.NewMetric("requests", nil, map[string]interface{}{"count": int64(20)}, collector.Counter, time.Now()))
	agg.Add(collector.NewMetric("requests", nil, map[string]interface{}{"count": int64(30)}, collector.Counter, time.Now()))

	acc := collector.NewAccumulator(100)
	agg.Push(acc)

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 aggregated metric, got %d", len(metrics))
	}

	sum := metrics[0].Fields()["count"].(int64)
	if sum != 60 {
		t.Fatalf("expected sum=60, got %d", sum)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/aggregators/sum/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Sum aggregator**

Create `internal/collector/aggregators/sum/sum.go`:

```go
package sum

import (
	"time"

	"opsagent/internal/collector"
)

// SumAggregator computes the sum of specified fields over a time window.
type SumAggregator struct {
	fields []string
	sums   map[string]float64
	isInt  map[string]bool
	name   string
	tags   map[string]string
}

func (s *SumAggregator) Init(cfg map[string]interface{}) error {
	s.sums = make(map[string]float64)
	s.isInt = make(map[string]bool)

	if fields, ok := cfg["fields"].([]interface{}); ok {
		for _, f := range fields {
			if str, ok := f.(string); ok {
				s.fields = append(s.fields, str)
			}
		}
	}
	return nil
}

func (s *SumAggregator) Add(in *collector.Metric) {
	if s.name == "" {
		s.name = in.Name()
		s.tags = in.Tags()
	}

	for _, field := range s.fields {
		if v, ok := in.Fields()[field]; ok {
			switch val := v.(type) {
			case float64:
				s.sums[field] += val
			case int64:
				s.sums[field] += float64(val)
				s.isInt[field] = true
			}
		}
	}
}

func (s *SumAggregator) Push(acc collector.Accumulator) {
	if len(s.sums) == 0 {
		return
	}

	fields := make(map[string]interface{}, len(s.sums))
	for k, sum := range s.sums {
		if s.isInt[k] {
			fields[k] = int64(sum)
		} else {
			fields[k] = sum
		}
	}

	acc.AddCounter(s.name+"_sum", s.tags, fields, time.Now())
	s.Reset()
}

func (s *SumAggregator) Reset() {
	s.sums = make(map[string]float64)
	s.isInt = make(map[string]bool)
	s.name = ""
	s.tags = nil
}

func (s *SumAggregator) SampleConfig() string {
	return `# fields = ["count"]`
}

func init() {
	collector.DefaultRegistry.RegisterAggregator("sum", func() collector.Aggregator { return &SumAggregator{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/aggregators/sum/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/aggregators/sum/
git commit -m "feat(collector): add Sum aggregator plugin"
```
