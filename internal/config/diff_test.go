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
		Auth:     AuthConfig{Enabled: true, BearerToken: "01234567890123456789012345678901"}, // reloadable change
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
		Auth:     AuthConfig{Enabled: true, BearerToken: "01234567890123456789012345678901"},
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

func TestDiff_SandboxChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Sandbox:  SandboxConfig{Enabled: false},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Sandbox:  SandboxConfig{Enabled: true, NsjailPath: "/usr/bin/nsjail", BaseWorkdir: "/work", DefaultTimeoutSeconds: 30, MaxConcurrentTasks: 4, CgroupBasePath: "/sys/fs/cgroup", AuditLogPath: "/var/log/audit.log", Policy: PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536}},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, nr := range nonReloadable {
		if nr.Field == "sandbox.*" {
			found = true
		}
	}
	if !found {
		t.Error("expected sandbox.* in non-reloadable list")
	}
}

func TestDiff_PluginChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Plugin:   PluginConfig{Enabled: false},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Plugin:   PluginConfig{Enabled: true, SocketPath: "/tmp/sock", StartupTimeoutSeconds: 5, RequestTimeoutSeconds: 30, MaxConcurrentTasks: 4, MaxResultBytes: 1024, ChunkSizeBytes: 256, SandboxProfile: "strict"},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, nr := range nonReloadable {
		if nr.Field == "plugin.*" {
			found = true
		}
	}
	if !found {
		t.Error("expected plugin.* in non-reloadable list")
	}
}

func TestDiff_AgentNameChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n1", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n2", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, nr := range nonReloadable {
		if nr.Field == "agent.name" {
			found = true
		}
	}
	if !found {
		t.Error("expected agent.name in non-reloadable list")
	}
}

func TestDiff_GRPCFieldsChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", EnrollToken: "tok1", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000, CachePersistPath: "/tmp/c"},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "y:443", EnrollToken: "tok2", HeartbeatIntervalSeconds: 20, ReconnectInitialBackoffMS: 2000, ReconnectMaxBackoffMS: 60000, CachePersistPath: "/tmp/d"},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedFields := []string{"grpc.server_addr", "grpc.enroll_token", "grpc.heartbeat_interval_seconds", "grpc.reconnect_initial_backoff_ms", "grpc.reconnect_max_backoff_ms", "grpc.cache_persist_path"}
	for _, field := range expectedFields {
		found := false
		for _, nr := range nonReloadable {
			if nr.Field == field {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in non-reloadable list", field)
		}
	}
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"short", "abc", "***"},
		{"4 chars", "abcd", "***"},
		{"long", "abcdefghijklmnop", "ab***op"},
		{"empty", "", "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskSecret(tt.input)
			if got != tt.want {
				t.Errorf("maskSecret(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDiff_ExecutorFieldsChanged(t *testing.T) {
	old := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	newCfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 20, AllowedCommands: []string{"ls", "ps"}, MaxOutputBytes: 2048},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
	}
	_, nonReloadable, err := Diff(old, newCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedFields := []string{"executor.timeout_seconds", "executor.allowed_commands", "executor.max_output_bytes"}
	for _, field := range expectedFields {
		found := false
		for _, nr := range nonReloadable {
			if nr.Field == field {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in non-reloadable list", field)
		}
	}
}
