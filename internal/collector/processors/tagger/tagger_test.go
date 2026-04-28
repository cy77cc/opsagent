package tagger

import (
	"testing"
	"time"

	"nodeagentx/internal/collector"
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
