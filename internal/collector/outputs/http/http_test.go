package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

func TestHTTPOutput_Init(t *testing.T) {
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
				"url":               "http://localhost:8080",
				"timeout":           5,
				"batch_size":        100,
				"retry_count":       2,
				"retry_interval_ms": 200,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HTTPOutput{}
			err := h.Init(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHTTPOutput_Write(t *testing.T) {
	var mu sync.Mutex
	var received payload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusBadRequest)
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

	h := &HTTPOutput{}
	if err := h.Init(map[string]interface{}{
		"url":        ts.URL,
		"timeout":    5,
		"batch_size": 10,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	defer h.Close()

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("cpu.usage", map[string]string{"host": "server1"}, map[string]interface{}{"value": 75.5}, collector.Gauge, now),
		*collector.NewMetric("requests.count", map[string]string{"endpoint": "/api"}, map[string]interface{}{"value": int64(100)}, collector.Counter, now),
	}

	if err := h.Write(metrics); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if received.Count != 2 {
		t.Errorf("expected count 2, got %d", received.Count)
	}
	if len(received.Metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(received.Metrics))
	}
	if received.Metrics[0].Name != "cpu.usage" {
		t.Errorf("expected metric name cpu.usage, got %s", received.Metrics[0].Name)
	}
	if received.Metrics[0].Tags["host"] != "server1" {
		t.Errorf("expected tag host=server1, got %s", received.Metrics[0].Tags["host"])
	}
	if received.Metrics[0].Timestamp != now.UnixMilli() {
		t.Errorf("expected timestamp %d, got %d", now.UnixMilli(), received.Metrics[0].Timestamp)
	}
}

func TestHTTPOutput_RetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := &HTTPOutput{}
	if err := h.Init(map[string]interface{}{
		"url":               ts.URL,
		"timeout":           5,
		"retry_count":       3,
		"retry_interval_ms": 10,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, now),
	}

	if err := h.Write(metrics); err != nil {
		t.Fatalf("Write() should succeed after retries, got error: %v", err)
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestHTTPOutput_RetryExhaustion(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	h := &HTTPOutput{}
	if err := h.Init(map[string]interface{}{
		"url":               ts.URL,
		"timeout":           5,
		"retry_count":       2,
		"retry_interval_ms": 10,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := []collector.Metric{
		*collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, now),
	}

	err := h.Write(metrics)
	if err == nil {
		t.Fatal("Write() should fail after retry exhaustion")
	}

	if got := attempts.Load(); got != 3 { // initial + 2 retries
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestHTTPOutput_Batching(t *testing.T) {
	var requestCount atomic.Int32
	var mu sync.Mutex
	var allCounts []int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		var p payload
		json.Unmarshal(body, &p)
		mu.Lock()
		allCounts = append(allCounts, p.Count)
		mu.Unlock()
		if p.Count > 2 {
			t.Errorf("batch size exceeded: got %d", p.Count)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	h := &HTTPOutput{}
	if err := h.Init(map[string]interface{}{
		"url":        ts.URL,
		"timeout":    5,
		"batch_size": 2,
	}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	now := time.Now()
	metrics := make([]collector.Metric, 5)
	for i := range metrics {
		metrics[i] = *collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": float64(i)}, collector.Gauge, now)
	}

	if err := h.Write(metrics); err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	if got := requestCount.Load(); got != 3 { // 5 metrics / batch_size 2 = 3 batches (2+2+1)
		t.Errorf("expected 3 requests, got %d", got)
	}
}

func TestHTTPOutput_SampleConfig(t *testing.T) {
	h := &HTTPOutput{}
	cfg := h.SampleConfig()
	if cfg == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
