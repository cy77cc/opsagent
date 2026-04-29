package server

import (
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
