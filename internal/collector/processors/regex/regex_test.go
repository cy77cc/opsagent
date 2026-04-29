package regex

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestNewValidConfig(t *testing.T) {
	cfg := Config{
		Tags: []Rule{
			{Key: "host", Pattern: `(\d+)`, Replacement: "num"},
		},
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
}

func TestNewInvalidPattern(t *testing.T) {
	cfg := Config{
		Tags: []Rule{
			{Key: "host", Pattern: `[invalid`, Replacement: ""},
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid regex pattern")
	}
}

func TestNewEmptyKey(t *testing.T) {
	cfg := Config{
		Tags: []Rule{
			{Key: "", Pattern: `.*`, Replacement: "x"},
		},
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestApplySingleRule(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "host", Pattern: `\d+`, Replacement: "REDACTED"},
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
	if tags["host"] != "server-REDACTED" {
		t.Errorf("expected host=server-REDACTED, got %q", tags["host"])
	}
}

func TestApplyMultipleRules(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "host", Pattern: `\d+`, Replacement: "X"},
			{Key: "env", Pattern: `prod`, Replacement: "production"},
		},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "srv-01", "env": "prod"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	if tags["host"] != "srv-X" {
		t.Errorf("expected host=srv-X, got %q", tags["host"])
	}
	if tags["env"] != "production" {
		t.Errorf("expected env=production, got %q", tags["env"])
	}
}

func TestApplyMissingTag(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "nonexistent", Pattern: `.*`, Replacement: "replaced"},
		},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	if tags["host"] != "server" {
		t.Errorf("expected host unchanged, got %q", tags["host"])
	}
	if _, ok := tags["nonexistent"]; ok {
		t.Error("expected nonexistent tag to not be added")
	}
}

func TestApplyEmptyRules(t *testing.T) {
	p, _ := New(Config{})

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
		t.Errorf("expected host unchanged, got %q", tags["host"])
	}
}

func TestApplyMultipleMetrics(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "host", Pattern: `\d+`, Replacement: "NUM"},
		},
	})

	metrics := []*collector.Metric{
		collector.NewMetric("cpu",
			map[string]string{"host": "server-01"},
			map[string]interface{}{"value": float64(1)},
			collector.Gauge, time.Now()),
		collector.NewMetric("mem",
			map[string]string{"host": "server-02"},
			map[string]interface{}{"value": float64(2)},
			collector.Gauge, time.Now()),
	}

	result := p.Apply(metrics)
	if len(result) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(result))
	}

	for _, m := range result {
		tags := m.Tags()
		if tags["host"] != "server-NUM" {
			t.Errorf("expected host=server-NUM, got %q", tags["host"])
		}
	}
}

func TestSampleConfig(t *testing.T) {
	p, _ := New(Config{})
	cfg := p.SampleConfig()
	if cfg == "" {
		t.Error("expected non-empty sample config")
	}
}

func TestRegisteredInDefaultRegistry(t *testing.T) {
	f, ok := collector.DefaultRegistry.GetProcessor("regex")
	if !ok {
		t.Fatal("regex processor not registered in default registry")
	}
	p := f()
	if p == nil {
		t.Fatal("expected non-nil processor from factory")
	}
}

func TestApplyNoMatch(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "host", Pattern: `^no-such-pattern$`, Replacement: "replaced"},
		},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(42)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	// Pattern does not match, tag value should remain unchanged.
	if tags["host"] != "server-01" {
		t.Errorf("expected host=server-01 (unchanged), got %q", tags["host"])
	}
}

func TestApplyMultipleRulesSameKeyInOrder(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			// First rule: replace digits with "NUM".
			{Key: "host", Pattern: `\d+`, Replacement: "NUM"},
			// Second rule: replace "NUM" with "REDACTED" (operates on result of first rule).
			{Key: "host", Pattern: `NUM`, Replacement: "REDACTED"},
		},
	})

	m := collector.NewMetric("cpu",
		map[string]string{"host": "server-01"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	tags := result[0].Tags()

	// Rules are applied in order: "server-01" -> "server-NUM" -> "server-REDACTED".
	if tags["host"] != "server-REDACTED" {
		t.Errorf("expected host=server-REDACTED, got %q", tags["host"])
	}
}

func TestInitInvalidPattern(t *testing.T) {
	p, _ := New(Config{})

	cfg := map[string]interface{}{
		"tags": []interface{}{
			map[string]interface{}{
				"key":         "host",
				"pattern":     `[invalid`,
				"replacement": "x",
			},
		},
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error for invalid regex pattern in Init")
	}
}

func TestInitEmptyKey(t *testing.T) {
	p, _ := New(Config{})

	cfg := map[string]interface{}{
		"tags": []interface{}{
			map[string]interface{}{
				"key":         "",
				"pattern":     `.*`,
				"replacement": "x",
			},
		},
	}

	err := p.Init(cfg)
	if err == nil {
		t.Fatal("expected error for empty key in Init")
	}
}

func TestApplyEmptyInputSlice(t *testing.T) {
	p, _ := New(Config{
		Tags: []Rule{
			{Key: "host", Pattern: `\d+`, Replacement: "REDACTED"},
		},
	})

	result := p.Apply([]*collector.Metric{})
	if len(result) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(result))
	}
}

func TestInitNoTagsKey(t *testing.T) {
	p, _ := New(Config{})

	// Config map without "tags" key — Init should succeed with no rules.
	cfg := map[string]interface{}{}

	err := p.Init(cfg)
	if err != nil {
		t.Fatalf("unexpected error when tags key is missing: %v", err)
	}

	// Apply should be a no-op.
	m := collector.NewMetric("cpu",
		map[string]string{"host": "server"},
		map[string]interface{}{"value": float64(1)},
		collector.Gauge, time.Now())

	result := p.Apply([]*collector.Metric{m})
	if result[0].Tags()["host"] != "server" {
		t.Errorf("expected host=server (unchanged), got %q", result[0].Tags()["host"])
	}
}
