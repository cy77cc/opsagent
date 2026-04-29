package server

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func newTestServerWithOpts(opts Options) *Server {
	return New(":0", zerolog.Nop(), executor.New([]string{"ls"}, 10*time.Second, 1024), task.NewDispatcher(), time.Now(), opts)
}

func TestServer_UpdateAuth(t *testing.T) {
	s := newTestServerWithOpts(Options{Auth: AuthConfig{Enabled: false}})
	s.UpdateAuth(AuthConfig{Enabled: true, BearerToken: "new-tok"})
	got := s.GetAuth()
	if !got.Enabled {
		t.Error("expected auth enabled after update")
	}
	if got.BearerToken != "new-tok" {
		t.Errorf("expected token 'new-tok', got %q", got.BearerToken)
	}
}

func TestServer_UpdatePrometheus(t *testing.T) {
	s := newTestServerWithOpts(Options{Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"}})
	s.UpdatePrometheus(PrometheusConfig{Enabled: true, Path: "/new-metrics"})
	got := s.GetPrometheus()
	if got.Path != "/new-metrics" {
		t.Errorf("expected path '/new-metrics', got %q", got.Path)
	}
}

func TestAuthReloader_CanReload(t *testing.T) {
	s := newTestServerWithOpts(Options{})
	r := NewAuthReloader(s)
	if !r.CanReload(&config.ChangeSet{AuthChanged: true}) {
		t.Error("expected CanReload = true")
	}
	if r.CanReload(&config.ChangeSet{AuthChanged: false}) {
		t.Error("expected CanReload = false")
	}
}

func TestAuthReloader_ApplyRollback(t *testing.T) {
	s := newTestServerWithOpts(Options{Auth: AuthConfig{Enabled: false}})
	r := NewAuthReloader(s)

	newCfg := &config.Config{Auth: config.AuthConfig{Enabled: true, BearerToken: "tok"}}
	if err := r.Apply(newCfg); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !s.GetAuth().Enabled {
		t.Error("expected auth enabled after Apply")
	}

	oldCfg := &config.Config{Auth: config.AuthConfig{Enabled: false}}
	if err := r.Rollback(oldCfg); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	if s.GetAuth().Enabled {
		t.Error("expected auth disabled after Rollback")
	}
}

func TestPrometheusReloader_CanReload(t *testing.T) {
	s := newTestServerWithOpts(Options{})
	r := NewPrometheusReloader(s)
	if !r.CanReload(&config.ChangeSet{PrometheusChanged: true}) {
		t.Error("expected CanReload = true")
	}
	if r.CanReload(&config.ChangeSet{PrometheusChanged: false}) {
		t.Error("expected CanReload = false")
	}
}

func TestPrometheusReloader_ApplyRollback(t *testing.T) {
	s := newTestServerWithOpts(Options{Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"}})
	r := NewPrometheusReloader(s)

	newCfg := &config.Config{Prometheus: config.PrometheusConfig{Enabled: true, Path: "/new-metrics", ProtectWithAuth: true}}
	if err := r.Apply(newCfg); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	got := s.GetPrometheus()
	if got.Path != "/new-metrics" || !got.ProtectWithAuth {
		t.Errorf("unexpected prometheus config after Apply: %+v", got)
	}

	oldCfg := &config.Config{Prometheus: config.PrometheusConfig{Enabled: true, Path: "/metrics"}}
	if err := r.Rollback(oldCfg); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
	got = s.GetPrometheus()
	if got.Path != "/metrics" || got.ProtectWithAuth {
		t.Errorf("unexpected prometheus config after Rollback: %+v", got)
	}
}
