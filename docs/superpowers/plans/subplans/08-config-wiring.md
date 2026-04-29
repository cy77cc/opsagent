# Sub-Plan 8: Config & Agent Wiring

> **Parent:** [OpsAgent Full Implementation Plan](../2026-04-28-opsagent-full-implementation.md)
> **Depends on:** Sub-Plans 1-7

**Goal:** Extend configuration structs for gRPC, sandbox, and collector subsystems; wire all new components into the agent lifecycle; update config.yaml with new sections.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/app/agent.go`
- Modify: `configs/config.yaml`

---

## Task 8.1: Extend Config Structs

- [ ] **Step 1: Write failing test for new config sections**

Add to `internal/config/config_test.go`:

```go
func TestLoadGRPCSandboxCollectorConfig(t *testing.T) {
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
  server_addr: "control.example.com:8443"
  enroll_token: "tok-abc123"
  mtls:
    cert_file: "/etc/opsagent/certs/client.crt"
    key_file: "/etc/opsagent/certs/client.key"
    ca_file: "/etc/opsagent/certs/ca.crt"
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000
sandbox:
  enabled: true
  nsjail_path: "/usr/bin/nsjail"
  base_workdir: "/tmp/opsagent/sandbox"
  default_timeout_seconds: 30
  max_concurrent_tasks: 4
  cgroup_base_path: "/sys/fs/cgroup/opsagent"
  audit_log_path: "/var/log/opsagent/audit.log"
  policy:
    allowed_commands: ["cat", "grep", "df", "free", "uptime", "ls", "wc", "tail", "head"]
    blocked_commands: ["rm -rf /", "dd if=", "mkfs", "shutdown", "reboot"]
    blocked_keywords: ["eval(", "exec(", "|bash", "|sh"]
    allowed_interpreters: ["/bin/bash", "/bin/sh", "/usr/bin/python3"]
    script_max_bytes: 65536
    shell_injection_check: true
collector:
  inputs:
    - type: "cpu"
      config:
        totalcpu: true
    - type: "memory"
      config: {}
    - type: "disk"
      config:
        mount_points: ["/"]
    - type: "net"
      config: {}
    - type: "process"
      config:
        top_n: 10
  processors:
    - type: "tagger"
      config:
        tags:
          env: "production"
  aggregators: []
  outputs:
    - type: "http"
      config:
        url: "https://platform.example.com/api/v1/metrics"
        timeout: 5
        batch_size: 500
`)
	if err := os.WriteFile(cfgPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// GRPC
	if cfg.GRPC.ServerAddr != "control.example.com:8443" {
		t.Fatalf("expected grpc server_addr, got %s", cfg.GRPC.ServerAddr)
	}
	if cfg.GRPC.EnrollToken != "tok-abc123" {
		t.Fatalf("expected grpc enroll_token, got %s", cfg.GRPC.EnrollToken)
	}
	if cfg.GRPC.MTLS.CertFile != "/etc/opsagent/certs/client.crt" {
		t.Fatalf("expected mtls cert_file")
	}
	if cfg.GRPC.HeartbeatIntervalSeconds != 15 {
		t.Fatalf("expected heartbeat 15, got %d", cfg.GRPC.HeartbeatIntervalSeconds)
	}

	// Sandbox
	if !cfg.Sandbox.Enabled {
		t.Fatal("expected sandbox enabled")
	}
	if cfg.Sandbox.NsjailPath != "/usr/bin/nsjail" {
		t.Fatalf("expected nsjail path, got %s", cfg.Sandbox.NsjailPath)
	}
	if cfg.Sandbox.MaxConcurrentTasks != 4 {
		t.Fatalf("expected max_concurrent_tasks=4, got %d", cfg.Sandbox.MaxConcurrentTasks)
	}
	if !cfg.Sandbox.Policy.ShellInjectionCheck {
		t.Fatal("expected shell_injection_check=true")
	}

	// Collector
	if len(cfg.Collector.Inputs) != 5 {
		t.Fatalf("expected 5 inputs, got %d", len(cfg.Collector.Inputs))
	}
	if cfg.Collector.Inputs[0].Type != "cpu" {
		t.Fatalf("expected first input type=cpu, got %s", cfg.Collector.Inputs[0].Type)
	}
	if len(cfg.Collector.Outputs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(cfg.Collector.Outputs))
	}
}

func TestValidateGRPCServerAddrRequired(t *testing.T) {
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
		GRPC:       GRPCConfig{ServerAddr: ""},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected grpc.server_addr validation error")
	}
}

func TestValidateSandboxNsjailPathRequired(t *testing.T) {
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
		GRPC:       GRPCConfig{ServerAddr: "control:8443"},
		Sandbox:    SandboxConfig{Enabled: true, NsjailPath: ""},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected sandbox.nsjail_path validation error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -v -run TestLoadGRPCSandboxCollectorConfig
```

Expected: FAIL — `GRPCConfig`, `SandboxConfig`, `CollectorConfig` types don't exist.

- [ ] **Step 3: Add new config structs and extend Validate**

Add to `internal/config/config.go` after the existing `Config` struct fields:

```go
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
```

Add the new struct definitions after `PluginConfig`:

```go
// GRPCConfig controls gRPC client connection to the platform.
type GRPCConfig struct {
	ServerAddr                string   `mapstructure:"server_addr"`
	EnrollToken               string   `mapstructure:"enroll_token"`
	MTLS                      MTLSConfig `mapstructure:"mtls"`
	HeartbeatIntervalSeconds  int      `mapstructure:"heartbeat_interval_seconds"`
	ReconnectInitialBackoffMS int      `mapstructure:"reconnect_initial_backoff_ms"`
	ReconnectMaxBackoffMS     int      `mapstructure:"reconnect_max_backoff_ms"`
}

// MTLSConfig holds mutual TLS certificate paths.
type MTLSConfig struct {
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	CAFile   string `mapstructure:"ca_file"`
}

// SandboxConfig controls nsjail-based sandbox execution.
type SandboxConfig struct {
	Enabled               bool           `mapstructure:"enabled"`
	NsjailPath            string         `mapstructure:"nsjail_path"`
	BaseWorkdir           string         `mapstructure:"base_workdir"`
	DefaultTimeoutSeconds int            `mapstructure:"default_timeout_seconds"`
	MaxConcurrentTasks    int            `mapstructure:"max_concurrent_tasks"`
	CgroupBasePath        string         `mapstructure:"cgroup_base_path"`
	AuditLogPath          string         `mapstructure:"audit_log_path"`
	Policy                PolicyConfig   `mapstructure:"policy"`
}

// PolicyConfig defines sandbox security policy.
type PolicyConfig struct {
	AllowedCommands     []string `mapstructure:"allowed_commands"`
	BlockedCommands     []string `mapstructure:"blocked_commands"`
	BlockedKeywords     []string `mapstructure:"blocked_keywords"`
	AllowedInterpreters []string `mapstructure:"allowed_interpreters"`
	ScriptMaxBytes      int      `mapstructure:"script_max_bytes"`
	ShellInjectionCheck bool     `mapstructure:"shell_injection_check"`
}

// CollectorConfig defines the metrics collection pipeline.
type CollectorConfig struct {
	Inputs      []PluginInstanceConfig `mapstructure:"inputs"`
	Processors  []PluginInstanceConfig `mapstructure:"processors"`
	Aggregators []PluginInstanceConfig `mapstructure:"aggregators"`
	Outputs     []PluginInstanceConfig `mapstructure:"outputs"`
}

// PluginInstanceConfig is a single plugin instance with type and config map.
type PluginInstanceConfig struct {
	Type   string                 `mapstructure:"type"`
	Config map[string]interface{} `mapstructure:"config"`
}
```

Add defaults in `Load()`:

```go
v.SetDefault("grpc.heartbeat_interval_seconds", 15)
v.SetDefault("grpc.reconnect_initial_backoff_ms", 1000)
v.SetDefault("grpc.reconnect_max_backoff_ms", 30000)
v.SetDefault("sandbox.enabled", false)
v.SetDefault("sandbox.default_timeout_seconds", 30)
v.SetDefault("sandbox.max_concurrent_tasks", 4)
v.SetDefault("sandbox.policy.shell_injection_check", true)
v.SetDefault("sandbox.policy.script_max_bytes", 65536)
```

Add validation in `Validate()`:

```go
// GRPC validation
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

// Sandbox validation
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
		return fmt.Errorf("sandbox.policy.allowed_commands must not be empty")
	}
	if c.Sandbox.Policy.ScriptMaxBytes <= 0 {
		return fmt.Errorf("sandbox.policy.script_max_bytes must be > 0")
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add GRPC, Sandbox, and Collector config structs with validation"
```

---

## Task 8.2: Update config.yaml

- [ ] **Step 1: Add new sections to configs/config.yaml**

Append to `configs/config.yaml`:

```yaml
grpc:
  server_addr: "control.example.com:8443"
  enroll_token: ""
  mtls:
    cert_file: "/etc/opsagent/certs/client.crt"
    key_file: "/etc/opsagent/certs/client.key"
    ca_file: "/etc/opsagent/certs/ca.crt"
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000

sandbox:
  enabled: false
  nsjail_path: "/usr/bin/nsjail"
  base_workdir: "/tmp/opsagent/sandbox"
  default_timeout_seconds: 30
  max_concurrent_tasks: 4
  cgroup_base_path: "/sys/fs/cgroup/opsagent"
  audit_log_path: "/var/log/opsagent/audit.log"
  policy:
    allowed_commands:
      - cat
      - grep
      - df
      - free
      - uptime
      - ls
      - wc
      - tail
      - head
    blocked_commands:
      - "rm -rf /"
      - "dd if="
      - mkfs
      - shutdown
      - reboot
    blocked_keywords:
      - "eval("
      - "exec("
      - "|bash"
      - "|sh"
    allowed_interpreters:
      - /bin/bash
      - /bin/sh
      - /usr/bin/python3
    script_max_bytes: 65536
    shell_injection_check: true

collector:
  inputs:
    - type: "cpu"
      config:
        totalcpu: true
    - type: "memory"
      config: {}
    - type: "disk"
      config:
        mount_points: ["/"]
    - type: "net"
      config: {}
    - type: "process"
      config:
        top_n: 10
  processors:
    - type: "tagger"
      config:
        tags:
          env: "production"
  aggregators: []
  outputs:
    - type: "http"
      config:
        url: "https://platform.example.com/api/v1/metrics"
        timeout: 5
        batch_size: 500
```

- [ ] **Step 2: Verify config loads without error**

```bash
go run ./cmd/agent run --config ./configs/config.yaml &
sleep 2 && kill %1
```

Expected: Agent starts (will fail on gRPC connect if server not running, but config should parse).

- [ ] **Step 3: Commit**

```bash
git add configs/config.yaml
git commit -m "feat(config): add grpc, sandbox, and collector sections to default config"
```

---

## Task 8.3: Wire Collector Pipeline into Agent

- [ ] **Step 1: Write failing test for pipeline wiring**

Add to a new file `internal/app/agent_test.go`:

```go
package app

import (
	"testing"

	"opsagent/internal/config"
)

func TestNewAgentWiresCollectorPipeline(t *testing.T) {
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test-agent", IntervalSeconds: 10},
		Server: config.ServerConfig{ListenAddr: "127.0.0.1:0"},
		Executor: config.ExecutorConfig{
			TimeoutSeconds:  5,
			AllowedCommands: []string{"echo"},
			MaxOutputBytes:  1024,
		},
		Reporter:   config.ReporterConfig{Mode: "stdout", TimeoutSeconds: 5, RetryCount: 1, RetryIntervalMS: 10},
		Prometheus: config.PrometheusConfig{Enabled: false},
		GRPC:       config.GRPCConfig{ServerAddr: "localhost:8443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
		Sandbox:    config.SandboxConfig{Enabled: false},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{"totalcpu": true}},
			},
			Outputs: []config.PluginInstanceConfig{
				{Type: "http", Config: map[string]interface{}{"url": "http://localhost:9999/metrics", "timeout": 1}},
			},
		},
	}

	log := logger.New("info")
	agent, err := NewAgent(cfg, log)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Verify pipeline components are wired
	if agent.pipeline == nil {
		t.Fatal("expected pipeline to be initialized")
	}
	if agent.grpcClient == nil {
		t.Fatal("expected grpc client to be initialized")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/app/ -v -run TestNewAgentWiresCollectorPipeline
```

Expected: FAIL — `pipeline` and `grpcClient` fields don't exist on `Agent`.

- [ ] **Step 3: Modify Agent struct and NewAgent to wire pipeline**

Modify `internal/app/agent.go`. Add imports:

```go
import (
	// ... existing imports ...
	"opsagent/internal/collector/aggregators/avg"
	"opsagent/internal/collector/aggregators/sum"
	"opsagent/internal/collector/inputs/cpu"
	"opsagent/internal/collector/inputs/disk"
	"opsagent/internal/collector/inputs/memory"
	"opsagent/internal/collector/inputs/net"
	"opsagent/internal/collector/inputs/process"
	"opsagent/internal/collector/outputs/http"
	"opsagent/internal/collector/outputs/prometheus"
	"opsagent/internal/collector/outputs/promrw"
	"opsagent/internal/collector/processors/regex"
	"opsagent/internal/collector/processors/tagger"
	"opsagent/internal/grpcclient"
	"opsagent/internal/sandbox"
)
```

Note: These blank imports trigger `init()` registration. For plugins, use blank imports to trigger registration:

```go
// Blank imports to trigger plugin init() registration
_ "opsagent/internal/collector/inputs/cpu"
_ "opsagent/internal/collector/inputs/memory"
_ "opsagent/internal/collector/inputs/disk"
_ "opsagent/internal/collector/inputs/net"
_ "opsagent/internal/collector/inputs/process"
_ "opsagent/internal/collector/outputs/http"
_ "opsagent/internal/collector/outputs/prometheus"
_ "opsagent/internal/collector/outputs/promrw"
_ "opsagent/internal/collector/processors/regex"
_ "opsagent/internal/collector/processors/tagger"
_ "opsagent/internal/collector/aggregators/avg"
_ "opsagent/internal/collector/aggregators/sum"
```

Update `Agent` struct:

```go
type Agent struct {
	cfg           *config.Config
	log           zerolog.Logger
	manager       *collector.Manager  // legacy, keep for backward compat
	reporter      reporter.Reporter
	server        *server.Server
	executor      *executor.Executor
	pluginRuntime *pluginruntime.Runtime
	pipeline      *collector.Pipeline  // new collector pipeline
	grpcClient    *grpcclient.Client   // new gRPC client
	sandboxExec   *sandbox.Executor    // new sandbox executor
	startedAt     time.Time
}
```

In `NewAgent()`, after existing setup, add pipeline wiring:

```go
// Build collector pipeline from config
pipeline, err := buildPipeline(cfg.Collector, log)
if err != nil {
	return nil, fmt.Errorf("build collector pipeline: %w", err)
}

// Build gRPC client
grpcClient := grpcclient.New(grpcclient.Config{
	ServerAddr:                cfg.GRPC.ServerAddr,
	EnrollToken:               cfg.GRPC.EnrollToken,
	CertFile:                  cfg.GRPC.MTLS.CertFile,
	KeyFile:                   cfg.GRPC.MTLS.KeyFile,
	CAFile:                    cfg.GRPC.MTLS.CAFile,
	HeartbeatInterval:         time.Duration(cfg.GRPC.HeartbeatIntervalSeconds) * time.Second,
	ReconnectInitialBackoff:   time.Duration(cfg.GRPC.ReconnectInitialBackoffMS) * time.Millisecond,
	ReconnectMaxBackoff:       time.Duration(cfg.GRPC.ReconnectMaxBackoffMS) * time.Millisecond,
	AgentID:                   cfg.Agent.ID,
	AgentName:                 cfg.Agent.Name,
}, log)

// Build sandbox executor
var sandboxExec *sandbox.Executor
if cfg.Sandbox.Enabled {
	sandboxExec, err = sandbox.New(sandbox.Config{
		NsjailPath:         cfg.Sandbox.NsjailPath,
		BaseWorkdir:        cfg.Sandbox.BaseWorkdir,
		DefaultTimeout:     time.Duration(cfg.Sandbox.DefaultTimeoutSeconds) * time.Second,
		MaxConcurrentTasks: cfg.Sandbox.MaxConcurrentTasks,
		CgroupBasePath:     cfg.Sandbox.CgroupBasePath,
		AuditLogPath:       cfg.Sandbox.AuditLogPath,
	}, sandbox.PolicyConfig{
		AllowedCommands:     cfg.Sandbox.Policy.AllowedCommands,
		BlockedCommands:     cfg.Sandbox.Policy.BlockedCommands,
		BlockedKeywords:     cfg.Sandbox.Policy.BlockedKeywords,
		AllowedInterpreters: cfg.Sandbox.Policy.AllowedInterpreters,
		ScriptMaxBytes:      cfg.Sandbox.Policy.ScriptMaxBytes,
		ShellInjectionCheck: cfg.Sandbox.Policy.ShellInjectionCheck,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("create sandbox executor: %w", err)
	}
}
```

Add `buildPipeline` helper function:

```go
func buildPipeline(cfg config.CollectorConfig, log zerolog.Logger) (*collector.Pipeline, error) {
	inputs, err := instantiateInputs(cfg.Inputs)
	if err != nil {
		return nil, fmt.Errorf("inputs: %w", err)
	}
	processors, err := instantiateProcessors(cfg.Processors)
	if err != nil {
		return nil, fmt.Errorf("processors: %w", err)
	}
	aggregators, err := instantiateAggregators(cfg.Aggregators)
	if err != nil {
		return nil, fmt.Errorf("aggregators: %w", err)
	}
	outputs, err := instantiateOutputs(cfg.Outputs)
	if err != nil {
		return nil, fmt.Errorf("outputs: %w", err)
	}

	return collector.NewPipeline(collector.PipelineConfig{
		Inputs:      inputs,
		Processors:  processors,
		Aggregators: aggregators,
		Outputs:     outputs,
		Log:         log,
	})
}

func instantiateInputs(cfgs []config.PluginInstanceConfig) ([]collector.Input, error) {
	var inputs []collector.Input
	for _, c := range cfgs {
		factory, ok := collector.DefaultRegistry.GetInput(c.Type)
		if !ok {
			return nil, fmt.Errorf("unknown input type: %s", c.Type)
		}
		input := factory()
		if err := input.Init(c.Config); err != nil {
			return nil, fmt.Errorf("init input %s: %w", c.Type, err)
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func instantiateProcessors(cfgs []config.PluginInstanceConfig) ([]collector.Processor, error) {
	var processors []collector.Processor
	for _, c := range cfgs {
		factory, ok := collector.DefaultRegistry.GetProcessor(c.Type)
		if !ok {
			return nil, fmt.Errorf("unknown processor type: %s", c.Type)
		}
		proc := factory()
		if err := proc.Init(c.Config); err != nil {
			return nil, fmt.Errorf("init processor %s: %w", c.Type, err)
		}
		processors = append(processors, proc)
	}
	return processors, nil
}

func instantiateAggregators(cfgs []config.PluginInstanceConfig) ([]collector.Aggregator, error) {
	var aggregators []collector.Aggregator
	for _, c := range cfgs {
		factory, ok := collector.DefaultRegistry.GetAggregator(c.Type)
		if !ok {
			return nil, fmt.Errorf("unknown aggregator type: %s", c.Type)
		}
		agg := factory()
		if err := agg.Init(c.Config); err != nil {
			return nil, fmt.Errorf("init aggregator %s: %w", c.Type, err)
		}
		aggregators = append(aggregators, agg)
	}
	return aggregators, nil
}

func instantiateOutputs(cfgs []config.PluginInstanceConfig) ([]collector.Output, error) {
	var outputs []collector.Output
	for _, c := range cfgs {
		factory, ok := collector.DefaultRegistry.GetOutput(c.Type)
		if !ok {
			return nil, fmt.Errorf("unknown output type: %s", c.Type)
		}
		out := factory()
		if err := out.Init(c.Config); err != nil {
			return nil, fmt.Errorf("init output %s: %w", c.Type, err)
		}
		outputs = append(outputs, out)
	}
	return outputs, nil
}
```

Update `NewAgent` struct initialization:

```go
a := &Agent{
	cfg:           cfg,
	log:           log,
	manager:       manager,
	reporter:      rep,
	server:        srv,
	executor:      exec,
	pluginRuntime: pr,
	pipeline:      pipeline,
	grpcClient:    grpcClient,
	sandboxExec:   sandboxExec,
	startedAt:     startedAt,
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/app/ -v -run TestNewAgentWiresCollectorPipeline
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/
git commit -m "feat(app): wire collector pipeline, gRPC client, and sandbox executor into agent lifecycle"
```

---

## Task 8.4: Wire gRPC Client Message Handlers

- [ ] **Step 1: Register sandbox task handler with gRPC receiver**

Modify `internal/app/agent.go` `Run()` method. After starting the HTTP server, add gRPC client and pipeline startup:

```go
// Register gRPC message handlers
a.grpcClient.OnPlatformMessage(func(msg *pb.PlatformMessage) {
	switch m := msg.Message.(type) {
	case *pb.PlatformMessage_ExecuteTask:
		a.handleSandboxTask(m.ExecuteTask)
	case *pb.PlatformMessage_CollectMetrics:
		a.handleCollectRequest()
	case *pb.PlatformMessage_ConfigUpdate:
		a.log.Info().Msg("received config update (not yet implemented)")
	}
})

// Start gRPC client (non-blocking reconnect loop)
if err := a.grpcClient.Start(ctx); err != nil {
	return fmt.Errorf("start grpc client: %w", err)
}
defer a.grpcClient.Stop()

// Start collector pipeline
if err := a.pipeline.Start(ctx); err != nil {
	return fmt.Errorf("start collector pipeline: %w", err)
}
defer a.pipeline.Stop()

// Register pipeline output that forwards to gRPC sender
a.pipeline.OnMetrics(func(metrics []collector.Metric) {
	if err := a.grpcClient.SendMetrics(metrics); err != nil {
		a.log.Error().Err(err).Msg("failed to send metrics via gRPC")
	}
})
```

Add handler methods:

```go
func (a *Agent) handleSandboxTask(task *pb.ExecuteTaskRequest) {
	if a.sandboxExec == nil {
		a.log.Warn().Str("task_id", task.TaskId).Msg("sandbox not enabled, rejecting task")
		// Send error response via gRPC
		a.grpcClient.SendTaskResult(task.TaskId, pb.TaskStatus_TASK_STATUS_FAILED, nil, "sandbox not enabled")
		return
	}

	go func() {
		result, err := a.sandboxExec.Execute(context.Background(), sandbox.Request{
			TaskID:          task.TaskId,
			Command:         task.GetCommand().GetRaw(),
			Script:          task.GetScript().GetContent(),
			Interpreter:     task.GetScript().GetInterpreter(),
			TimeoutSeconds:  int(task.TimeoutSeconds),
			WorkingDir:      task.WorkingDirectory,
			EnvVars:         task.EnvVars,
			ResourceLimits:  convertResourceLimits(task.ResourceLimits),
		})

		if err != nil {
			a.log.Error().Err(err).Str("task_id", task.TaskId).Msg("sandbox execution failed")
			a.grpcClient.SendTaskResult(task.TaskId, pb.TaskStatus_TASK_STATUS_FAILED, nil, err.Error())
			return
		}

		a.grpcClient.SendTaskResult(task.TaskId, pb.TaskStatus_TASK_STATUS_SUCCESS, result, "")
	}()
}

func (a *Agent) handleCollectRequest() {
	acc := collector.NewAccumulator(1000)
	metrics, err := a.pipeline.CollectOnce(context.Background(), acc)
	if err != nil {
		a.log.Error().Err(err).Msg("on-demand collect failed")
		return
	}
	if err := a.grpcClient.SendMetrics(metrics); err != nil {
		a.log.Error().Err(err).Msg("failed to send on-demand metrics")
	}
}
```

- [ ] **Step 2: Run go vet to check compilation**

```bash
go vet ./internal/app/
```

Expected: PASS (may have unresolved references until all sub-plans are implemented, but structurally correct).

- [ ] **Step 3: Commit**

```bash
git add internal/app/
git commit -m "feat(app): wire gRPC message handlers for sandbox tasks and metric collection"
```

---

## Task 8.5: Update Agent.Run() Lifecycle

- [ ] **Step 1: Modify Run() to integrate new subsystems**

Replace the existing `Run()` method body to incorporate the new pipeline alongside legacy collection:

```go
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info().Str("agent_id", a.cfg.Agent.ID).Str("listen_addr", a.cfg.Server.ListenAddr).Msg("agent starting")

	// Start plugin runtime (existing)
	if err := a.pluginRuntime.Start(ctx); err != nil {
		return fmt.Errorf("start plugin runtime: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.pluginRuntime.Stop(stopCtx); err != nil {
			a.log.Error().Err(err).Msg("failed to stop plugin runtime")
		}
	}()

	// Start HTTP server (existing)
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.Start()
	}()

	// Start gRPC client (new)
	if err := a.grpcClient.Start(ctx); err != nil {
		return fmt.Errorf("start grpc client: %w", err)
	}
	defer a.grpcClient.Stop()

	// Start collector pipeline (new)
	if err := a.pipeline.Start(ctx); err != nil {
		return fmt.Errorf("start collector pipeline: %w", err)
	}
	defer a.pipeline.Stop()

	// Forward pipeline metrics to gRPC and server
	a.pipeline.OnMetrics(func(metrics []collector.Metric) {
		a.server.SetLatestMetric(convertToLegacyPayload(metrics, a.cfg.Agent.ID, a.cfg.Agent.Name))
		if err := a.grpcClient.SendMetrics(metrics); err != nil {
			a.log.Error().Err(err).Msg("grpc send metrics failed")
		}
	})

	// Legacy collection fallback if pipeline has no outputs
	if !a.pipeline.HasOutputs() {
		if err := a.collectAndReport(ctx); err != nil {
			a.log.Error().Err(err).Msg("initial collect failed")
		}

		ticker := time.NewTicker(time.Duration(a.cfg.Agent.IntervalSeconds) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := a.server.Shutdown(shutdownCtx); err != nil {
					return fmt.Errorf("shutdown server: %w", err)
				}
				return nil
			case err := <-errCh:
				if err != nil {
					return fmt.Errorf("http server stopped: %w", err)
				}
				return nil
			case <-ticker.C:
				if err := a.collectAndReport(ctx); err != nil {
					a.log.Error().Err(err).Msg("collect loop failed")
				}
			}
		}
	}

	// Pipeline mode: just wait for shutdown
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server stopped: %w", err)
		}
		return nil
	}
}
```

Add helper to convert new metrics to legacy payload for backward compat:

```go
func convertToLegacyPayload(metrics []collector.Metric, agentID, agentName string) *collector.MetricPayload {
	payload := &collector.MetricPayload{
		AgentID:     agentID,
		AgentName:   agentName,
		Collector:   "pipeline",
		CollectedAt: time.Now().UTC(),
	}

	for _, m := range metrics {
		switch m.Name() {
		case "cpu":
			if v, ok := m.Fields()["usage_percent"].(float64); ok {
				payload.CPUUsagePercent = v
			}
		case "memory":
			if v, ok := m.Fields()["used_percent"].(float64); ok {
				payload.MemoryUsagePercent = v
			}
		case "disk":
			if v, ok := m.Fields()["used_percent"].(float64); ok {
				payload.DiskUsagePercent = v
			}
		case "net":
			if v, ok := m.Fields()["bytes_sent"].(int64); ok {
				payload.NetworkIO.BytesSent = uint64(v)
			}
			if v, ok := m.Fields()["bytes_recv"].(int64); ok {
				payload.NetworkIO.BytesRecv = uint64(v)
			}
		}
	}

	return payload
}
```

- [ ] **Step 2: Run go vet**

```bash
go vet ./internal/app/
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/app/
git commit -m "feat(app): integrate pipeline and gRPC client into agent run lifecycle"
```

---

## Task 8.6: Register gRPC Task Handlers

- [ ] **Step 1: Register sandbox and metric collection handlers**

In `registerTaskHandlers`, add new task types for gRPC-driven operations. This is additive — existing handlers remain untouched.

Add to `internal/app/agent.go`:

```go
// Register gRPC-driven sandbox execution handler
dispatcher.Register("sandbox_exec", func(ctx context.Context, t task.AgentTask) (any, error) {
	if a.sandboxExec == nil {
		return nil, fmt.Errorf("sandbox not enabled")
	}

	cmd, _ := t.Payload["command"].(string)
	script, _ := t.Payload["script"].(string)
	interpreter, _ := t.Payload["interpreter"].(string)
	timeoutSec := 30
	if v, ok := t.Payload["timeout_seconds"].(float64); ok {
		timeoutSec = int(v)
	}
	workDir, _ := t.Payload["working_directory"].(string)

	envVars := make(map[string]string)
	if raw, ok := t.Payload["env_vars"].(map[string]interface{}); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok {
				envVars[k] = s
			}
		}
	}

	return a.sandboxExec.Execute(ctx, sandbox.Request{
		TaskID:         t.TaskID,
		Command:        cmd,
		Script:         script,
		Interpreter:    interpreter,
		TimeoutSeconds: timeoutSec,
		WorkingDir:     workDir,
		EnvVars:        envVars,
	})
})
```

- [ ] **Step 2: Run go vet**

```bash
go vet ./internal/app/
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/app/
git commit -m "feat(app): register sandbox_exec task handler for gRPC-driven execution"
```
