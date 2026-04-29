package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config is the root runtime configuration.
type Config struct {
	Agent      AgentConfig      `mapstructure:"agent"`
	Server     ServerConfig     `mapstructure:"server"`
	Executor   ExecutorConfig   `mapstructure:"executor"`
	Reporter   ReporterConfig   `mapstructure:"reporter"`
	Auth       AuthConfig       `mapstructure:"auth"`
	Prometheus PrometheusConfig `mapstructure:"prometheus"`
	Plugin     PluginConfig     `mapstructure:"plugin"`
	GRPC       GRPCConfig       `mapstructure:"grpc"`
	Sandbox    SandboxConfig    `mapstructure:"sandbox"`
	Collector  CollectorConfig  `mapstructure:"collector"`
}

// AgentConfig controls agent identity and collection cadence.
type AgentConfig struct {
	ID              string `mapstructure:"id"`
	Name            string `mapstructure:"name"`
	IntervalSeconds int    `mapstructure:"interval_seconds"`
}

// ServerConfig controls local API server settings.
type ServerConfig struct {
	ListenAddr string `mapstructure:"listen_addr"`
}

// ExecutorConfig controls command execution boundaries.
type ExecutorConfig struct {
	TimeoutSeconds  int      `mapstructure:"timeout_seconds"`
	AllowedCommands []string `mapstructure:"allowed_commands"`
	MaxOutputBytes  int      `mapstructure:"max_output_bytes"`
}

// ReporterConfig controls how data is reported.
type ReporterConfig struct {
	Mode            string `mapstructure:"mode"`
	Endpoint        string `mapstructure:"endpoint"`
	TimeoutSeconds  int    `mapstructure:"timeout_seconds"`
	RetryCount      int    `mapstructure:"retry_count"`
	RetryIntervalMS int    `mapstructure:"retry_interval_ms"`
}

// AuthConfig controls API authentication.
type AuthConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	BearerToken string `mapstructure:"bearer_token"`
}

// PrometheusConfig controls exporter endpoint behavior.
type PrometheusConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Path            string `mapstructure:"path"`
	ProtectWithAuth bool   `mapstructure:"protect_with_auth"`
}

// PluginConfig controls rust runtime integration.
type PluginConfig struct {
	Enabled               bool   `mapstructure:"enabled"`
	RuntimePath           string `mapstructure:"runtime_path"`
	SocketPath            string `mapstructure:"socket_path"`
	AutoStart             bool   `mapstructure:"auto_start"`
	StartupTimeoutSeconds int    `mapstructure:"startup_timeout_seconds"`
	RequestTimeoutSeconds int    `mapstructure:"request_timeout_seconds"`
	MaxConcurrentTasks    int    `mapstructure:"max_concurrent_tasks"`
	MaxResultBytes        int    `mapstructure:"max_result_bytes"`
	ChunkSizeBytes        int    `mapstructure:"chunk_size_bytes"`
	SandboxProfile        string `mapstructure:"sandbox_profile"`
}

// GRPCConfig controls the gRPC client connection to the platform.
type GRPCConfig struct {
	ServerAddr                string     `mapstructure:"server_addr"`
	EnrollToken               string     `mapstructure:"enroll_token"`
	MTLS                      MTLSConfig `mapstructure:"mtls"`
	HeartbeatIntervalSeconds  int        `mapstructure:"heartbeat_interval_seconds"`
	ReconnectInitialBackoffMS int        `mapstructure:"reconnect_initial_backoff_ms"`
	ReconnectMaxBackoffMS     int        `mapstructure:"reconnect_max_backoff_ms"`
}

// MTLSConfig holds mutual TLS certificate paths.
type MTLSConfig struct {
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	CAFile   string `mapstructure:"ca_file"`
}

// SandboxConfig controls nsjail sandbox execution.
type SandboxConfig struct {
	Enabled               bool         `mapstructure:"enabled"`
	NsjailPath            string       `mapstructure:"nsjail_path"`
	BaseWorkdir           string       `mapstructure:"base_workdir"`
	DefaultTimeoutSeconds int          `mapstructure:"default_timeout_seconds"`
	MaxConcurrentTasks    int          `mapstructure:"max_concurrent_tasks"`
	CgroupBasePath        string       `mapstructure:"cgroup_base_path"`
	AuditLogPath          string       `mapstructure:"audit_log_path"`
	Policy                PolicyConfig `mapstructure:"policy"`
}

// PolicyConfig defines the sandbox security policy.
type PolicyConfig struct {
	AllowedCommands     []string `mapstructure:"allowed_commands"`
	BlockedCommands     []string `mapstructure:"blocked_commands"`
	BlockedKeywords     []string `mapstructure:"blocked_keywords"`
	AllowedInterpreters []string `mapstructure:"allowed_interpreters"`
	ScriptMaxBytes      int      `mapstructure:"script_max_bytes"`
	ShellInjectionCheck bool     `mapstructure:"shell_injection_check"`
}

// CollectorConfig defines the metric collection pipeline.
type CollectorConfig struct {
	Inputs      []PluginInstanceConfig `mapstructure:"inputs"`
	Processors  []PluginInstanceConfig `mapstructure:"processors"`
	Aggregators []PluginInstanceConfig `mapstructure:"aggregators"`
	Outputs     []PluginInstanceConfig `mapstructure:"outputs"`
}

// PluginInstanceConfig is a single plugin instance in the collector pipeline.
type PluginInstanceConfig struct {
	Type   string                 `mapstructure:"type"`
	Config map[string]interface{} `mapstructure:"config"`
}

// Load reads and validates configuration from a file path.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	v.SetDefault("agent.interval_seconds", 10)
	v.SetDefault("server.listen_addr", "0.0.0.0:18080")
	v.SetDefault("executor.timeout_seconds", 10)
	v.SetDefault("executor.max_output_bytes", 65536)
	v.SetDefault("reporter.mode", "stdout")
	v.SetDefault("reporter.timeout_seconds", 5)
	v.SetDefault("reporter.retry_count", 3)
	v.SetDefault("reporter.retry_interval_ms", 500)
	v.SetDefault("auth.enabled", false)
	v.SetDefault("prometheus.enabled", true)
	v.SetDefault("prometheus.path", "/metrics")
	v.SetDefault("prometheus.protect_with_auth", false)
	v.SetDefault("plugin.enabled", false)
	v.SetDefault("plugin.runtime_path", "./rust-runtime/target/release/github.com/cy77cc/nodeagentx-rust-runtime")
	v.SetDefault("plugin.socket_path", "/tmp/github.com/cy77cc/nodeagentx/plugin.sock")
	v.SetDefault("plugin.auto_start", true)
	v.SetDefault("plugin.startup_timeout_seconds", 5)
	v.SetDefault("plugin.request_timeout_seconds", 30)
	v.SetDefault("plugin.max_concurrent_tasks", 4)
	v.SetDefault("plugin.max_result_bytes", 8388608)
	v.SetDefault("plugin.chunk_size_bytes", 262144)
	v.SetDefault("plugin.sandbox_profile", "strict")
	v.SetDefault("grpc.heartbeat_interval_seconds", 15)
	v.SetDefault("grpc.reconnect_initial_backoff_ms", 1000)
	v.SetDefault("grpc.reconnect_max_backoff_ms", 30000)
	v.SetDefault("sandbox.enabled", false)
	v.SetDefault("sandbox.default_timeout_seconds", 30)
	v.SetDefault("sandbox.max_concurrent_tasks", 4)
	v.SetDefault("sandbox.policy.shell_injection_check", true)
	v.SetDefault("sandbox.policy.script_max_bytes", 65536)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := v.UnmarshalExact(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks required config values and sane bounds.
func (c *Config) Validate() error {
	if c.Agent.ID == "" {
		return fmt.Errorf("agent.id is required")
	}
	if c.Agent.Name == "" {
		return fmt.Errorf("agent.name is required")
	}
	if c.Agent.IntervalSeconds <= 0 {
		return fmt.Errorf("agent.interval_seconds must be > 0")
	}
	if c.Server.ListenAddr == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	if c.Executor.TimeoutSeconds <= 0 {
		return fmt.Errorf("executor.timeout_seconds must be > 0")
	}
	if len(c.Executor.AllowedCommands) == 0 {
		return fmt.Errorf("executor.allowed_commands must not be empty")
	}
	for _, cmd := range c.Executor.AllowedCommands {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("executor.allowed_commands contains empty command")
		}
	}
	if c.Executor.MaxOutputBytes <= 0 {
		return fmt.Errorf("executor.max_output_bytes must be > 0")
	}

	switch c.Reporter.Mode {
	case "stdout", "http":
	default:
		return fmt.Errorf("reporter.mode must be one of: stdout, http")
	}
	if c.Reporter.Mode == "http" && strings.TrimSpace(c.Reporter.Endpoint) == "" {
		return fmt.Errorf("reporter.endpoint is required when reporter.mode=http")
	}
	if c.Reporter.TimeoutSeconds <= 0 {
		return fmt.Errorf("reporter.timeout_seconds must be > 0")
	}
	if c.Reporter.RetryCount < 0 {
		return fmt.Errorf("reporter.retry_count must be >= 0")
	}
	if c.Reporter.RetryIntervalMS < 0 {
		return fmt.Errorf("reporter.retry_interval_ms must be >= 0")
	}

	if c.Auth.Enabled && strings.TrimSpace(c.Auth.BearerToken) == "" {
		return fmt.Errorf("auth.bearer_token is required when auth.enabled=true")
	}

	if c.Prometheus.Enabled {
		if strings.TrimSpace(c.Prometheus.Path) == "" {
			return fmt.Errorf("prometheus.path is required when prometheus.enabled=true")
		}
		if !strings.HasPrefix(c.Prometheus.Path, "/") {
			return fmt.Errorf("prometheus.path must start with /")
		}
	}

	if c.Plugin.Enabled {
		if strings.TrimSpace(c.Plugin.SocketPath) == "" {
			return fmt.Errorf("plugin.socket_path is required when plugin.enabled=true")
		}
		if c.Plugin.AutoStart && strings.TrimSpace(c.Plugin.RuntimePath) == "" {
			return fmt.Errorf("plugin.runtime_path is required when plugin.auto_start=true")
		}
		if c.Plugin.StartupTimeoutSeconds <= 0 {
			return fmt.Errorf("plugin.startup_timeout_seconds must be > 0")
		}
		if c.Plugin.RequestTimeoutSeconds <= 0 {
			return fmt.Errorf("plugin.request_timeout_seconds must be > 0")
		}
		if c.Plugin.MaxConcurrentTasks <= 0 {
			return fmt.Errorf("plugin.max_concurrent_tasks must be > 0")
		}
		if c.Plugin.MaxResultBytes <= 0 {
			return fmt.Errorf("plugin.max_result_bytes must be > 0")
		}
		if c.Plugin.ChunkSizeBytes <= 0 {
			return fmt.Errorf("plugin.chunk_size_bytes must be > 0")
		}
		if strings.TrimSpace(c.Plugin.SandboxProfile) == "" {
			return fmt.Errorf("plugin.sandbox_profile is required")
		}
	}

	// GRPC validation.
	if strings.TrimSpace(c.GRPC.ServerAddr) == "" {
		return fmt.Errorf("grpc.server_addr is required")
	}
	if c.GRPC.HeartbeatIntervalSeconds <= 0 {
		return fmt.Errorf("grpc.heartbeat_interval_seconds must be > 0")
	}
	if c.GRPC.ReconnectInitialBackoffMS <= 0 {
		return fmt.Errorf("grpc.reconnect_initial_backoff_ms must be > 0")
	}
	if c.GRPC.ReconnectMaxBackoffMS <= 0 {
		return fmt.Errorf("grpc.reconnect_max_backoff_ms must be > 0")
	}

	// Sandbox validation (only when enabled).
	if c.Sandbox.Enabled {
		if strings.TrimSpace(c.Sandbox.NsjailPath) == "" {
			return fmt.Errorf("sandbox.nsjail_path is required when sandbox.enabled=true")
		}
		if strings.TrimSpace(c.Sandbox.BaseWorkdir) == "" {
			return fmt.Errorf("sandbox.base_workdir is required when sandbox.enabled=true")
		}
		if c.Sandbox.DefaultTimeoutSeconds <= 0 {
			return fmt.Errorf("sandbox.default_timeout_seconds must be > 0")
		}
		if c.Sandbox.MaxConcurrentTasks <= 0 {
			return fmt.Errorf("sandbox.max_concurrent_tasks must be > 0")
		}
		if strings.TrimSpace(c.Sandbox.CgroupBasePath) == "" {
			return fmt.Errorf("sandbox.cgroup_base_path is required when sandbox.enabled=true")
		}
		if strings.TrimSpace(c.Sandbox.AuditLogPath) == "" {
			return fmt.Errorf("sandbox.audit_log_path is required when sandbox.enabled=true")
		}
		if len(c.Sandbox.Policy.AllowedCommands) == 0 {
			return fmt.Errorf("sandbox.policy.allowed_commands must not be empty when sandbox.enabled=true")
		}
		if c.Sandbox.Policy.ScriptMaxBytes <= 0 {
			return fmt.Errorf("sandbox.policy.script_max_bytes must be > 0")
		}
	}

	return nil
}
