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
		SocketPath:            "/tmp/github.com/cy77cc/nodeagentx/plugin.sock",
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
