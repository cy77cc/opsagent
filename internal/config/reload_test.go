package config

import (
	"context"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
)

// mockReloader implements Reloader for testing.
type mockReloader struct {
	canReloadFn    func(*ChangeSet) bool
	applyFn        func(*Config) error
	rollbackFn     func(*Config) error
	applyCalled    int
	rollbackCalled int
}

func (m *mockReloader) CanReload(cs *ChangeSet) bool {
	if m.canReloadFn != nil {
		return m.canReloadFn(cs)
	}
	return false
}

func (m *mockReloader) Apply(cfg *Config) error {
	m.applyCalled++
	if m.applyFn != nil {
		return m.applyFn(cfg)
	}
	return nil
}

func (m *mockReloader) Rollback(cfg *Config) error {
	m.rollbackCalled++
	if m.rollbackFn != nil {
		return m.rollbackFn(cfg)
	}
	return nil
}

func baseConfig() *Config {
	return &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
}

func TestConfigReloader_ApplySuccess(t *testing.T) {
	oldCfg := baseConfig()
	reloader := &mockReloader{
		canReloadFn: func(cs *ChangeSet) bool { return cs.AuthChanged },
	}
	cr := NewConfigReloader(oldCfg, zerolog.Nop(), reloader)

	yamlData := []byte(`agent:
  id: a
  name: n
  interval_seconds: 10
server:
  listen_addr: ":8080"
executor:
  timeout_seconds: 10
  allowed_commands: ["ls"]
  max_output_bytes: 1024
reporter:
  mode: stdout
  timeout_seconds: 5
auth:
  enabled: true
  bearer_token: tok
grpc:
  server_addr: "x:443"
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000`)

	err := cr.Apply(context.Background(), yamlData)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if reloader.applyCalled != 1 {
		t.Errorf("Apply called %d times, want 1", reloader.applyCalled)
	}
}

func TestConfigReloader_RejectsNonReloadable(t *testing.T) {
	oldCfg := baseConfig()
	reloader := &mockReloader{}
	cr := NewConfigReloader(oldCfg, zerolog.Nop(), reloader)

	yamlData := []byte(`agent:
  id: a
  name: n
  interval_seconds: 10
server:
  listen_addr: ":9090"
executor:
  timeout_seconds: 10
  allowed_commands: ["ls"]
  max_output_bytes: 1024
reporter:
  mode: stdout
  timeout_seconds: 5
grpc:
  server_addr: "x:443"
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000`)

	err := cr.Apply(context.Background(), yamlData)
	if err == nil {
		t.Fatal("expected error for non-reloadable change")
	}
	if reloader.applyCalled != 0 {
		t.Errorf("Apply should not be called, got %d", reloader.applyCalled)
	}
}

func TestConfigReloader_InvalidYAML(t *testing.T) {
	oldCfg := baseConfig()
	cr := NewConfigReloader(oldCfg, zerolog.Nop())

	err := cr.Apply(context.Background(), []byte("not: valid: yaml: ["))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestConfigReloader_RollbackOnFailure(t *testing.T) {
	oldCfg := baseConfig()
	authReloader := &mockReloader{
		canReloadFn: func(cs *ChangeSet) bool { return cs.AuthChanged },
		applyFn:     func(_ *Config) error { return nil },
	}
	failReloader := &mockReloader{
		canReloadFn: func(cs *ChangeSet) bool { return cs.AuthChanged },
		applyFn:     func(_ *Config) error { return fmt.Errorf("apply failed") },
	}
	cr := NewConfigReloader(oldCfg, zerolog.Nop(), authReloader, failReloader)

	yamlData := []byte(`agent:
  id: a
  name: n
  interval_seconds: 10
server:
  listen_addr: ":8080"
executor:
  timeout_seconds: 10
  allowed_commands: ["ls"]
  max_output_bytes: 1024
reporter:
  mode: stdout
  timeout_seconds: 5
auth:
  enabled: true
  bearer_token: tok
grpc:
  server_addr: "x:443"
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000`)

	err := cr.Apply(context.Background(), yamlData)
	if err == nil {
		t.Fatal("expected error from Apply")
	}
	if authReloader.rollbackCalled != 1 {
		t.Errorf("authReloader.Rollback called %d times, want 1", authReloader.rollbackCalled)
	}
}
