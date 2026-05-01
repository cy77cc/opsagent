package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func TestAuthMiddlewareProtectsAPI(t *testing.T) {
	exec := executor.New([]string{"echo"}, 1*time.Second, 1024)
	dispatcher := task.NewDispatcher()
	s := New("127.0.0.1:0", zerolog.Nop(), exec, dispatcher, time.Now(), Options{
		Auth:       AuthConfig{Enabled: true, BearerToken: "secret"},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics", ProtectWithAuth: false},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRecoverMiddleware_PanicReturns500(t *testing.T) {
	s := newTestServer(t)
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	handler := s.recoverMiddleware(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	var resp apiResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Success {
		t.Error("expected success=false")
	}
	if resp.Error != "internal server error" {
		t.Errorf("expected error 'internal server error', got %q", resp.Error)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	log := zerolog.Nop()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	s := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Auth: AuthConfig{Enabled: true, BearerToken: "mytoken"},
	})
	handler := s.authMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/exec", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("expected inner handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequiresAuth_PrometheusProtectWithAuth(t *testing.T) {
	s := New(":0", zerolog.Nop(), &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Auth:       AuthConfig{Enabled: true, BearerToken: "tok"},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics", ProtectWithAuth: true},
	})

	if !s.requiresAuth("/metrics") {
		t.Error("expected /metrics to require auth when ProtectWithAuth=true")
	}
	if !s.requiresAuth("/api/v1/exec") {
		t.Error("expected /api/v1/exec to require auth")
	}
	if s.requiresAuth("/healthz") {
		t.Error("expected /healthz to not require auth")
	}
}

func TestAuthMiddleware_PrometheusUnauthorizedPlainError(t *testing.T) {
	s := New(":0", zerolog.Nop(), &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Auth:       AuthConfig{Enabled: true, BearerToken: "tok"},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics", ProtectWithAuth: true},
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called")
	})
	handler := s.authMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
