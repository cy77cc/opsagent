package grpcclient

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestMetricTypeString(t *testing.T) {
	tests := []struct {
		input collector.MetricType
		want  string
	}{
		{collector.Counter, "counter"},
		{collector.Gauge, "gauge"},
		{collector.Histogram, "gauge"}, // default falls through to "gauge"
		{collector.MetricType(99), "gauge"},
	}

	for _, tt := range tests {
		got := metricTypeString(tt.input)
		if got != tt.want {
			t.Errorf("metricTypeString(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMetricTypeFromString(t *testing.T) {
	tests := []struct {
		input string
		want  collector.MetricType
	}{
		{"counter", collector.Counter},
		{"gauge", collector.Gauge},
		{"unknown", collector.Gauge}, // default falls through to Gauge
		{"", collector.Gauge},
	}

	for _, tt := range tests {
		got := metricTypeFromString(tt.input)
		if got != tt.want {
			t.Errorf("metricTypeFromString(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestMetricTypeRoundTrip(t *testing.T) {
	// Counter and Gauge should round-trip through string conversion.
	types := []collector.MetricType{collector.Counter, collector.Gauge}
	for _, mt := range types {
		s := metricTypeString(mt)
		got := metricTypeFromString(s)
		if got != mt {
			t.Errorf("round-trip failed for %d: string=%q, back=%d", mt, s, got)
		}
	}
}

func TestPersistAndLoadMetrics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.json")

	ts := time.UnixMilli(1700000000000)
	metrics := []*collector.Metric{
		collector.NewMetric(
			"cpu_usage",
			map[string]string{"host": "web-1"},
			map[string]interface{}{"value": float64(85.3)},
			collector.Gauge,
			ts,
		),
		collector.NewMetric(
			"request_count",
			map[string]string{"host": "web-1", "endpoint": "/api"},
			map[string]interface{}{"value": int64(42)},
			collector.Counter,
			ts,
		),
	}

	if err := persistMetrics(metrics, path); err != nil {
		t.Fatalf("persistMetrics failed: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("persisted file does not exist")
	}

	loaded, err := loadMetrics(path)
	if err != nil {
		t.Fatalf("loadMetrics failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(loaded))
	}

	// Verify first metric.
	if loaded[0].Name() != "cpu_usage" {
		t.Errorf("expected cpu_usage, got %s", loaded[0].Name())
	}
	if loaded[0].Type() != collector.Gauge {
		t.Errorf("expected Gauge, got %d", loaded[0].Type())
	}
	tags := loaded[0].Tags()
	if tags["host"] != "web-1" {
		t.Errorf("expected host=web-1, got %s", tags["host"])
	}

	// Verify second metric.
	if loaded[1].Name() != "request_count" {
		t.Errorf("expected request_count, got %s", loaded[1].Name())
	}
	if loaded[1].Type() != collector.Counter {
		t.Errorf("expected Counter, got %d", loaded[1].Type())
	}
	if loaded[1].Timestamp().UnixMilli() != 1700000000000 {
		t.Errorf("expected timestamp 1700000000000, got %d", loaded[1].Timestamp().UnixMilli())
	}
}

func TestPersistMetrics_EmptySlice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")

	if err := persistMetrics(nil, path); err != nil {
		t.Fatalf("persistMetrics failed for nil slice: %v", err)
	}

	loaded, err := loadMetrics(path)
	if err != nil {
		t.Fatalf("loadMetrics failed: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("expected 0 metrics, got %d", len(loaded))
	}
}

func TestLoadMetrics_NonExistentFile(t *testing.T) {
	_, err := loadMetrics("/nonexistent/path/metrics.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadMetrics_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not valid json{{{"), 0644)

	_, err := loadMetrics(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestPersistMetrics_InvalidPath(t *testing.T) {
	metrics := []*collector.Metric{
		collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now()),
	}
	// Use a directory path as the file path - writing to a directory should fail
	dir := t.TempDir()
	err := persistMetrics(metrics, dir)
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestLoadMetrics_FieldsPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fields.json")

	metrics := []*collector.Metric{
		collector.NewMetric(
			"multi_field",
			map[string]string{"env": "prod"},
			map[string]interface{}{
				"cpu":    float64(90.5),
				"memory": int64(1024),
			},
			collector.Gauge,
			time.UnixMilli(1700000001000),
		),
	}

	if err := persistMetrics(metrics, path); err != nil {
		t.Fatalf("persistMetrics failed: %v", err)
	}

	loaded, err := loadMetrics(path)
	if err != nil {
		t.Fatalf("loadMetrics failed: %v", err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(loaded))
	}

	fields := loaded[0].Fields()
	if fields["cpu"] != float64(90.5) {
		t.Errorf("expected cpu=90.5, got %v", fields["cpu"])
	}
	// JSON unmarshals numbers as float64 by default.
	if fields["memory"] != float64(1024) {
		t.Errorf("expected memory=1024, got %v", fields["memory"])
	}
}
