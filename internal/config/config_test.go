package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validGRPCConfig returns a minimal valid GRPC config for use in tests.
func validGRPCConfig() GRPCConfig {
	return GRPCConfig{
		ServerAddr:                "platform.example.com:443",
		HeartbeatIntervalSeconds:  15,
		ReconnectInitialBackoffMS: 1000,
		ReconnectMaxBackoffMS:     30000,
	}
}

// validBaseConfig returns a Config that passes all unconditional validation.
func validBaseConfig() *Config {
	return &Config{
		Agent:  AgentConfig{ID: "a1", Name: "n1", IntervalSeconds: 5},
		Server: ServerConfig{ListenAddr: "127.0.0.1:18080"},
		Executor: ExecutorConfig{
			TimeoutSeconds:  3,
			AllowedCommands: []string{"echo"},
			MaxOutputBytes:  1024,
		},
		Reporter:   ReporterConfig{Mode: "stdout", TimeoutSeconds: 3, RetryCount: 1, RetryIntervalMS: 10},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
		GRPC:       validGRPCConfig(),
	}
}

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
auth:
  bearer_token: "01234567890123456789012345678901"
grpc:
  server_addr: "platform.example.com:443"
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
	if cfg.GRPC.ServerAddr != "platform.example.com:443" {
		t.Fatalf("unexpected grpc server addr: %s", cfg.GRPC.ServerAddr)
	}
	if cfg.GRPC.HeartbeatIntervalSeconds != 15 {
		t.Fatalf("expected default heartbeat 15, got %d", cfg.GRPC.HeartbeatIntervalSeconds)
	}
	if cfg.Sandbox.Enabled {
		t.Fatalf("expected sandbox disabled by default")
	}
}

func TestLoadConfigGRPCDefaults(t *testing.T) {
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
auth:
  bearer_token: "01234567890123456789012345678901"
grpc:
  server_addr: "platform:443"
`)
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GRPC.HeartbeatIntervalSeconds != 15 {
		t.Errorf("expected default heartbeat 15, got %d", cfg.GRPC.HeartbeatIntervalSeconds)
	}
	if cfg.GRPC.ReconnectInitialBackoffMS != 1000 {
		t.Errorf("expected default reconnect initial backoff 1000, got %d", cfg.GRPC.ReconnectInitialBackoffMS)
	}
	if cfg.GRPC.ReconnectMaxBackoffMS != 30000 {
		t.Errorf("expected default reconnect max backoff 30000, got %d", cfg.GRPC.ReconnectMaxBackoffMS)
	}
	if cfg.Sandbox.DefaultTimeoutSeconds != 30 {
		t.Errorf("expected default sandbox timeout 30, got %d", cfg.Sandbox.DefaultTimeoutSeconds)
	}
	if cfg.Sandbox.MaxConcurrentTasks != 4 {
		t.Errorf("expected default sandbox max concurrent 4, got %d", cfg.Sandbox.MaxConcurrentTasks)
	}
	if !cfg.Sandbox.Policy.ShellInjectionCheck {
		t.Errorf("expected default shell_injection_check true")
	}
	if cfg.Sandbox.Policy.ScriptMaxBytes != 65536 {
		t.Errorf("expected default script_max_bytes 65536, got %d", cfg.Sandbox.Policy.ScriptMaxBytes)
	}
}

func TestValidateAuthTokenRequiredWhenEnabled(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Auth = AuthConfig{Enabled: true}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected auth token validation error")
	}
}

func TestValidateHTTPReporterEndpointRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Reporter = ReporterConfig{Mode: "http", Endpoint: "", TimeoutSeconds: 3, RetryCount: 1, RetryIntervalMS: 10}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected reporter.endpoint validation error")
	}
}

func TestValidatePluginRuntimePathRequiredWhenAutoStart(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		AutoStart:             true,
		SocketPath:            "/tmp/github.com/cy77cc/opsagent/plugin.sock",
		RuntimePath:           "",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected plugin runtime path validation error")
	}
}

func TestValidateGRPCServerAddrRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GRPC.ServerAddr = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "grpc.server_addr") {
		t.Fatalf("expected grpc.server_addr error, got: %v", err)
	}
}

func TestValidateGRPCHeartbeatMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GRPC.HeartbeatIntervalSeconds = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "grpc.heartbeat_interval_seconds") {
		t.Fatalf("expected heartbeat error, got: %v", err)
	}
}

func TestValidateGRPCReconnectInitialBackoffMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GRPC.ReconnectInitialBackoffMS = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "grpc.reconnect_initial_backoff_ms") {
		t.Fatalf("expected reconnect initial backoff error, got: %v", err)
	}
}

func TestValidateGRPCReconnectMaxBackoffMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.GRPC.ReconnectMaxBackoffMS = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "grpc.reconnect_max_backoff_ms") {
		t.Fatalf("expected reconnect max backoff error, got: %v", err)
	}
}

func TestValidateSandboxDisabledSkipsChecks(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error when sandbox disabled, got: %v", err)
	}
}

func TestValidateSandboxNsjailPathRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.nsjail_path") {
		t.Fatalf("expected nsjail_path error, got: %v", err)
	}
}

func TestValidateSandboxBaseWorkdirRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.base_workdir") {
		t.Fatalf("expected base_workdir error, got: %v", err)
	}
}

func TestValidateSandboxTimeoutMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 0,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.default_timeout_seconds") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestValidateSandboxMaxConcurrentMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    0,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.max_concurrent_tasks") {
		t.Fatalf("expected max_concurrent_tasks error, got: %v", err)
	}
}

func TestValidateSandboxCgroupBasePathRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.cgroup_base_path") {
		t.Fatalf("expected cgroup_base_path error, got: %v", err)
	}
}

func TestValidateSandboxAuditLogPathRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.audit_log_path") {
		t.Fatalf("expected audit_log_path error, got: %v", err)
	}
}

func TestValidateSandboxAllowedCommandsRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.policy.allowed_commands") {
		t.Fatalf("expected allowed_commands error, got: %v", err)
	}
}

func TestValidateSandboxScriptMaxBytesMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo"}, ScriptMaxBytes: 0},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sandbox.policy.script_max_bytes") {
		t.Fatalf("expected script_max_bytes error, got: %v", err)
	}
}

func TestValidateSandboxValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Sandbox = SandboxConfig{
		Enabled:               true,
		NsjailPath:            "/usr/bin/nsjail",
		BaseWorkdir:           "/work",
		DefaultTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		CgroupBasePath:        "/sys/fs/cgroup",
		AuditLogPath:          "/var/log/audit.log",
		Policy:                PolicyConfig{AllowedCommands: []string{"echo", "ls"}, ScriptMaxBytes: 65536},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error for valid sandbox config, got: %v", err)
	}
}

func TestConfig_ShutdownTimeoutSeconds(t *testing.T) {
	c := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10, ShutdownTimeoutSeconds: 45},
		Server:   ServerConfig{ListenAddr: ":0"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000, CachePersistPath: "/tmp/cache.json"},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Agent.ShutdownTimeoutSeconds != 45 {
		t.Errorf("ShutdownTimeoutSeconds = %d, want 45", c.Agent.ShutdownTimeoutSeconds)
	}
	if c.GRPC.CachePersistPath != "/tmp/cache.json" {
		t.Errorf("CachePersistPath = %q, want %q", c.GRPC.CachePersistPath, "/tmp/cache.json")
	}
}

// --- Boundary value tests ---

func TestValidateBoundaryAgentID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"empty", "", true},
		{"single char", "a", false},
		{"very long 300 chars", strings.Repeat("x", 300), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Agent.ID = tt.id
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("id length=%d, wantErr=%v, got err=%v", len(tt.id), tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryAgentIntervalSeconds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero", 0, true},
		{"one (lower bound)", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Agent.IntervalSeconds = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("interval_seconds=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryExecutorTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero", 0, true},
		{"one (lower bound)", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Executor.TimeoutSeconds = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("timeout_seconds=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryExecutorMaxOutputBytes(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero", 0, true},
		{"one (lower bound)", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Executor.MaxOutputBytes = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("max_output_bytes=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryReporterMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{"empty", "", true},
		{"invalid value", "file", true},
		{"stdout", "stdout", false},
		{"http", "http", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Reporter.Mode = tt.mode
			if tt.mode == "http" {
				cfg.Reporter.Endpoint = "http://example.com/report"
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("mode=%q, wantErr=%v, got err=%v", tt.mode, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryReporterHTTPEndpointEmpty(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Reporter.Mode = "http"
	cfg.Reporter.Endpoint = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for http mode with empty endpoint")
	}
}

func TestValidateBoundaryReporterStdoutEndpointNotRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Reporter.Mode = "stdout"
	cfg.Reporter.Endpoint = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error for stdout mode with empty endpoint, got: %v", err)
	}
}

func TestValidateBoundaryReporterTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero", 0, true},
		{"one (lower bound)", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Reporter.TimeoutSeconds = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("timeout_seconds=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryReporterRetryCount(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero (lower bound)", 0, false},
		{"positive", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Reporter.RetryCount = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("retry_count=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

func TestValidateBoundaryReporterRetryIntervalMS(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{"negative", -1, true},
		{"zero (lower bound)", 0, false},
		{"positive", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Reporter.RetryIntervalMS = tt.value
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("retry_interval_ms=%d, wantErr=%v, got err=%v", tt.value, tt.wantErr, err)
			}
		})
	}
}

// --- Load error paths ---

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := Load("/tmp/nonexistent_config_file_12345.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "bad.yaml")
	if err := os.WriteFile(cfgPath, []byte("not: valid: yaml: ["), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// --- Plugin validation ---

func TestValidatePluginSocketPathRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.socket_path") {
		t.Fatalf("expected plugin.socket_path error, got: %v", err)
	}
}

func TestValidatePluginStartupTimeoutMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "/tmp/sock",
		StartupTimeoutSeconds: 0,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.startup_timeout_seconds") {
		t.Fatalf("expected plugin.startup_timeout_seconds error, got: %v", err)
	}
}

func TestValidatePluginRequestTimeoutMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "/tmp/sock",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 0,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.request_timeout_seconds") {
		t.Fatalf("expected plugin.request_timeout_seconds error, got: %v", err)
	}
}

func TestValidatePluginMaxConcurrentTasksMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "/tmp/sock",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    0,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.max_concurrent_tasks") {
		t.Fatalf("expected plugin.max_concurrent_tasks error, got: %v", err)
	}
}

func TestValidatePluginMaxResultBytesMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "/tmp/sock",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        0,
		ChunkSizeBytes:        256,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.max_result_bytes") {
		t.Fatalf("expected plugin.max_result_bytes error, got: %v", err)
	}
}

func TestValidatePluginChunkSizeBytesMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{
		Enabled:               true,
		SocketPath:            "/tmp/sock",
		StartupTimeoutSeconds: 5,
		RequestTimeoutSeconds: 30,
		MaxConcurrentTasks:    4,
		MaxResultBytes:        1024,
		ChunkSizeBytes:        0,
		SandboxProfile:        "strict",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin.chunk_size_bytes") {
		t.Fatalf("expected plugin.chunk_size_bytes error, got: %v", err)
	}
}

// --- PluginGateway validation ---

func TestValidatePluginGatewayPluginsDirRequired(t *testing.T) {
	cfg := validBaseConfig()
	cfg.PluginGateway = PluginGatewayConfig{
		Enabled:                 true,
		PluginsDir:              "",
		StartupTimeoutSeconds:   10,
		HealthCheckIntervalSecs: 30,
		MaxRestarts:             3,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin_gateway.plugins_dir") {
		t.Fatalf("expected plugin_gateway.plugins_dir error, got: %v", err)
	}
}

func TestValidatePluginGatewayStartupTimeoutMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.PluginGateway = PluginGatewayConfig{
		Enabled:                 true,
		PluginsDir:              "/etc/plugins",
		StartupTimeoutSeconds:   0,
		HealthCheckIntervalSecs: 30,
		MaxRestarts:             3,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin_gateway.startup_timeout_seconds") {
		t.Fatalf("expected plugin_gateway.startup_timeout_seconds error, got: %v", err)
	}
}

func TestValidatePluginGatewayHealthCheckIntervalMustBePositive(t *testing.T) {
	cfg := validBaseConfig()
	cfg.PluginGateway = PluginGatewayConfig{
		Enabled:                 true,
		PluginsDir:              "/etc/plugins",
		StartupTimeoutSeconds:   10,
		HealthCheckIntervalSecs: 0,
		MaxRestarts:             3,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin_gateway.health_check_interval_seconds") {
		t.Fatalf("expected plugin_gateway.health_check_interval_seconds error, got: %v", err)
	}
}

func TestValidatePluginGatewayMaxRestartsMustBeNonNegative(t *testing.T) {
	cfg := validBaseConfig()
	cfg.PluginGateway = PluginGatewayConfig{
		Enabled:                 true,
		PluginsDir:              "/etc/plugins",
		StartupTimeoutSeconds:   10,
		HealthCheckIntervalSecs: 30,
		MaxRestarts:             -1,
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "plugin_gateway.max_restarts") {
		t.Fatalf("expected plugin_gateway.max_restarts error, got: %v", err)
	}
}

// --- Prometheus path validation ---

func TestValidatePrometheusPathMustStartWithSlash(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Prometheus = PrometheusConfig{Enabled: true, Path: "metrics"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "prometheus.path must start with /") {
		t.Fatalf("expected prometheus.path prefix error, got: %v", err)
	}
}

func TestValidatePrometheusPathEmpty(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Prometheus = PrometheusConfig{Enabled: true, Path: "  "}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "prometheus.path is required") {
		t.Fatalf("expected prometheus.path required error, got: %v", err)
	}
}

// --- AllowedCommands empty entry validation ---

func TestValidateAllowedCommandsEmptyEntry(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Executor.AllowedCommands = []string{"echo", "  "}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "empty command") {
		t.Fatalf("expected empty command error, got: %v", err)
	}
}

// --- Plugin disabled skips checks ---

func TestValidatePluginDisabledSkipsChecks(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Plugin = PluginConfig{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error when plugin disabled, got: %v", err)
	}
}

// --- PluginGateway disabled skips checks ---

func TestValidatePluginGatewayDisabledSkipsChecks(t *testing.T) {
	cfg := validBaseConfig()
	cfg.PluginGateway = PluginGatewayConfig{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error when plugin_gateway disabled, got: %v", err)
	}
}

func TestValidate_AuthTokenMinLength(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"empty", "", true},
		{"too short 1 char", "a", true},
		{"too short 31 chars", strings.Repeat("a", 31), true},
		{"exactly 32 chars", strings.Repeat("a", 32), false},
		{"long token", strings.Repeat("a", 64), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.Auth = AuthConfig{Enabled: true, BearerToken: tt.token}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("token length=%d, wantErr=%v, got err=%v", len(tt.token), tt.wantErr, err)
			}
		})
	}
}

func TestLoadConfig_ListenAddrLocalhostByDefault(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	content := []byte(`agent:
  id: "a1"
  name: "n1"
  interval_seconds: 5
executor:
  timeout_seconds: 3
  max_output_bytes: 1024
  allowed_commands: ["echo"]
reporter:
  mode: "stdout"
auth:
  bearer_token: "01234567890123456789012345678901"
grpc:
  server_addr: "platform.example.com:443"
`)
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.ListenAddr != "127.0.0.1:18080" {
		t.Errorf("expected listen addr 127.0.0.1:18080, got %s", cfg.Server.ListenAddr)
	}
}

func TestLoadConfig_AuthEnabledByDefault(t *testing.T) {
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
auth:
  bearer_token: "01234567890123456789012345678901"
grpc:
  server_addr: "platform.example.com:443"
`)
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Auth.Enabled {
		t.Error("expected auth.enabled to be true by default")
	}
}
