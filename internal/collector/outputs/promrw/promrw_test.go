package promrw

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func TestPromRWOutput_Init(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]interface{}
		wantErr bool
	}{
		{
			name:    "missing url",
			cfg:     map[string]interface{}{},
			wantErr: true,
		},
		{
			name:    "empty url",
			cfg:     map[string]interface{}{"url": ""},
			wantErr: true,
		},
		{
			name: "valid config",
			cfg: map[string]interface{}{
				"url":     "http://localhost:9090/api/v1/write",
				"timeout": 5,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PromRWOutput{}
			err := p.Init(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPromRWOutput_Write(t *testing.T) {
	var mu sync.Mutex
	var received remoteWritePayload
	var headers http.Header

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = r.Header.Clone()
		mu.Unlock()

		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		mu.Lock()
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("failed to unmarshal body: %v", err)
		}
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := &PromRWOutput{}
	if err := p.Init(map[string]interface{}{
		"url":     ts.URL,
		"timeout": 5,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("cpu.usage", map[string]string{"host": "server1"}, map[string]interface{}{"value": 75.5}, collector.Gauge, now),
		*collector.NewMetric("requests.count", map[string]string{"endpoint": "/api"}, map[string]interface{}{"value": int64(100)}, collector.Counter, now),
	}

	if err := p.Write(metrics); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Check headers
	if headers.Get("Content-Type") != "application/x-protobuf" {
		t.Errorf("expected Content-Type application/x-protobuf, got %s", headers.Get("Content-Type"))
	}
	if headers.Get("X-Prometheus-Remote-Write-Version") != "0.1.0" {
		t.Errorf("expected X-Prometheus-Remote-Write-Version 0.1.0, got %s", headers.Get("X-Prometheus-Remote-Write-Version"))
	}

	// Check payload
	if len(received.TimeSeries) != 2 {
		t.Fatalf("expected 2 timeseries, got %d", len(received.TimeSeries))
	}

	// First timeseries: cpu.usage
	ts0 := received.TimeSeries[0]
	if ts0.Labels[0].Name != "__name__" || ts0.Labels[0].Value != "cpu.usage" {
		t.Errorf("expected __name__=cpu.usage, got %s=%s", ts0.Labels[0].Name, ts0.Labels[0].Value)
	}
	if len(ts0.Samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(ts0.Samples))
	}
	if ts0.Samples[0].Value != 75.5 {
		t.Errorf("expected value 75.5, got %f", ts0.Samples[0].Value)
	}
	if ts0.Samples[0].Timestamp != now.UnixMilli() {
		t.Errorf("expected timestamp %d, got %d", now.UnixMilli(), ts0.Samples[0].Timestamp)
	}

	// Check host label exists
	found := false
	for _, l := range ts0.Labels {
		if l.Name == "host" && l.Value == "server1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected host=server1 label in cpu.usage timeseries")
	}
}

func TestPromRWOutput_EmptyMetrics(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not receive request for empty metrics")
	}))
	defer ts.Close()

	p := &PromRWOutput{}
	if err := p.Init(map[string]interface{}{
		"url": ts.URL,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	if err := p.Write([]collector.Metric{}); err != nil {
		t.Fatalf("Write() should not error for empty metrics, got: %v", err)
	}
}

func TestPromRWOutput_LabelSorting(t *testing.T) {
	var mu sync.Mutex
	var received remoteWritePayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(body, &received)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := &PromRWOutput{}
	if err := p.Init(map[string]interface{}{
		"url":     ts.URL,
		"timeout": 5,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	m := collector.NewMetric(
		"test",
		map[string]string{"z_label": "z", "a_label": "a", "m_label": "m"},
		map[string]interface{}{"value": 1.0},
		collector.Gauge,
		now,
	)

	if err := p.Write([]collector.Metric{*m}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received.TimeSeries) != 1 {
		t.Fatalf("expected 1 timeseries, got %d", len(received.TimeSeries))
	}

	labels := received.TimeSeries[0].Labels
	// __name__ should be first
	if labels[0].Name != "__name__" {
		t.Errorf("expected __name__ first, got %s", labels[0].Name)
	}

	// Remaining labels should be sorted
	for i := 1; i < len(labels)-1; i++ {
		if labels[i].Name > labels[i+1].Name {
			t.Errorf("labels not sorted: %s > %s at positions %d, %d", labels[i].Name, labels[i+1].Name, i, i+1)
		}
	}
}

func TestPromRWOutput_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	p := &PromRWOutput{}
	if err := p.Init(map[string]interface{}{
		"url":     ts.URL,
		"timeout": 5,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, now),
	}

	err := p.Write(metrics)
	if err == nil {
		t.Error("Write() should error on 5xx response")
	}
}

func TestPromRWOutput_SampleConfig(t *testing.T) {
	p := &PromRWOutput{}
	cfg := p.SampleConfig()
	if cfg == "" {
		t.Error("SampleConfig() should not be empty")
	}
}

func TestPromRWOutput_Close(t *testing.T) {
	p := &PromRWOutput{}
	if err := p.Close(); err != nil {
		t.Errorf("Close() should not error, got: %v", err)
	}
}

func TestPromRWOutput_Timestamp(t *testing.T) {
	var mu sync.Mutex
	var received remoteWritePayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(body, &received)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	p := &PromRWOutput{}
	if err := p.Init(map[string]interface{}{
		"url":     ts.URL,
		"timeout": 5,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	ts2 := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	m := collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 42.0}, collector.Gauge, ts2)

	if err := p.Write([]collector.Metric{*m}); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received.TimeSeries) != 1 {
		t.Fatalf("expected 1 timeseries, got %d", len(received.TimeSeries))
	}

	s := received.TimeSeries[0].Samples[0]
	if s.Timestamp != ts2.UnixMilli() {
		t.Errorf("expected timestamp %d, got %d", ts2.UnixMilli(), s.Timestamp)
	}
	if s.Value != 42.0 {
		t.Errorf("expected value 42.0, got %f", s.Value)
	}
}
