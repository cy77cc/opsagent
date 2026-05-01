package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	log := zerolog.Nop()
	srv := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Start should return nil after graceful shutdown.
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error after shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}

func TestRateLimiting(t *testing.T) {
	s := newTestServer(t)

	// Send many requests rapidly
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, req)

		if i < 20 {
			// First 20 should succeed (burst)
			if w.Code == http.StatusTooManyRequests {
				t.Errorf("request %d got rate limited too early", i)
			}
		}
	}
	// The 25th should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestServerSetLatestMetric(t *testing.T) {
	log := zerolog.Nop()
	srv := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{})

	if srv.LatestMetricExists() {
		t.Error("expected no metric initially")
	}

	srv.SetLatestMetric(&collector.MetricPayload{Collector: "test"})

	if !srv.LatestMetricExists() {
		t.Error("expected metric to exist after set")
	}
}
