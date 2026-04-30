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
	"github.com/cy77cc/opsagent/internal/health"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

// mockStatuser is a test helper implementing health.Statuser.
type mockStatuser struct {
	status health.Status
}

func (m *mockStatuser) HealthStatus() health.Status { return m.status }

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

func TestHandleHealthz_Enhanced(t *testing.T) {
	log := zerolog.Nop()
	s := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Version:   "1.0.0",
		GitCommit: "abc1234",
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data field")
	}
	if data["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", data["version"])
	}
	if data["git_commit"] != "abc1234" {
		t.Errorf("expected git_commit abc1234, got %v", data["git_commit"])
	}
	if data["status"] == nil {
		t.Error("expected status field")
	}
}

func TestHandleHealthz_StatusLogic(t *testing.T) {
	tests := []struct {
		name       string
		checkers   HealthCheckers
		wantStatus string
	}{
		{
			name: "all healthy",
			checkers: HealthCheckers{
				GRPC:      &mockStatuser{health.Status{Status: "connected"}},
				Scheduler: &mockStatuser{health.Status{Status: "running"}},
				PluginRT:  &mockStatuser{health.Status{Status: "running"}},
			},
			wantStatus: "healthy",
		},
		{
			name: "core grpc unhealthy",
			checkers: HealthCheckers{
				GRPC:      &mockStatuser{health.Status{Status: "disconnected"}},
				Scheduler: &mockStatuser{health.Status{Status: "running"}},
				PluginRT:  &mockStatuser{health.Status{Status: "running"}},
			},
			wantStatus: "unhealthy",
		},
		{
			name: "core scheduler unhealthy",
			checkers: HealthCheckers{
				GRPC:      &mockStatuser{health.Status{Status: "connected"}},
				Scheduler: &mockStatuser{health.Status{Status: "stopped"}},
				PluginRT:  &mockStatuser{health.Status{Status: "running"}},
			},
			wantStatus: "unhealthy",
		},
		{
			name: "non-core plugin_runtime degraded",
			checkers: HealthCheckers{
				GRPC:      &mockStatuser{health.Status{Status: "connected"}},
				Scheduler: &mockStatuser{health.Status{Status: "running"}},
				PluginRT:  &mockStatuser{health.Status{Status: "stopped"}},
			},
			wantStatus: "degraded",
		},
		{
			name: "nil core checker unhealthy",
			checkers: HealthCheckers{
				GRPC:      nil,
				Scheduler: &mockStatuser{health.Status{Status: "running"}},
				PluginRT:  &mockStatuser{health.Status{Status: "running"}},
			},
			wantStatus: "unhealthy",
		},
		{
			name: "nil non-core checker degraded",
			checkers: HealthCheckers{
				GRPC:      &mockStatuser{health.Status{Status: "connected"}},
				Scheduler: &mockStatuser{health.Status{Status: "running"}},
				PluginRT:  nil,
			},
			wantStatus: "degraded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t)
			s.healthCheckers = tt.checkers

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			w := httptest.NewRecorder()
			s.handleHealthz(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}

			var resp map[string]any
			json.NewDecoder(w.Body).Decode(&resp)
			data, ok := resp["data"].(map[string]any)
			if !ok {
				t.Fatal("expected data field")
			}
			if data["status"] != tt.wantStatus {
				t.Errorf("expected status %q, got %v", tt.wantStatus, data["status"])
			}
		})
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
