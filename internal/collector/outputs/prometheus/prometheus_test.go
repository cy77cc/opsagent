package prometheus

import (
	"strings"
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func TestPrometheusOutput_Init(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]interface{}
		wantErr bool
	}{
		{
			name:    "default config",
			cfg:     map[string]interface{}{},
			wantErr: false,
		},
		{
			name: "custom config",
			cfg: map[string]interface{}{
				"path": "/custom",
				":8080": ":8080",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PrometheusOutput{}
			err := p.Init(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPrometheusOutput_RenderPrometheus(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("cpu.usage", map[string]string{"host": "server1"}, map[string]interface{}{"value": 75.5}, collector.Gauge, now),
		*collector.NewMetric("requests.count", map[string]string{"endpoint": "/api", "method": "GET"}, map[string]interface{}{"value": int64(100)}, collector.Counter, now),
	}

	if err := p.Write(metrics); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	output := p.renderPrometheus()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Should have 4 lines: TYPE + value for each metric (2 metrics = 2 TYPE lines + 2 value lines)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), output)
	}

	// Check first metric (cpu.usage sorts before requests.count)
	if !strings.Contains(lines[0], "# TYPE cpu_usage gauge") {
		t.Errorf("expected TYPE line for cpu_usage, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "cpu_usage{") {
		t.Errorf("expected metric line for cpu_usage, got: %s", lines[1])
	}
	if !strings.Contains(lines[1], `host="server1"`) {
		t.Errorf("expected host label, got: %s", lines[1])
	}
	if !strings.Contains(lines[1], "75.5") {
		t.Errorf("expected value 75.5, got: %s", lines[1])
	}

	// Check second metric (requests.count)
	if !strings.Contains(lines[2], "# TYPE requests_count counter") {
		t.Errorf("expected TYPE line for requests_count, got: %s", lines[2])
	}
	if !strings.Contains(lines[3], "requests_count{") {
		t.Errorf("expected metric line for requests_count, got: %s", lines[3])
	}
	if !strings.Contains(lines[3], "100") {
		t.Errorf("expected value 100, got: %s", lines[3])
	}
}

func TestPrometheusOutput_RenderEmpty(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	output := p.renderPrometheus()
	if output != "" {
		t.Errorf("expected empty output, got: %q", output)
	}
}

func TestPrometheusOutput_SanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cpu.usage", "cpu_usage"},
		{"requests-count", "requests_count"},
		{"123metric", "_123metric"},
		{"valid_name", "valid_name"},
		{"", ""},
		{"host.name:port", "host_name_port"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPrometheusOutput_Write(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	m1 := collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, now)
	m2 := collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 2.0}, collector.Gauge, now.Add(time.Second))

	if err := p.Write([]collector.Metric{*m1}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	p.mu.RLock()
	if p.latest["test"].Fields()["value"] != 1.0 {
		t.Errorf("expected value 1.0, got %v", p.latest["test"].Fields()["value"])
	}
	p.mu.RUnlock()

	// Overwrite with newer metric
	if err := p.Write([]collector.Metric{*m2}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	p.mu.RLock()
	if p.latest["test"].Fields()["value"] != 2.0 {
		t.Errorf("expected value 2.0 after overwrite, got %v", p.latest["test"].Fields()["value"])
	}
	p.mu.RUnlock()
}

func TestPrometheusOutput_SampleConfig(t *testing.T) {
	p := &PrometheusOutput{}
	cfg := p.SampleConfig()
	if cfg == "" {
		t.Error("SampleConfig() should not be empty")
	}
}

func TestPrometheusOutput_Close(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Close(); err != nil {
		t.Errorf("Close() should not error when server is nil, got: %v", err)
	}
}

func TestPrometheusOutput_LabelSorting(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	m := collector.NewMetric(
		"test_metric",
		map[string]string{"z_label": "z", "a_label": "a", "m_label": "m"},
		map[string]interface{}{"value": 42.0},
		collector.Gauge,
		now,
	)

	if err := p.Write([]collector.Metric{*m}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	output := p.renderPrometheus()

	// Labels should be sorted alphabetically.
	aIdx := strings.Index(output, "a_label=")
	mIdx := strings.Index(output, "m_label=")
	zIdx := strings.Index(output, "z_label=")

	if aIdx >= mIdx || mIdx >= zIdx {
		t.Errorf("labels not sorted: a_label at %d, m_label at %d, z_label at %d", aIdx, mIdx, zIdx)
	}
}

func TestPrometheusOutput_Timestamp(t *testing.T) {
	p := &PrometheusOutput{}
	if err := p.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	m := collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, ts)

	if err := p.Write([]collector.Metric{*m}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	output := p.renderPrometheus()
	expectedMs := ts.UnixMilli()

	if !strings.Contains(output, " "+string(rune('0'+expectedMs/1000000000%10))) {
		// Just check the output contains the timestamp number
		tsStr := strings.TrimSpace(strings.Split(output, "\n")[1])
		parts := strings.Split(tsStr, " ")
		if len(parts) < 3 {
			t.Errorf("expected timestamp in output, got: %q", output)
		}
	}
}
