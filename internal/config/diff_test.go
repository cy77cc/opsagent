package config

import (
	"testing"
)

func TestDiff_IdenticalConfigs(t *testing.T) {
	cfg := &Config{
		Agent:      AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10, ShutdownTimeoutSeconds: 30},
		Server:     ServerConfig{ListenAddr: ":8080"},
		Executor:   ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter:   ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		Auth:       AuthConfig{Enabled: false},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
		GRPC:       GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	cs, nonReloadable, err := Diff(cfg, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.CollectorChanged || cs.ReporterChanged || cs.AuthChanged || cs.PrometheusChanged {
		t.Error("expected no changes in ChangeSet")
	}
	if len(nonReloadable) != 0 {
		t.Errorf("expected 0 non-reloadable changes, got %d", len(nonReloadable))
	}
}

func TestDiff_CollectorChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Collector: CollectorConfig{
			Inputs: []PluginInstanceConfig{{Type: "cpu", Config: map[string]interface{}{"per_cpu": false}}},
		},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Collector: CollectorConfig{
			Inputs: []PluginInstanceConfig{{Type: "cpu", Config: map[string]interface{}{"per_cpu": true}}},
		},
	}
	cs, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cs.CollectorChanged {
		t.Error("expected CollectorChanged = true")
	}
	if len(nonReloadable) != 0 {
		t.Errorf("expected 0 non-reloadable, got %d", len(nonReloadable))
	}
}

func TestDiff_NonReloadableChangeRejected(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":9090"}, // changed!
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nonReloadable) == 0 {
		t.Fatal("expected non-reloadable changes")
	}
	found := false
	for _, nr := range nonReloadable {
		if nr.Field == "server.listen_addr" {
			found = true
		}
	}
	if !found {
		t.Error("expected server.listen_addr in non-reloadable list")
	}
}

func TestDiff_MixedChangesNonReloadableBlocksAll(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Auth:     AuthConfig{Enabled: false},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":9090"}, // non-reloadable change
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Auth:     AuthConfig{Enabled: true, BearerToken: "tok"}, // reloadable change
	}
	cs, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cs.AuthChanged {
		t.Error("expected AuthChanged = true")
	}
	if len(nonReloadable) == 0 {
		t.Fatal("expected non-reloadable changes to block")
	}
}

func TestDiff_InvalidNewConfig(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	newCfg := &Config{} // missing required fields
	_, _, err := Diff(old, newCfg)
	if err == nil {
		t.Fatal("expected error for invalid new config")
	}
}

func TestDiff_AuthChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Auth:     AuthConfig{Enabled: false},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Auth:     AuthConfig{Enabled: true, BearerToken: "new-token"},
	}
	cs, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cs.AuthChanged {
		t.Error("expected AuthChanged = true")
	}
	if len(nonReloadable) != 0 {
		t.Errorf("expected 0 non-reloadable, got %d", len(nonReloadable))
	}
}
