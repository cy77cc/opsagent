package server

import (
	"context"
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
	// Without auth enabled, version info should NOT be exposed.
	if data["version"] != nil {
		t.Errorf("expected no version without auth, got %v", data["version"])
	}
	if data["git_commit"] != nil {
		t.Errorf("expected no git_commit without auth, got %v", data["git_commit"])
	}
	if data["uptime_seconds"] != nil {
		t.Errorf("expected no uptime_seconds without auth, got %v", data["uptime_seconds"])
	}
	if data["status"] == nil {
		t.Error("expected status field")
	}
}

func TestHandleHealthz_AuthenticatedVersionDisclosure(t *testing.T) {
	log := zerolog.Nop()
	s := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Version:   "1.0.0",
		GitCommit: "abc1234",
		Auth: AuthConfig{
			Enabled:     true,
			BearerToken: "test-secret",
		},
	})

	// Without auth header: version info should NOT be exposed.
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.handleHealthz(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data := resp["data"].(map[string]any)
	if data["version"] != nil {
		t.Errorf("expected no version without auth header, got %v", data["version"])
	}

	// With valid auth header: version info SHOULD be exposed.
	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req2.Header.Set("Authorization", "Bearer test-secret")
	w2 := httptest.NewRecorder()
	s.handleHealthz(w2, req2)

	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	data2 := resp2["data"].(map[string]any)
	if data2["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", data2["version"])
	}
	if data2["git_commit"] != "abc1234" {
		t.Errorf("expected git_commit abc1234, got %v", data2["git_commit"])
	}
	if data2["uptime_seconds"] == nil {
		t.Error("expected uptime_seconds with valid auth")
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

func TestHandleExec_RejectsOversizedBody(t *testing.T) {
	// Use an executor with "echo" in the allowlist so that without
	// MaxBytesReader the request would succeed (200).
	log := zerolog.Nop()
	exec := executor.New([]string{"echo"}, 1*time.Second, 1024)
	s := New(":0", log, exec, task.NewDispatcher(), time.Now(), Options{})

	bigBody := `{"command":"echo","args":["` + strings.Repeat("x", 2*1024*1024) + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", w.Code)
	}

	var resp apiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Fatalf("expected 'invalid request body' in error, got %q", resp.Error)
	}
}

func TestHandleTask_RejectsOversizedBody(t *testing.T) {
	s := newTestServer(t)

	bigBody := `{"type":"test","payload":{"data":"` + strings.Repeat("x", 2*1024*1024) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized body, got %d", w.Code)
	}

	// Verify the 400 comes from the body size limit, not downstream validation.
	var resp apiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.Contains(resp.Error, "invalid request body") {
		t.Fatalf("expected 'invalid request body' in error, got %q", resp.Error)
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

func TestHandleExec_Success(t *testing.T) {
	log := zerolog.Nop()
	exec := executor.New([]string{"echo"}, 1*time.Second, 1024)
	s := New(":0", log, exec, task.NewDispatcher(), time.Now(), Options{})

	body := `{"command":"echo","args":["hello"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleExec(w, req)

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

func TestHandleTask_Success(t *testing.T) {
	log := zerolog.Nop()
	dispatcher := task.NewDispatcher()
	dispatcher.Register("ping", func(_ context.Context, _ task.AgentTask) (any, error) {
		return map[string]string{"result": "pong"}, nil
	})
	s := New(":0", log, &executor.Executor{}, dispatcher, time.Now(), Options{})

	body := `{"task_id":"1","type":"ping","payload":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleTask(w, req)

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

func TestHandleExec_ErrorDoesNotLeakInternalDetails(t *testing.T) {
	s := newTestServer(t)

	body := `{"command": "forbidden_cmd", "args": []}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	var resp apiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.Error == "" {
		t.Fatal("expected non-empty error message")
	}
	if strings.Contains(resp.Error, "forbidden_cmd") {
		t.Errorf("error message leaks command name: %q", resp.Error)
	}
}

func TestReadyzRejectsPost(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/readyz", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /readyz status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestLatestMetricsRejectsPost(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics/latest", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/v1/metrics/latest status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleTask_TimeoutCapped(t *testing.T) {
	log := zerolog.Nop()
	dispatcher := task.NewDispatcher()
	dispatcher.Register("ping", func(ctx context.Context, _ task.AgentTask) (any, error) {
		// Verify the context deadline does not exceed maxTimeoutSeconds.
		deadline, ok := ctx.Deadline()
		if ok {
			remaining := time.Until(deadline)
			if remaining > time.Duration(maxTimeoutSeconds+1)*time.Second {
				t.Errorf("timeout not capped: deadline is %v in the future, want at most %v", remaining, maxTimeoutSeconds)
			}
		}
		return map[string]string{"result": "pong"}, nil
	})
	s := New(":0", log, &executor.Executor{}, dispatcher, time.Now(), Options{})

	body := `{"task_id":"1","type":"ping","payload":{"timeout_seconds":999999}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
	w := httptest.NewRecorder()

	s.handleTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
