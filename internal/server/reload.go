package server

import (
	"fmt"

	"github.com/cy77cc/opsagent/internal/config"
)

// AuthReloader implements config.Reloader for auth configuration.
type AuthReloader struct {
	server *Server
}

// NewAuthReloader creates an AuthReloader.
func NewAuthReloader(server *Server) *AuthReloader {
	return &AuthReloader{server: server}
}

// CanReload returns true if auth config changed.
func (r *AuthReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.AuthChanged
}

// Apply updates the server's auth config.
func (r *AuthReloader) Apply(newCfg *config.Config) error {
	if !newCfg.Auth.Enabled {
		return fmt.Errorf("auth cannot be disabled via hot-reload (restart required)")
	}
	if newCfg.Auth.BearerToken == "" {
		return fmt.Errorf("bearer_token cannot be empty")
	}
	if len(newCfg.Auth.BearerToken) < 32 {
		return fmt.Errorf("bearer_token must be at least 32 characters")
	}
	r.server.UpdateAuth(AuthConfig{
		Enabled:     newCfg.Auth.Enabled,
		BearerToken: newCfg.Auth.BearerToken,
	})
	return nil
}

// Rollback restores the old auth config.
func (r *AuthReloader) Rollback(oldCfg *config.Config) error {
	r.server.UpdateAuth(AuthConfig{
		Enabled:     oldCfg.Auth.Enabled,
		BearerToken: oldCfg.Auth.BearerToken,
	})
	return nil
}

// PrometheusReloader implements config.Reloader for Prometheus configuration.
type PrometheusReloader struct {
	server *Server
}

// NewPrometheusReloader creates a PrometheusReloader.
func NewPrometheusReloader(server *Server) *PrometheusReloader {
	return &PrometheusReloader{server: server}
}

// CanReload returns true if prometheus config changed.
func (r *PrometheusReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.PrometheusChanged
}

// Apply updates the server's prometheus config.
func (r *PrometheusReloader) Apply(newCfg *config.Config) error {
	r.server.UpdatePrometheus(PrometheusConfig{
		Enabled:         newCfg.Prometheus.Enabled,
		Path:            newCfg.Prometheus.Path,
		ProtectWithAuth: newCfg.Prometheus.ProtectWithAuth,
	})
	return nil
}

// Rollback restores the old prometheus config.
func (r *PrometheusReloader) Rollback(oldCfg *config.Config) error {
	r.server.UpdatePrometheus(PrometheusConfig{
		Enabled:         oldCfg.Prometheus.Enabled,
		Path:            oldCfg.Prometheus.Path,
		ProtectWithAuth: oldCfg.Prometheus.ProtectWithAuth,
	})
	return nil
}
