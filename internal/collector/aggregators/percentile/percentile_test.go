package percentile

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

// testAccumulator is a simple accumulator that records AddGaugeWithTimestamp calls.
type testAccumulator struct {
	metrics []struct {
		name   string
		tags   map[string]string
		fields map[string]interface{}
	}
}

func (t *testAccumulator) AddFields(name string, tags map[string]string, fields map[string]interface{}) {
}
func (t *testAccumulator) AddGauge(name string, tags map[string]string, fields map[string]interface{}) {}
func (t *testAccumulator) AddCounter(name string, tags map[string]string, fields map[string]interface{}) {
}
func (t *testAccumulator) AddFieldsWithTimestamp(name string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
}
func (t *testAccumulator) AddGaugeWithTimestamp(name string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
	t.metrics = append(t.metrics, struct {
		name   string
		tags   map[string]string
		fields map[string]interface{}
	}{name: name, tags: tags, fields: fields})
}
func (t *testAccumulator) AddCounterWithTimestamp(name string, tags map[string]string, fields map[string]interface{}, ts time.Time) {
}
func (t *testAccumulator) Collect() []*collector.Metric { return nil }

func newMetric(name string, tags map[string]string, fields map[string]interface{}) *collector.Metric {
	return collector.NewMetric(name, tags, fields, collector.Gauge, time.Now())
}

func floatEquals(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// TestKnownDistribution verifies p50, p95, p99 for values 1..100.
func TestKnownDistribution(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"response_time_ms"},
		"percentiles": []interface{}{50, 95, 99},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	for i := 1; i <= 100; i++ {
		m := newMetric("http_request", map[string]string{"host": "web1"},
			map[string]interface{}{"response_time_ms": float64(i)})
		agg.Add(m)
	}

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(acc.metrics))
	}
	m := acc.metrics[0]
	if m.name != "http_request" {
		t.Errorf("expected name 'http_request', got '%s'", m.name)
	}
	if m.tags["host"] != "web1" {
		t.Errorf("expected tag host=web1, got '%s'", m.tags["host"])
	}

	fields := m.fields
	// For 1..100 (100 values):
	// p50: index = 50/100 * 99 = 49.5 -> sorted[49] + (sorted[50]-sorted[49]) * 0.5 = 50 + (51-50)*0.5 = 50.5
	// p95: index = 95/100 * 99 = 94.05 -> sorted[94] + (sorted[95]-sorted[94]) * 0.05 = 95 + (96-95)*0.05 = 95.05
	// p99: index = 99/100 * 99 = 98.01 -> sorted[98] + (sorted[99]-sorted[98]) * 0.01 = 99 + (100-99)*0.01 = 99.01
	p50, ok := fields["response_time_ms_p50"].(float64)
	if !ok {
		t.Fatalf("response_time_ms_p50 not a float64, got %T", fields["response_time_ms_p50"])
	}
	if !floatEquals(p50, 50.5, 0.001) {
		t.Errorf("p50 = %v, want 50.5", p50)
	}

	p95, ok := fields["response_time_ms_p95"].(float64)
	if !ok {
		t.Fatalf("response_time_ms_p95 not a float64, got %T", fields["response_time_ms_p95"])
	}
	if !floatEquals(p95, 95.05, 0.001) {
		t.Errorf("p95 = %v, want 95.05", p95)
	}

	p99, ok := fields["response_time_ms_p99"].(float64)
	if !ok {
		t.Fatalf("response_time_ms_p99 not a float64, got %T", fields["response_time_ms_p99"])
	}
	if !floatEquals(p99, 99.01, 0.001) {
		t.Errorf("p99 = %v, want 99.01", p99)
	}
}

// TestEmptyWindow verifies that pushing with no data emits nothing.
func TestEmptyWindow(t *testing.T) {
	cfg := map[string]interface{}{
		"fields": []interface{}{"response_time_ms"},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 0 {
		t.Errorf("expected 0 metrics from empty window, got %d", len(acc.metrics))
	}
}

// TestSingleValue verifies that a single value yields that value for all percentiles.
func TestSingleValue(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"latency_ms"},
		"percentiles": []interface{}{50, 95, 99},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	m := newMetric("request", map[string]string{},
		map[string]interface{}{"latency_ms": float64(42)})
	agg.Add(m)

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(acc.metrics))
	}
	fields := acc.metrics[0].fields
	for _, suffix := range []string{"_p50", "_p95", "_p99"} {
		key := "latency_ms" + suffix
		val, ok := fields[key].(float64)
		if !ok {
			t.Errorf("%s not a float64, got %T", key, fields[key])
			continue
		}
		if !floatEquals(val, 42, 0.001) {
			t.Errorf("%s = %v, want 42", key, val)
		}
	}
}

// TestLargeDataset verifies correctness with 10000 values.
func TestLargeDataset(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"value"},
		"percentiles": []interface{}{50, 95, 99},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	n := 10000
	for i := 1; i <= n; i++ {
		m := newMetric("data", nil, map[string]interface{}{"value": float64(i)})
		agg.Add(m)
	}

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(acc.metrics))
	}
	fields := acc.metrics[0].fields

	// For 1..10000 (sorted):
	// p50: index = 0.5 * 9999 = 4999.5 -> sorted[4999] + (sorted[5000]-sorted[4999]) * 0.5 = 5000 + 0.5 = 5000.5
	// p95: index = 0.95 * 9999 = 9499.05 -> sorted[9499] + (sorted[9500]-sorted[9499]) * 0.05 = 9500 + 0.05 = 9500.05
	// p99: index = 0.99 * 9999 = 9899.01 -> sorted[9899] + (sorted[9900]-sorted[9899]) * 0.01 = 9900 + 0.01 = 9900.01
	tests := []struct {
		key      string
		expected float64
	}{
		{"value_p50", 5000.5},
		{"value_p95", 9500.05},
		{"value_p99", 9900.01},
	}
	for _, tt := range tests {
		val, ok := fields[tt.key].(float64)
		if !ok {
			t.Errorf("%s not a float64, got %T", tt.key, fields[tt.key])
			continue
		}
		if !floatEquals(val, tt.expected, 0.01) {
			t.Errorf("%s = %v, want %v", tt.key, val, tt.expected)
		}
	}
}

// TestCustomPercentiles verifies non-default percentiles (25, 75).
func TestCustomPercentiles(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"value"},
		"percentiles": []interface{}{25, 75},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Add 1..100
	for i := 1; i <= 100; i++ {
		m := newMetric("test", nil, map[string]interface{}{"value": float64(i)})
		agg.Add(m)
	}

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(acc.metrics))
	}
	fields := acc.metrics[0].fields

	// p25: index = 25/100 * 99 = 24.75 -> sorted[24] + (sorted[25]-sorted[24]) * 0.75 = 25 + (26-25)*0.75 = 25.75
	// p75: index = 75/100 * 99 = 74.25 -> sorted[74] + (sorted[75]-sorted[74]) * 0.25 = 75 + (76-75)*0.25 = 75.25
	p25, ok := fields["value_p25"].(float64)
	if !ok {
		t.Fatalf("value_p25 not a float64, got %T", fields["value_p25"])
	}
	if !floatEquals(p25, 25.75, 0.001) {
		t.Errorf("p25 = %v, want 25.75", p25)
	}

	p75, ok := fields["value_p75"].(float64)
	if !ok {
		t.Fatalf("value_p75 not a float64, got %T", fields["value_p75"])
	}
	if !floatEquals(p75, 75.25, 0.001) {
		t.Errorf("p75 = %v, want 75.25", p75)
	}

	// Ensure no p50/p95/p99 fields exist
	for _, key := range []string{"value_p50", "value_p95", "value_p99"} {
		if _, exists := fields[key]; exists {
			t.Errorf("unexpected field %s present", key)
		}
	}
}

// TestRegisteredInDefaultRegistry verifies "percentile" is registered.
func TestRegisteredInDefaultRegistry(t *testing.T) {
	factory, ok := collector.DefaultRegistry.GetAggregator("percentile")
	if !ok {
		t.Fatal("percentile aggregator not registered in DefaultRegistry")
	}
	agg := factory()
	if agg == nil {
		t.Fatal("factory returned nil aggregator")
	}
}

// TestReset verifies that Reset clears accumulated state.
func TestReset(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"val"},
		"percentiles": []interface{}{50},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	m := newMetric("m", map[string]string{"k": "v"},
		map[string]interface{}{"val": float64(42)})
	agg.Add(m)
	agg.Reset()

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 0 {
		t.Errorf("expected 0 metrics after reset, got %d", len(acc.metrics))
	}
}

// TestConcurrentSafety verifies concurrent Add calls don't race.
func TestConcurrentSafety(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"val"},
		"percentiles": []interface{}{50, 95, 99},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m := newMetric("test", map[string]string{"g": "g"},
				map[string]interface{}{"val": float64(n * 10)})
			agg.Add(m)
		}(i)
	}
	wg.Wait()
	// No panic = pass.
}

// TestDefaultPercentiles verifies that omitting percentiles uses [50, 95, 99].
func TestDefaultPercentiles(t *testing.T) {
	cfg := map[string]interface{}{
		"fields": []interface{}{"val"},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	m := newMetric("m", nil, map[string]interface{}{"val": float64(42)})
	agg.Add(m)

	acc := &testAccumulator{}
	agg.Push(acc)

	if len(acc.metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(acc.metrics))
	}
	fields := acc.metrics[0].fields
	for _, key := range []string{"val_p50", "val_p95", "val_p99"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("expected field %s with default percentiles", key)
		}
	}
}

// TestPercentileValidationRejectsOutOfRange verifies [0,100] range check.
func TestPercentileValidationRejectsOutOfRange(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"val"},
		"percentiles": []interface{}{150},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err == nil {
		t.Error("expected error for percentile > 100")
	}

	cfg["percentiles"] = []interface{}{-10}
	agg = &Aggregator{}
	if err := agg.Init(cfg); err == nil {
		t.Error("expected error for percentile < 0")
	}
}

// TestPercentileValidationRejectsFractional verifies whole-number check.
func TestPercentileValidationRejectsFractional(t *testing.T) {
	cfg := map[string]interface{}{
		"fields":      []interface{}{"val"},
		"percentiles": []interface{}{99.9},
	}
	agg := &Aggregator{}
	if err := agg.Init(cfg); err == nil {
		t.Error("expected error for fractional percentile")
	}
}
