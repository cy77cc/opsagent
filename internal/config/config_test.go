package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	content := []byte(`agent:
  id: "a1"
  name: "n1"
  interval_seconds: 5
server:
  listen_addr: "127.0.0.1:18080"
executor:
  timeout_seconds: 3
  max_output_bytes: 1024
  allowed_commands: ["echo"]
reporter:
  mode: "stdout"
  endpoint: ""
`)
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent.ID != "a1" {
		t.Fatalf("unexpected agent id: %s", cfg.Agent.ID)
	}
	if !cfg.Prometheus.Enabled {
		t.Fatalf("expected prometheus enabled by default")
	}
	if cfg.Plugin.Enabled {
		t.Fatalf("expected plugin disabled by default")
	}
}

func TestValidateAuthTokenRequiredWhenEnabled(t *testing.T) {
	cfg := &Config{
		Agent:  AgentConfig{ID: "a1", Name: "n1", IntervalSeconds: 5},
		Server: ServerConfig{ListenAddr: "127.0.0.1:18080"},
		Executor: ExecutorConfig{
			TimeoutSeconds:  3,
			AllowedCommands: []string{"echo"},
			MaxOutputBytes:  1024,
		},
		Reporter:   ReporterConfig{Mode: "stdout", TimeoutSeconds: 3, RetryCount: 1, RetryIntervalMS: 10},
		Auth:       AuthConfig{Enabled: true},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected auth token validation error")
	}
}

func TestValidateHTTPReporterEndpointRequired(t *testing.T) {
	cfg := &Config{
		Agent:  AgentConfig{ID: "a1", Name: "n1", IntervalSeconds: 5},
		Server: ServerConfig{ListenAddr: "127.0.0.1:18080"},
		Executor: ExecutorConfig{
			TimeoutSeconds:  3,
			AllowedCommands: []string{"echo"},
			MaxOutputBytes:  1024,
		},
		Reporter:   ReporterConfig{Mode: "http", Endpoint: "", TimeoutSeconds: 3, RetryCount: 1, RetryIntervalMS: 10},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected reporter.endpoint validation error")
	}
}

func TestValidatePluginRuntimePathRequiredWhenAutoStart(t *testing.T) {
	cfg := &Config{
		Agent:  AgentConfig{ID: "a1", Name: "n1", IntervalSeconds: 5},
		Server: ServerConfig{ListenAddr: "127.0.0.1:18080"},
		Executor: ExecutorConfig{
			TimeoutSeconds:  3,
			AllowedCommands: []string{"echo"},
			MaxOutputBytes:  1024,
		},
		Reporter:   ReporterConfig{Mode: "stdout", TimeoutSeconds: 3, RetryCount: 1, RetryIntervalMS: 10},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
		Plugin: PluginConfig{
			Enabled:               true,
			AutoStart:             true,
			SocketPath:            "/tmp/nodeagentx/plugin.sock",
			RuntimePath:           "",
			StartupTimeoutSeconds: 5,
			RequestTimeoutSeconds: 30,
			MaxConcurrentTasks:    4,
			MaxResultBytes:        1024,
			ChunkSizeBytes:        256,
			SandboxProfile:        "strict",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected plugin runtime path validation error")
	}
}
