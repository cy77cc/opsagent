package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	log := zerolog.Nop()
	return New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{})
}

func TestHandleHealthz(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	s.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp apiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleReadyz_NotReady(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	s.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleReadyz_Ready(t *testing.T) {
	s := newTestServer(t)
	s.SetLatestMetric(&collector.MetricPayload{Collector: "test"})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	s.handleReadyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleLatestMetrics_NotFound(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/latest", nil)
	w := httptest.NewRecorder()

	s.handleLatestMetrics(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleLatestMetrics_OK(t *testing.T) {
	s := newTestServer(t)
	s.SetLatestMetric(&collector.MetricPayload{Collector: "cpu"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/latest", nil)
	w := httptest.NewRecorder()

	s.handleLatestMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp apiResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestHandleExec_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/exec", nil)
	w := httptest.NewRecorder()

	s.handleExec(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleExec_BadJSON(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader("{bad"))
	w := httptest.NewRecorder()

	s.handleExec(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleTask_MethodNotAllowed(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	w := httptest.NewRecorder()

	s.handleTask(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleTask_BadJSON(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader("{bad"))
	w := httptest.NewRecorder()

	s.handleTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestParseTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int
		ok    bool
	}{
		{"float64", float64(30), 30, true},
		{"int", 15, 15, true},
		{"string", "20", 20, true},
		{"invalid string", "abc", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTimeoutSeconds(tt.input)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("got = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSetLatestMetric(t *testing.T) {
	s := newTestServer(t)
	if s.LatestMetricExists() {
		t.Error("expected no metric initially")
	}
	s.SetLatestMetric(&collector.MetricPayload{Collector: "test"})
	if !s.LatestMetricExists() {
		t.Error("expected metric to exist after set")
	}
}
