package tagger

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestNew(t *testing.T) {
	cfg := Config{
		Tags: map[string]string{"env": "prod"},
		Conditions: []Condition{
			{Tag: "alert", Value: "true", WhenName: "disk"},
		},
	}
	p := New(cfg)
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
}

func TestApplyStaticTags(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{
			"env":    "production",
			"region": "us-east-1",
		},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	tags := result[0].Tags()
	if tags["env"] != "production" {
		t.Errorf("expected env=production, got %q", tags["env"])
	}
	if tags["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1, got %q", tags["region"])
	}
	if tags["host"] != "server-01" {
		t.Errorf("expected host=server-01, got %q", tags["host"])
	}
}

func TestApplyConditionalTags(t *testing.T) {
	p := New(Config{
		Conditions: []Condition{
			{Tag: "critical", Value: "true", WhenName: "disk_usage"},
		},
	})

	// Matching metric name - condition should apply.
	m1 := collector.NewMetric("disk_usage",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(90)},
		collector.Gauge, time.Now())

	// Non-matching metric name - condition should not apply.
	m2 := collector.NewMetric("cpu_usage",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(50)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m1, m2})

	tags1 := result[0].Tags()
	if tags1["critical"] != "true" {
		t.Errorf("expected critical=true on disk_usage, got %q", tags1["critical"])
	}

	tags2 := result[1].Tags()
	if _, ok := tags2["critical"]; ok {
		t.Error("expected no critical tag on cpu_usage")
	}
}

func TestApplyStaticAndConditional(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{"env": "staging"},
		Conditions: []Condition{
			{Tag: "alert", Value: "high", WhenName: "disk"},
		},
	})

	m := collector.NewMetric("disk",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(95)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	if tags["env"] != "staging" {
		t.Errorf("expected env=staging, got %q", tags["env"])
	}
	if tags["alert"] != "high" {
		t.Errorf("expected alert=high, got %q", tags["alert"])
	}
}

func TestApplyMultipleConditions(t *testing.T) {
	p := New(Config{
		Conditions: []Condition{
			{Tag: "tier", Value: "frontend", WhenName: "http_requests"},
			{Tag: "tier", Value: "backend", WhenName: "db_queries"},
		},
	})

	m1 := collector.NewMetric("http_requests",
		map[string]string{},
		map[string]interface{}{"count": int64(100)},
		collector.Counter, time.Now())

	m2 := collector.NewMetric("db_queries",
		map[string]string{},
		map[string]interface{}{"count": int64(50)},
		collector.Counter, time.Now())

	m3 := collector.NewMetric("cpu",
		map[string]string{},
		map[string]interface{}{"value": float64(50)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m1, m2, m3})

	if result[0].Tags()["tier"] != "frontend" {
		t.Errorf("expected tier=frontend for http_requests, got %q", result[0].Tags()["tier"])
	}
	if result[1].Tags()["tier"] != "backend" {
		t.Errorf("expected tier=backend for db_queries, got %q", result[1].Tags()["tier"])
	}
	if _, ok := result[2].Tags()["tier"]; ok {
		t.Error("expected no tier tag on cpu metric")
	}
}

func TestApplyEmptyConfig(t *testing.T) {
	p := New(Config{})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}
	tags := result[0].Tags()
	if tags["host"] != "server" {
		t.Errorf("expected host=server, got %q", tags["host"])
	}
}

func TestSampleConfig(t *testing.T) {
	p := New(Config{})
	cfg := p.SampleConfig()
	if cfg == "" {
		t.Error("expected non-empty sample config")
	}
}

func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetProcessor("tagger")
	if !ok {
		t.Fatal("tagger processor not registered in default registry")
	}
	p := f()
	if p == nil {
		t.Fatal("expected non-nil processor from factory")
	}
}

func TestApplyNilMetricInSlice(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{"env": "prod"},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	// A nil metric in the slice causes a panic because Apply calls m.AddTag
	// without a nil check. This test documents that edge case.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when slice contains nil metric, but did not panic")
		}
	}()

	p.Apply([]*collector.Metric{nil, m})
}

func TestApplyEmptyTagsMap(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{"env": "production"},
	})

	// Metric with no existing tags (empty map).
	m := collector.NewMetric("cpu",
		map[string]string{},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	tags := result[0].Tags()
	if tags["env"] != "production" {
		t.Errorf("expected env=production, got %q", tags["env"])
	}
	// Verify no unexpected tags were added.
	if len(tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(tags))
	}
}

func TestApplyTagOverride(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{
			"env":  "production",
			"host": "new-host",
		},
	})

	// Metric already has "env" and "host" tags — static tags should override them.
	m := collector.NewMetric("cpu",
		map[string]string{"env": "development", "host": "old-host"},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	if tags["env"] != "production" {
		t.Errorf("expected env=production after override, got %q", tags["env"])
	}
	if tags["host"] != "new-host" {
		t.Errorf("expected host=new-host after override, got %q", tags["host"])
	}
}

func TestApplyConditionWhenNameExactMatch(t *testing.T) {
	p := New(Config{
		Conditions: []Condition{
			{Tag: "severity", Value: "critical", WhenName: "disk"},
		},
	})

	// Metric name exactly matches WhenName — condition should apply.
	mMatch := collector.NewMetric("disk",
		map[string]string{},
		map[string]interface{}{"value": float64(90)},
		collector.Gauge, time.Now())

	// Metric name contains but is not equal to WhenName — condition should NOT apply.
	mPartial := collector.NewMetric("disk_io",
		map[string]string{},
		map[string]interface{}{"value": float64(50)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{mMatch, mPartial})

	if result[0].Tags()["severity"] != "critical" {
		t.Errorf("expected severity=critical on disk metric, got %q", result[0].Tags()["severity"])
	}
	if _, ok := result[1].Tags()["severity"]; ok {
		t.Error("expected no severity tag on disk_io metric (partial name match)")
	}
}

func TestApplyEmptySlice(t *testing.T) {
	p := New(Config{
		Tags: map[string]string{"env": "prod"},
	})

	result := p.Apply([]*collector.Metric{})
	if len(result) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(result))
	}
}

func TestInitHappyPath(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"tags": map[string]interface{}{
			"env":    "production",
			"region": "us-east-1",
		},
		"conditions": []interface{}{
			map[string]interface{}{
				"tag":       "critical",
				"value":     "true",
				"when_name": "disk_usage",
			},
		},
	}

	if err := p.Init(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.staticTags) != 2 {
		t.Fatalf("expected 2 static tags, got %d", len(p.staticTags))
	}
	if p.staticTags["env"] != "production" {
		t.Errorf("expected env=production, got %q", p.staticTags["env"])
	}
	if p.staticTags["region"] != "us-east-1" {
		t.Errorf("expected region=us-east-1, got %q", p.staticTags["region"])
	}

	if len(p.conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(p.conditions))
	}
	if p.conditions[0].Tag != "critical" {
		t.Errorf("expected condition tag=critical, got %q", p.conditions[0].Tag)
	}
	if p.conditions[0].Value != "true" {
		t.Errorf("expected condition value=true, got %q", p.conditions[0].Value)
	}
	if p.conditions[0].WhenName != "disk_usage" {
		t.Errorf("expected condition when_name=disk_usage, got %q", p.conditions[0].WhenName)
	}
}

func TestInitTagsNotAMap(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"tags": "not-a-map",
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error when tags is not a map")
	}
	if expected := `tagger: "tags" must be a map, got string`; err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestInitConditionsNotAList(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"conditions": "not-a-list",
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error when conditions is not a list")
	}
	if expected := `tagger: "conditions" must be a list, got string`; err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestInitConditionEntryNotAMap(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"conditions": []interface{}{
			"not-a-map",
		},
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error when condition entry is not a map")
	}
	if expected := "tagger: condition entry 0 must be a map, got string"; err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestInitConditionEntryNotAMapAtIndex(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{"tag": "a", "value": "1", "when_name": "x"},
			42,
		},
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error when condition entry at index 1 is not a map")
	}
	if expected := "tagger: condition entry 1 must be a map, got int"; err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestInitOnlyTags(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"tags": map[string]interface{}{
			"env": "staging",
		},
	}

	if err := p.Init(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(p.staticTags) != 1 {
		t.Fatalf("expected 1 static tag, got %d", len(p.staticTags))
	}
	if p.staticTags["env"] != "staging" {
		t.Errorf("expected env=staging, got %q", p.staticTags["env"])
	}
	if p.conditions != nil {
		t.Errorf("expected nil conditions when not provided, got %v", p.conditions)
	}
}

func TestInitOnlyConditions(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"tag":       "alert",
				"value":     "high",
				"when_name": "cpu",
			},
		},
	}

	if err := p.Init(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.staticTags != nil {
		t.Errorf("expected nil static tags when not provided, got %v", p.staticTags)
	}
	if len(p.conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(p.conditions))
	}
	if p.conditions[0].Tag != "alert" {
		t.Errorf("expected condition tag=alert, got %q", p.conditions[0].Tag)
	}
}

func TestInitEmptyConfig(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{}

	if err := p.Init(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.staticTags != nil {
		t.Errorf("expected nil static tags, got %v", p.staticTags)
	}
	if p.conditions != nil {
		t.Errorf("expected nil conditions, got %v", p.conditions)
	}
}

func TestInitTagsValueConvertedToString(t *testing.T) {
	p := &Processor{}
	cfg := map[string]interface{}{
		"tags": map[string]interface{}{
			"count": 42,
			"rate":  3.14,
			"ok":    true,
		},
	}

	if err := p.Init(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.staticTags["count"] != "42" {
		t.Errorf("expected count=42, got %q", p.staticTags["count"])
	}
	if p.staticTags["rate"] != "3.14" {
		t.Errorf("expected rate=3.14, got %q", p.staticTags["rate"])
	}
	if p.staticTags["ok"] != "true" {
		t.Errorf("expected ok=true, got %q", p.staticTags["ok"])
	}
}
