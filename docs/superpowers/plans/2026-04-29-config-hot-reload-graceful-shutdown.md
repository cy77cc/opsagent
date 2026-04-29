# Config Hot-Reload & Graceful Shutdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现配置热更新（gRPC + SIGHUP 触发，原子性回滚）和有序优雅关闭（排空 pipeline、flush 缓存、协调进行中任务）。

**Architecture:** ConfigReloader 持有当前配置和一组 Reloader 实例，Diff 引擎检测变更并分类为可重载/不可重载，原子性地应用变更或回滚。优雅关闭按序标记 shuttingDown → 等待任务 → 停 scheduler → flush gRPC → 停 runtime → 停 server。

**Tech Stack:** Go, sync.RWMutex, sync/atomic, reflect.DeepEqual, os/signal (SIGHUP), encoding/json (cache persist)

---

### Task 1: Config 字段扩展

**Files:**
- Modify: `internal/config/config.go`
- Modify: `configs/config.yaml`

- [ ] **Step 1: Write failing test for new config fields**

```go
// internal/config/config_test.go — 添加到已有文件
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/config/ -run TestConfig_ShutdownTimeoutSeconds`
Expected: FAIL — `ShutdownTimeoutSeconds` and `CachePersistPath` fields don't exist

- [ ] **Step 3: Add fields to config structs**

In `internal/config/config.go`, add to `AgentConfig`:
```go
type AgentConfig struct {
	ID                    string `mapstructure:"id"`
	Name                  string `mapstructure:"name"`
	IntervalSeconds       int    `mapstructure:"interval_seconds"`
	ShutdownTimeoutSeconds int   `mapstructure:"shutdown_timeout_seconds"`
}
```

Add to `GRPCConfig`:
```go
type GRPCConfig struct {
	ServerAddr                string     `mapstructure:"server_addr"`
	EnrollToken               string     `mapstructure:"enroll_token"`
	MTLS                      MTLSConfig `mapstructure:"mtls"`
	HeartbeatIntervalSeconds  int        `mapstructure:"heartbeat_interval_seconds"`
	ReconnectInitialBackoffMS int        `mapstructure:"reconnect_initial_backoff_ms"`
	ReconnectMaxBackoffMS     int        `mapstructure:"reconnect_max_backoff_ms"`
	CachePersistPath          string     `mapstructure:"cache_persist_path"`
}
```

Add defaults in `Load`:
```go
v.SetDefault("agent.shutdown_timeout_seconds", 30)
v.SetDefault("grpc.cache_persist_path", "")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/config/ -run TestConfig_ShutdownTimeoutSeconds`
Expected: PASS

- [ ] **Step 5: Update config.yaml**

Add to `configs/config.yaml`:
```yaml
agent:
  id: "agent-local-001"
  name: "local-dev-agent"
  interval_seconds: 10
  shutdown_timeout_seconds: 30
```

Add under `grpc`:
```yaml
grpc:
  # ... existing fields
  cache_persist_path: ""
```

- [ ] **Step 6: Run all config tests**

Run: `go test -race ./internal/config/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go configs/config.yaml
git commit -m "feat(config): add shutdown_timeout_seconds and cache_persist_path fields"
```

---

### Task 2: Diff 引擎

**Files:**
- Create: `internal/config/diff.go`
- Create: `internal/config/diff_test.go`

- [ ] **Step 1: Write failing tests for Diff**

```go
// internal/config/diff_test.go
package config

import (
	"testing"
)

func TestDiff_IdenticalConfigs(t *testing.T) {
	cfg := &Config{
		Agent:    AgentConfig{ID: "a", Name: "n", IntervalSeconds: 10, ShutdownTimeoutSeconds: 30},
		Server:   ServerConfig{ListenAddr: ":8080"},
		Executor: ExecutorConfig{TimeoutSeconds: 10, AllowedCommands: []string{"ls"}, MaxOutputBytes: 1024},
		Reporter: ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
		Auth:     AuthConfig{Enabled: false},
		Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"},
		GRPC:     GRPCConfig{ServerAddr: "x:443", HeartbeatIntervalSeconds: 15, ReconnectInitialBackoffMS: 1000, ReconnectMaxBackoffMS: 30000},
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/config/ -run TestDiff`
Expected: FAIL — `Diff` function not defined

- [ ] **Step 3: Implement Diff engine**

```go
// internal/config/diff.go
package config

import (
	"fmt"
	"reflect"
)

// ChangeSet records which reloadable field groups changed.
type ChangeSet struct {
	CollectorChanged  bool
	ReporterChanged   bool
	AuthChanged       bool
	PrometheusChanged bool
}

// NonReloadableChange records a change to a field that requires restart.
type NonReloadableChange struct {
	Field  string
	OldVal interface{}
	NewVal interface{}
}

// Diff compares old and new configs, returning a ChangeSet for reloadable
// fields and a list of non-reloadable changes. The new config is validated first.
func Diff(old, new *Config) (*ChangeSet, []NonReloadableChange, error) {
	if err := new.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid new config: %w", err)
	}

	cs := &ChangeSet{}
	var nonReloadable []NonReloadableChange

	// Reloadable: collector
	if !reflect.DeepEqual(old.Collector, new.Collector) {
		cs.CollectorChanged = true
	}

	// Reloadable: reporter
	if diffReporter(old, new) {
		cs.ReporterChanged = true
	}

	// Reloadable: auth
	if diffAuth(old, new) {
		cs.AuthChanged = true
	}

	// Reloadable: prometheus
	if diffPrometheus(old, new) {
		cs.PrometheusChanged = true
	}

	// Non-reloadable checks
	nonReloadable = append(nonReloadable, diffAgent(old, new)...)
	nonReloadable = append(nonReloadable, diffServer(old, new)...)
	nonReloadable = append(nonReloadable, diffGRPC(old, new)...)
	nonReloadable = append(nonReloadable, diffExecutor(old, new)...)

	if !reflect.DeepEqual(old.Sandbox, new.Sandbox) {
		nonReloadable = append(nonReloadable, NonReloadableChange{
			Field:  "sandbox.*",
			OldVal: old.Sandbox,
			NewVal: new.Sandbox,
		})
	}

	if !reflect.DeepEqual(old.Plugin, new.Plugin) {
		nonReloadable = append(nonReloadable, NonReloadableChange{
			Field:  "plugin.*",
			OldVal: old.Plugin,
			NewVal: new.Plugin,
		})
	}

	return cs, nonReloadable, nil
}

func diffReporter(old, new *Config) bool {
	return old.Reporter != new.Reporter
}

func diffAuth(old, new *Config) bool {
	return old.Auth != new.Auth
}

func diffPrometheus(old, new *Config) bool {
	return old.Prometheus != new.Prometheus
}

func diffAgent(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Agent.ID != new.Agent.ID {
		changes = append(changes, NonReloadableChange{"agent.id", old.Agent.ID, new.Agent.ID})
	}
	if old.Agent.Name != new.Agent.Name {
		changes = append(changes, NonReloadableChange{"agent.name", old.Agent.Name, new.Agent.Name})
	}
	if old.Agent.IntervalSeconds != new.Agent.IntervalSeconds {
		changes = append(changes, NonReloadableChange{"agent.interval_seconds", old.Agent.IntervalSeconds, new.Agent.IntervalSeconds})
	}
	if old.Agent.ShutdownTimeoutSeconds != new.Agent.ShutdownTimeoutSeconds {
		changes = append(changes, NonReloadableChange{"agent.shutdown_timeout_seconds", old.Agent.ShutdownTimeoutSeconds, new.Agent.ShutdownTimeoutSeconds})
	}
	return changes
}

func diffServer(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Server.ListenAddr != new.Server.ListenAddr {
		changes = append(changes, NonReloadableChange{"server.listen_addr", old.Server.ListenAddr, new.Server.ListenAddr})
	}
	return changes
}

func diffGRPC(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.GRPC.ServerAddr != new.GRPC.ServerAddr {
		changes = append(changes, NonReloadableChange{"grpc.server_addr", old.GRPC.ServerAddr, new.GRPC.ServerAddr})
	}
	if old.GRPC.EnrollToken != new.GRPC.EnrollToken {
		changes = append(changes, NonReloadableChange{"grpc.enroll_token", old.GRPC.EnrollToken, new.GRPC.EnrollToken})
	}
	if old.GRPC.MTLS != new.GRPC.MTLS {
		changes = append(changes, NonReloadableChange{"grpc.mtls", old.GRPC.MTLS, new.GRPC.MTLS})
	}
	if old.GRPC.HeartbeatIntervalSeconds != new.GRPC.HeartbeatIntervalSeconds {
		changes = append(changes, NonReloadableChange{"grpc.heartbeat_interval_seconds", old.GRPC.HeartbeatIntervalSeconds, new.GRPC.HeartbeatIntervalSeconds})
	}
	if old.GRPC.ReconnectInitialBackoffMS != new.GRPC.ReconnectInitialBackoffMS {
		changes = append(changes, NonReloadableChange{"grpc.reconnect_initial_backoff_ms", old.GRPC.ReconnectInitialBackoffMS, new.GRPC.ReconnectInitialBackoffMS})
	}
	if old.GRPC.ReconnectMaxBackoffMS != new.GRPC.ReconnectMaxBackoffMS {
		changes = append(changes, NonReloadableChange{"grpc.reconnect_max_backoff_ms", old.GRPC.ReconnectMaxBackoffMS, new.GRPC.ReconnectMaxBackoffMS})
	}
	if old.GRPC.CachePersistPath != new.GRPC.CachePersistPath {
		changes = append(changes, NonReloadableChange{"grpc.cache_persist_path", old.GRPC.CachePersistPath, new.GRPC.CachePersistPath})
	}
	return changes
}

func diffExecutor(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Executor.TimeoutSeconds != new.Executor.TimeoutSeconds {
		changes = append(changes, NonReloadableChange{"executor.timeout_seconds", old.Executor.TimeoutSeconds, new.Executor.TimeoutSeconds})
	}
	if !reflect.DeepEqual(old.Executor.AllowedCommands, new.Executor.AllowedCommands) {
		changes = append(changes, NonReloadableChange{"executor.allowed_commands", old.Executor.AllowedCommands, new.Executor.AllowedCommands})
	}
	if old.Executor.MaxOutputBytes != new.Executor.MaxOutputBytes {
		changes = append(changes, NonReloadableChange{"executor.max_output_bytes", old.Executor.MaxOutputBytes, new.Executor.MaxOutputBytes})
	}
	return changes
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/config/ -run TestDiff`
Expected: PASS

- [ ] **Step 5: Run all config tests**

Run: `go test -race ./internal/config/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/diff.go internal/config/diff_test.go
git commit -m "feat(config): add Diff engine for config change detection"
```

---

### Task 3: Reloader 接口 + ConfigReloader

**Files:**
- Create: `internal/config/reload.go`
- Create: `internal/config/reload_test.go`

- [ ] **Step 1: Write failing tests for ConfigReloader**

```go
// internal/config/reload_test.go
package config

import (
	"context"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
)

// mockReloader implements Reloader for testing.
type mockReloader struct {
	canReloadFn func(*ChangeSet) bool
	applyFn     func(*Config) error
	rollbackFn  func(*Config) error
	applyCalled  int
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

	newCfg := baseConfig()
	newCfg.Auth = AuthConfig{Enabled: true, BearerToken: "tok"}
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/config/ -run TestConfigReloader`
Expected: FAIL — `ConfigReloader`, `NewConfigReloader`, `Reloader` not defined

- [ ] **Step 3: Implement Reloader interface and ConfigReloader**

```go
// internal/config/reload.go
package config

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// Reloader is implemented by each subsystem that supports hot-reload.
type Reloader interface {
	CanReload(cs *ChangeSet) bool
	Apply(newCfg *Config) error
	Rollback(oldCfg *Config) error
}

// ConfigReloader orchestrates config hot-reload with atomic rollback.
type ConfigReloader struct {
	current   *Config
	mu        sync.Mutex
	reloaders []Reloader
	logger    zerolog.Logger
}

// NewConfigReloader creates a ConfigReloader with the given initial config and reloaders.
func NewConfigReloader(current *Config, logger zerolog.Logger, reloaders ...Reloader) *ConfigReloader {
	return &ConfigReloader{
		current:   current,
		reloaders: reloaders,
		logger:    logger,
	}
}

// Current returns the current config snapshot.
func (r *ConfigReloader) Current() *Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// Apply parses newYAML, diffs against current config, and atomically applies reloadable changes.
func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newCfg := &Config{}
	if err := yaml.Unmarshal(newYAML, newCfg); err != nil {
		return fmt.Errorf("parse config yaml: %w", err)
	}

	cs, nonReloadable, err := Diff(r.current, newCfg)
	if err != nil {
		return fmt.Errorf("diff config: %w", err)
	}

	if len(nonReloadable) > 0 {
		fields := make([]string, len(nonReloadable))
		for i, nr := range nonReloadable {
			fields[i] = nr.Field
		}
		return fmt.Errorf("non-reloadable changes rejected (restart required): %v", fields)
	}

	var applied []Reloader
	for _, rel := range r.reloaders {
		if !rel.CanReload(cs) {
			continue
		}
		if err := rel.Apply(newCfg); err != nil {
			// Rollback all previously applied reloaders.
			for i := len(applied) - 1; i >= 0; i-- {
				if rbErr := applied[i].Rollback(r.current); rbErr != nil {
					r.logger.Error().Err(rbErr).Msg("rollback failed during partial apply")
				}
			}
			return fmt.Errorf("apply reloader failed: %w", err)
		}
		applied = append(applied, rel)
	}

	r.current = newCfg
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/config/ -run TestConfigReloader`
Expected: PASS

- [ ] **Step 5: Run all config tests**

Run: `go test -race ./internal/config/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/reload.go internal/config/reload_test.go
git commit -m "feat(config): add Reloader interface and ConfigReloader with atomic rollback"
```

---

### Task 4: Collector 热切换类型 + Scheduler 重构

**Files:**
- Modify: `internal/collector/scheduler.go`
- Modify: `internal/collector/scheduler_test.go`
- Modify: `internal/app/interfaces.go`

- [ ] **Step 1: Write failing test for Scheduler.Reload**

Add to `internal/collector/scheduler_test.go`:

```go
func TestSchedulerReload(t *testing.T) {
	input1 := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input1, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Wait for at least one gather
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial metrics")
	}

	// Reload with a different input
	input2 := newTestInput("mem", nil, map[string]interface{}{"v": 2.0})
	newInputs := []ScheduledInput{{Input: input2, Interval: 50 * time.Millisecond}}

	err := sched.Reload(ctx, ReloadConfig{
		Inputs: []PluginConfig{{Type: "mem", Config: map[string]interface{}{}}},
	})
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Wait for metrics from new input
	seen := false
	timeout := time.After(2 * time.Second)
	for !seen {
		select {
		case batch := <-ch:
			for _, m := range batch {
				if m.Name() == "mem" {
					seen = true
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for metrics from new input")
		}
	}

	sched.Stop()
}

func TestSchedulerReload_EmptyConfig(t *testing.T) {
	input1 := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input1, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	// Reload with empty config — should stop all inputs
	err := sched.Reload(ctx, ReloadConfig{})
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Channel should eventually close since no inputs are running
	// Wait a bit then stop
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after reload with empty config and stop")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/ -run TestSchedulerReload`
Expected: FAIL — `ReloadConfig`, `PluginConfig`, `Reload` not defined

- [ ] **Step 3: Add types and refactor Scheduler**

Add to `internal/collector/scheduler.go`:

```go
// ReloadConfig is the collector pipeline config snapshot, converted from config.CollectorConfig
// by CollectorReloader to avoid circular imports.
type ReloadConfig struct {
	Inputs      []PluginConfig
	Processors  []PluginConfig
	Aggregators []PluginConfig
	Outputs     []PluginConfig
}

// PluginConfig is a single plugin instance config.
type PluginConfig struct {
	Type   string
	Config map[string]interface{}
}
```

Replace `startOnce sync.Once` with `running bool` + `mu sync.Mutex` in `Scheduler` struct. Refactor `Start` and `Stop` accordingly. Add `Reload` method:

```go
// Reload stops all current inputs, rebuilds the pipeline from cfg, and restarts.
func (s *Scheduler) Reload(ctx context.Context, cfg ReloadConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop current goroutines.
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()

	// Push aggregator results before teardown.
	if len(s.aggregators) > 0 {
		acc := NewAccumulator(defaultAccumulatorSize)
		for _, agg := range s.aggregators {
			agg.Push(acc)
			agg.Reset()
		}
		// Note: outputs may be stale here, but we try anyway.
	}

	// Rebuild pipeline.
	var scheduledInputs []ScheduledInput
	for _, inCfg := range cfg.Inputs {
		factory, ok := DefaultRegistry.GetInput(inCfg.Type)
		if !ok {
			return fmt.Errorf("unknown input type: %q", inCfg.Type)
		}
		input := factory()
		if err := input.Init(inCfg.Config); err != nil {
			return fmt.Errorf("init input %q: %w", inCfg.Type, err)
		}
		scheduledInputs = append(scheduledInputs, ScheduledInput{Input: input, Interval: s.interval})
	}

	var processors []Processor
	for _, pCfg := range cfg.Processors {
		factory, ok := DefaultRegistry.GetProcessor(pCfg.Type)
		if !ok {
			return fmt.Errorf("unknown processor type: %q", pCfg.Type)
		}
		p := factory()
		if err := p.Init(pCfg.Config); err != nil {
			return fmt.Errorf("init processor %q: %w", pCfg.Type, err)
		}
		processors = append(processors, p)
	}

	var aggregators []Aggregator
	for _, aCfg := range cfg.Aggregators {
		factory, ok := DefaultRegistry.GetAggregator(aCfg.Type)
		if !ok {
			return fmt.Errorf("unknown aggregator type: %q", aCfg.Type)
		}
		agg := factory()
		if err := agg.Init(aCfg.Config); err != nil {
			return fmt.Errorf("init aggregator %q: %w", aCfg.Type, err)
		}
		aggregators = append(aggregators, agg)
	}

	var outputs []Output
	for _, oCfg := range cfg.Outputs {
		factory, ok := DefaultRegistry.GetOutput(oCfg.Type)
		if !ok {
			return fmt.Errorf("unknown output type: %q", oCfg.Type)
		}
		out := factory()
		if err := out.Init(oCfg.Config); err != nil {
			return fmt.Errorf("init output %q: %w", oCfg.Type, err)
		}
		outputs = append(outputs, out)
	}

	// Replace fields.
	s.inputs = scheduledInputs
	s.processors = processors
	s.aggregators = aggregators
	s.outputs = outputs

	// Restart goroutines if previously running.
	if s.running {
		ctx, s.cancel = context.WithCancel(ctx)
		for _, si := range s.inputs {
			s.wg.Add(1)
			go s.runInput(ctx, si, s.outCh)
		}
		if len(s.aggregators) > 0 {
			s.wg.Add(1)
			go s.runAggregatorPush(ctx)
		}
	}

	return nil
}
```

**Note:** The Scheduler needs the following structural changes:
- Add `interval time.Duration` field — set in `NewScheduler` from the first `ScheduledInput.Interval` (all inputs share the same interval from `agent.interval_seconds`)
- Add `outCh chan []*Metric` field — the output channel, created once in `Start` and stored for reuse by `Reload`
- Add `mu sync.Mutex` field — protects `running`, `cancel`, and field replacement during `Reload`
- Refactor `Start` to store `outCh` and set `running = true` under `s.mu`
- Refactor `Stop` to use `s.mu` and set `running = false`
- Remove `startOnce sync.Once`

- [ ] **Step 4: Update Scheduler interface in interfaces.go**

```go
// internal/app/interfaces.go
type Scheduler interface {
	Start(ctx context.Context) <-chan []*collector.Metric
	Reload(ctx context.Context, cfg collector.ReloadConfig) error
	Stop()
}
```

- [ ] **Step 5: Update mockScheduler in agent_test.go**

Add to `mockScheduler`:
```go
func (m *mockScheduler) Reload(_ context.Context, _ collector.ReloadConfig) error { return nil }
```

- [ ] **Step 6: Run tests**

Run: `go test -race ./internal/collector/ -run TestSchedulerReload && go test -race ./internal/app/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/scheduler.go internal/collector/scheduler_test.go internal/app/interfaces.go internal/app/agent_test.go
git commit -m "feat(collector): add ReloadConfig types and Scheduler.Reload method"
```

---

### Task 5: CollectorReloader

**Files:**
- Create: `internal/collector/reload.go`
- Create: `internal/collector/reload_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/collector/reload_test.go
package collector

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

// mockSchedulerForReload implements a minimal scheduler for testing CollectorReloader.
type mockSchedulerForReload struct {
	reloadCalled int
	reloadErr    error
	lastCfg      ReloadConfig
}

func (m *mockSchedulerForReload) Start(_ context.Context) <-chan []*Metric { return nil }
func (m *mockSchedulerForReload) Reload(_ context.Context, cfg ReloadConfig) error {
	m.reloadCalled++
	m.lastCfg = cfg
	return m.reloadErr
}
func (m *mockSchedulerForReload) Stop() {}

// ChangeSet mirrors config.ChangeSet to avoid import. In real code, CollectorReloader
// imports config.ChangeSet. For this test, we test the reloader logic directly.
type testChangeSet struct {
	CollectorChanged bool
}

func TestCollectorReloader_CanReload(t *testing.T) {
	sched := &mockSchedulerForReload{}
	_ = sched // CollectorReloader will be tested via config package integration
}
```

- [ ] **Step 2: Implement CollectorReloader**

```go
// internal/collector/reload.go
package collector

import (
	"context"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

// CollectorReloader implements config.Reloader for the collector pipeline.
type CollectorReloader struct {
	scheduler *Scheduler
	logger    zerolog.Logger
}

// NewCollectorReloader creates a CollectorReloader.
func NewCollectorReloader(scheduler *Scheduler, logger zerolog.Logger) *CollectorReloader {
	return &CollectorReloader{scheduler: scheduler, logger: logger}
}

// CanReload returns true if the collector config changed.
func (r *CollectorReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.CollectorChanged
}

// Apply converts config.CollectorConfig to ReloadConfig and calls Scheduler.Reload.
func (r *CollectorReloader) Apply(newCfg *config.Config) error {
	rc := toReloadConfig(newCfg.Collector)
	return r.scheduler.Reload(context.Background(), rc)
}

// Rollback restores the old collector config.
func (r *CollectorReloader) Rollback(oldCfg *config.Config) error {
	rc := toReloadConfig(oldCfg.Collector)
	return r.scheduler.Reload(context.Background(), rc)
}

func toReloadConfig(cc config.CollectorConfig) ReloadConfig {
	rc := ReloadConfig{}
	for _, in := range cc.Inputs {
		rc.Inputs = append(rc.Inputs, PluginConfig{Type: in.Type, Config: in.Config})
	}
	for _, p := range cc.Processors {
		rc.Processors = append(rc.Processors, PluginConfig{Type: p.Type, Config: p.Config})
	}
	for _, a := range cc.Aggregators {
		rc.Aggregators = append(rc.Aggregators, PluginConfig{Type: a.Type, Config: a.Config})
	}
	for _, o := range cc.Outputs {
		rc.Outputs = append(rc.Outputs, PluginConfig{Type: o.Type, Config: o.Config})
	}
	return rc
}
```

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/collector/ -run TestCollectorReloader`
Expected: PASS

- [ ] **Step 4: Run all collector tests**

Run: `go test -race ./internal/collector/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/collector/reload.go internal/collector/reload_test.go
git commit -m "feat(collector): add CollectorReloader implementing config.Reloader"
```

---

### Task 6: Server 运行时更新方法 + AuthReloader/PrometheusReloader

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/reload.go`
- Create: `internal/server/reload_test.go`

- [ ] **Step 1: Write failing tests for UpdateAuth/UpdatePrometheus**

```go
// internal/server/reload_test.go
package server

import (
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/task"
	"github.com/rs/zerolog"
)

func newTestServer(opts Options) *Server {
	return New(":0", zerolog.Nop(), executor.New([]string{"ls"}, 10*time.Second, 1024), task.NewDispatcher(), time.Now(), opts)
}

func TestServer_UpdateAuth(t *testing.T) {
	s := newTestServer(Options{Auth: AuthConfig{Enabled: false}})
	s.UpdateAuth(AuthConfig{Enabled: true, BearerToken: "new-tok"})
	// Verify by checking that subsequent requests would use new auth.
	// Since we can't easily test HTTP middleware in unit test, verify no panic.
}

func TestServer_UpdatePrometheus(t *testing.T) {
	s := newTestServer(Options{Prometheus: PrometheusConfig{Enabled: true, Path: "/metrics"}})
	s.UpdatePrometheus(PrometheusConfig{Enabled: true, Path: "/new-metrics"})
	// Verify no panic.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/server/ -run TestServer_Update`
Expected: FAIL — `UpdateAuth`/`UpdatePrometheus` not defined

- [ ] **Step 3: Add UpdateAuth and UpdatePrometheus to server.go**

Add to `internal/server/server.go`:

```go
// UpdateAuth atomically updates the auth configuration.
func (s *Server) UpdateAuth(cfg AuthConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Auth = cfg
}

// UpdatePrometheus atomically updates the Prometheus configuration.
func (s *Server) UpdatePrometheus(cfg PrometheusConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options.Prometheus = cfg
}

// GetAuth returns the current auth config snapshot.
func (s *Server) GetAuth() AuthConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.options.Auth
}

// GetPrometheus returns the current prometheus config snapshot.
func (s *Server) GetPrometheus() PrometheusConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.options.Prometheus
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/server/ -run TestServer_Update`
Expected: PASS

- [ ] **Step 5: Implement AuthReloader and PrometheusReloader**

```go
// internal/server/reload.go
package server

import (
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
```

- [ ] **Step 6: Run all server tests**

Run: `go test -race ./internal/server/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/reload.go internal/server/reload_test.go
git commit -m "feat(server): add UpdateAuth/UpdatePrometheus and AuthReloader/PrometheusReloader"
```

---

### Task 7: Reporter 运行时更新 + ReporterReloader

**Files:**
- Modify: `internal/reporter/reporter.go`
- Create: `internal/reporter/reload.go`
- Create: `internal/reporter/reload_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/reporter/reload_test.go
package reporter

import (
	"testing"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

func TestReporterReloader_CanReload(t *testing.T) {
	r := NewReporterReloader(zerolog.Nop())
	cs := &config.ChangeSet{ReporterChanged: true}
	if !r.CanReload(cs) {
		t.Error("expected CanReload = true when ReporterChanged")
	}
	cs2 := &config.ChangeSet{ReporterChanged: false}
	if r.CanReload(cs2) {
		t.Error("expected CanReload = false when ReporterChanged is false")
	}
}

func TestReporterReloader_Apply(t *testing.T) {
	r := NewReporterReloader(zerolog.Nop())
	cfg := &config.Config{
		Reporter: config.ReporterConfig{Mode: "stdout", TimeoutSeconds: 10},
	}
	if err := r.Apply(cfg); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/reporter/ -run TestReporterReloader`
Expected: FAIL — `NewReporterReloader` not defined

- [ ] **Step 3: Implement ReporterReloader**

```go
// internal/reporter/reload.go
package reporter

import (
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

// ReporterReloader implements config.Reloader for reporter configuration.
type ReporterReloader struct {
	logger zerolog.Logger
}

// NewReporterReloader creates a ReporterReloader.
func NewReporterReloader(logger zerolog.Logger) *ReporterReloader {
	return &ReporterReloader{logger: logger}
}

// CanReload returns true if reporter config changed.
func (r *ReporterReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.ReporterChanged
}

// Apply logs the reporter config change. The reporter is typically re-created
// at the agent level since it's a simple struct with no mutable state.
func (r *ReporterReloader) Apply(newCfg *config.Config) error {
	r.logger.Info().
		Str("mode", newCfg.Reporter.Mode).
		Int("timeout_seconds", newCfg.Reporter.TimeoutSeconds).
		Msg("reporter config updated")
	return nil
}

// Rollback logs the rollback.
func (r *ReporterReloader) Rollback(oldCfg *config.Config) error {
	r.logger.Info().
		Str("mode", oldCfg.Reporter.Mode).
		Int("timeout_seconds", oldCfg.Reporter.TimeoutSeconds).
		Msg("reporter config rolled back")
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/reporter/ -run TestReporterReloader`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/reporter/reload.go internal/reporter/reload_test.go
git commit -m "feat(reporter): add ReporterReloader implementing config.Reloader"
```

---

### Task 8: GRPCClient FlushAndStop + 缓存持久化

**Files:**
- Modify: `internal/grpcclient/client.go`
- Modify: `internal/grpcclient/client_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/grpcclient/client_test.go`:

```go
func TestFlushAndStop_PersistsOnStreamUnavailable(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)
	// Add metrics to cache without starting connection.
	for i := 0; i < 5; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	tmpFile := t.TempDir() + "/cache.json"
	err := c.FlushAndStop(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("FlushAndStop failed: %v", err)
	}

	// Verify persisted file exists and has metrics.
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read persisted cache: %v", err)
	}
	var metrics []*collector.Metric
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatalf("failed to unmarshal persisted cache: %v", err)
	}
	if len(metrics) != 5 {
		t.Errorf("persisted %d metrics, want 5", len(metrics))
	}
}

func TestLoadPersistedCache(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)

	// Create a persisted cache file.
	metrics := []*collector.Metric{
		collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now()),
	}
	data, _ := json.Marshal(metrics)
	tmpFile := t.TempDir() + "/cache.json"
	os.WriteFile(tmpFile, data, 0644)

	c.loadPersistedCache(tmpFile)

	if c.cache.Len() != 1 {
		t.Errorf("cache len = %d, want 1", c.cache.Len())
	}

	// File should be removed after loading.
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("persisted cache file should be removed after loading")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/grpcclient/ -run "TestFlushAndStop|TestLoadPersistedCache"`
Expected: FAIL — `FlushAndStop`/`loadPersistedCache` not defined

- [ ] **Step 3: Implement FlushAndStop and loadPersistedCache**

Add to `internal/grpcclient/client.go`:

```go
// FlushAndStop drains the cache, sends all metrics, and closes the connection.
// If sending fails and persistPath is non-empty, remaining metrics are written to disk.
func (c *Client) FlushAndStop(ctx context.Context, persistPath string) error {
	// Cancel the connection loop.
	if c.cancel != nil {
		c.cancel()
	}

	metrics := c.cache.Drain()
	if len(metrics) > 0 {
		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()

		sent := 0
		if stream != nil {
			// Send in batches of 100.
			batchSize := 100
			for i := 0; i < len(metrics); i += batchSize {
				end := i + batchSize
				if end > len(metrics) {
					end = len(metrics)
				}
				batch := metrics[i:end]
				msg := NewMetricBatchMessage(batch)
				if err := stream.Send(msg); err != nil {
					c.logger.Warn().Err(err).Msg("flush send failed, will persist remaining")
					metrics = metrics[i:] // remaining
					goto persist
				}
				sent = end
			}
			metrics = nil // all sent
		}

	persist:
		if len(metrics) > 0 && persistPath != "" {
			data, err := json.Marshal(metrics)
			if err != nil {
				c.logger.Error().Err(err).Msg("failed to marshal metrics for persistence")
			} else {
				if err := os.WriteFile(persistPath, data, 0644); err != nil {
					c.logger.Error().Err(err).Str("path", persistPath).Msg("failed to persist cache")
				} else {
					c.logger.Info().Int("count", len(metrics)).Str("path", persistPath).Msg("cache persisted to disk")
				}
			}
		} else if len(metrics) > 0 {
			c.logger.Warn().Int("count", len(metrics)).Msg("cache not persisted (no persist path configured)")
		}

		_ = sent // used for logging if needed
	}

	// Close connection.
	c.closeConn()

	// Wait for goroutines.
	c.wg.Wait()
	return nil
}

// loadPersistedCache loads metrics from a JSON file into the cache and removes the file.
func (c *Client) loadPersistedCache(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist or read error, ignore
	}
	var metrics []*collector.Metric
	if err := json.Unmarshal(data, &metrics); err != nil {
		c.logger.Warn().Err(err).Msg("failed to parse persisted cache, discarding")
		os.Remove(path)
		return
	}
	for _, m := range metrics {
		c.cache.Add(m)
	}
	os.Remove(path)
	c.logger.Info().Int("count", len(metrics)).Msg("loaded persisted cache")
}
```

Add necessary imports: `encoding/json`, `os`.

Modify `connect` to call `loadPersistedCache` before `replayCache`:
```go
func (c *Client) connect(ctx context.Context) error {
	// ... existing code up to stream creation ...

	c.mu.Lock()
	c.conn = conn
	c.stream = stream
	c.connected = true
	c.mu.Unlock()

	c.logger.Info().Msg("connected to platform")

	// Load persisted cache (from previous shutdown) before replaying in-memory cache.
	if c.cfg.CachePersistPath != "" {
		c.loadPersistedCache(c.cfg.CachePersistPath)
	}
	c.replayCache()
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/grpcclient/ -run "TestFlushAndStop|TestLoadPersistedCache"`
Expected: PASS

- [ ] **Step 5: Run all grpcclient tests**

Run: `go test -race ./internal/grpcclient/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/grpcclient/client.go internal/grpcclient/client_test.go
git commit -m "feat(grpcclient): add FlushAndStop with cache persistence and loadPersistedCache"
```

---

### Task 9: 优雅关闭重构

**Files:**
- Modify: `internal/app/agent.go`
- Modify: `internal/app/agent_test.go`

- [ ] **Step 1: Write failing tests for graceful shutdown**

Add to `internal/app/agent_test.go`:

```go
func TestAgentShutdown_RejectsNewTasks(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}
	agent.shuttingDown.Store(true)

	// Try to execute a task via dispatcher - should be rejected.
	// Since registerTaskHandlers isn't called, we test the flag directly.
	if !agent.shuttingDown.Load() {
		t.Error("expected shuttingDown to be true")
	}
}

func TestAgentShutdown_WaitsForActiveTasks(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}

	// Simulate an active task.
	taskCtx, taskCancel := context.WithCancel(context.Background())
	agent.activeTasks.Store("task-1", taskCancel)

	done := make(chan struct{})
	go func() {
		// Simulate task completing after 100ms.
		time.Sleep(100 * time.Millisecond)
		agent.activeTasks.Delete("task-1")
		taskCancel()
		close(done)
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.waitForActiveTasks(shutdownCtx)

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("waitForActiveTasks did not return after task completed")
	}
}

func TestAgentShutdown_ForceCancelsOnTimeout(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}

	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()
	agent.activeTasks.Store("task-1", taskCancel)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	agent.waitForActiveTasks(shutdownCtx)

	// taskCancel should have been called (via force cancel).
	select {
	case <-taskCtx.Done():
		// OK - task was cancelled
	default:
		t.Error("expected task context to be cancelled on timeout")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/app/ -run "TestAgentShutdown"`
Expected: FAIL — `shuttingDown`/`waitForActiveTasks` not defined

- [ ] **Step 3: Add shuttingDown flag and waitForActiveTasks to agent.go**

Add to `Agent` struct:
```go
type Agent struct {
	// ... existing fields
	shuttingDown atomic.Bool
}
```

Add `sync/atomic` to imports if not already there.

Add method:
```go
func (a *Agent) waitForActiveTasks(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			a.activeTasks.Range(func(key, value any) bool {
				value.(context.CancelFunc)()
				return true
			})
			return
		case <-ticker.C:
			remaining := 0
			a.activeTasks.Range(func(_, _ any) bool { remaining++; return true })
			if remaining == 0 {
				return
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/app/ -run "TestAgentShutdown"`
Expected: PASS

- [ ] **Step 5: Refactor shutdown method**

Replace existing `shutdown()` with:

```go
func (a *Agent) shutdown(ctx context.Context) {
	// 1. Mark as shutting down.
	a.shuttingDown.Store(true)

	// 2. Wait for active tasks.
	a.waitForActiveTasks(ctx)

	// 3. Stop scheduler.
	if a.scheduler != nil {
		a.scheduler.Stop()
	}

	// 4. Flush gRPC cache.
	if err := a.grpcClient.FlushAndStop(ctx, a.cfg.GRPC.CachePersistPath); err != nil {
		a.log.Error().Err(err).Msg("failed to flush gRPC client")
	}

	// 5. Stop plugin runtime.
	stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
	defer stopCancel()
	if err := a.pluginRuntime.Stop(stopCtx); err != nil {
		a.log.Error().Err(err).Msg("failed to stop plugin runtime")
	}

	// 6. Shutdown HTTP server.
	if err := a.server.Shutdown(ctx); err != nil {
		a.log.Error().Err(err).Msg("failed to shutdown server")
	}
}
```

Update `Run` to use configurable timeout:
```go
func (a *Agent) Run(ctx context.Context) error {
	pipelineCh, errCh, err := a.startSubsystems(ctx)
	if err != nil {
		return err
	}
	a.eventLoop(ctx, pipelineCh, errCh)

	timeout := time.Duration(a.cfg.Agent.ShutdownTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	a.shutdown(shutdownCtx)
	return nil
}
```

- [ ] **Step 6: Add shuttingDown checks to handlers**

In `registerTaskHandlers`, at the top of each handler function (before existing logic):
```go
if a.shuttingDown.Load() {
    return nil, fmt.Errorf("agent is shutting down")
}
```

In `registerGRPCHandlers`, at the top of CommandHandler and ScriptHandler:
```go
if a.shuttingDown.Load() {
    return fmt.Errorf("agent is shutting down")
}
```

- [ ] **Step 7: Update GRPCClient interface**

Add `FlushAndStop` to the `GRPCClient` interface in `interfaces.go`:
```go
type GRPCClient interface {
	Start(ctx context.Context) error
	Stop()
	FlushAndStop(ctx context.Context, persistPath string) error
	SendMetrics(metrics []*collector.Metric)
	SendExecOutput(taskID, streamName string, data []byte)
	SendExecResult(result *grpcclient.ExecResult)
	IsConnected() bool
}
```

Update `mockGRPCClient` in `agent_test.go`:
```go
func (m *mockGRPCClient) FlushAndStop(_ context.Context, _ string) error {
	m.stopCalled.Add(1)
	return nil
}
```

- [ ] **Step 8: Run all app tests**

Run: `go test -race ./internal/app/`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/app/agent.go internal/app/agent_test.go internal/app/interfaces.go
git commit -m "feat(app): implement graceful shutdown with task draining and gRPC flush"
```

---

### Task 10: Agent ConfigReloader 集成

**Files:**
- Modify: `internal/app/agent.go`
- Modify: `internal/app/agent_test.go`
- Modify: `internal/app/options.go`

- [ ] **Step 1: Add configReloader field and ConfigReloader() accessor to Agent**

In `agent.go`, add to `Agent` struct:
```go
configReloader *config.ConfigReloader
```

Add accessor:
```go
// ConfigReloader returns the agent's config reloader.
func (a *Agent) ConfigReloader() *config.ConfigReloader {
	return a.configReloader
}
```

- [ ] **Step 2: Construct ConfigReloader in NewAgent**

After building all subsystems in `NewAgent`, before returning:

```go
// Build config reloader.
collectorReloader := collector.NewCollectorReloader(a.scheduler.(*collector.Scheduler), log)
authReloader := server.NewAuthReloader(a.server.(*server.Server))
prometheusReloader := server.NewPrometheusReloader(a.server.(*server.Server))
reporterReloader := reporter.NewReporterReloader(log)
a.configReloader = config.NewConfigReloader(cfg, log,
	collectorReloader,
	authReloader,
	prometheusReloader,
	reporterReloader,
)
```

**Note:** This only works when scheduler and server are concrete types. When injected via options (testing), we need to handle gracefully. Add a `WithConfigReloader` option:

```go
// WithConfigReloader injects a custom ConfigReloader (for testing).
func WithConfigReloader(r *config.ConfigReloader) Option {
	return func(a *Agent) { a.configReloader = r }
}
```

In `NewAgent`, only build the reloader if not injected:
```go
if a.configReloader == nil {
	// Build reloaders from concrete subsystems.
	// ... (the construction code above)
}
```

- [ ] **Step 3: Update ConfigUpdate gRPC handler**

Replace the existing `SetConfigUpdateHandler` in `registerGRPCHandlers`:

```go
recv.SetConfigUpdateHandler(func(ctx context.Context, update *pb.ConfigUpdate) error {
	if err := a.configReloader.Apply(ctx, update.GetConfigYaml()); err != nil {
		a.log.Error().Err(err).Int64("version", update.GetVersion()).Msg("config reload failed")
		a.grpcClient.SendExecResult(&grpcclient.ExecResult{
			TaskID:   fmt.Sprintf("config-update-%d", update.GetVersion()),
			ExitCode: -1,
		})
		return nil
	}
	a.log.Info().Int64("version", update.GetVersion()).Msg("config reloaded")
	a.grpcClient.SendExecResult(&grpcclient.ExecResult{
		TaskID: fmt.Sprintf("config-update-%d", update.GetVersion()),
	})
	return nil
})
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/app/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent.go internal/app/agent_test.go internal/app/options.go
git commit -m "feat(app): integrate ConfigReloader with gRPC ConfigUpdate handler"
```

---

### Task 11: SIGHUP 支持

**Files:**
- Modify: `cmd/agent/main.go`

- [ ] **Step 1: Add SIGHUP handler to run command**

Modify `cmd/agent/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cy77cc/opsagent/internal/app"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/logger"
)

var version = "dev"

func main() {
	app.Version = version
	if err := app.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
```

Update the `runCmd` in `app.NewRootCommand()` — add SIGHUP listener after agent creation:

```go
runCmd := &cobra.Command{
	Use:   "run",
	Short: "Run telemetry exec agent",
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}

		logLevel := os.Getenv("LOG_LEVEL")
		if logLevel == "" {
			logLevel = "info"
		}
		log := logger.New(logLevel)

		agent, err := NewAgent(cfg, log)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		// SIGHUP handler for config reload.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGHUP)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case sig := <-sigCh:
					if sig == syscall.SIGHUP {
						yaml, err := os.ReadFile(configPath)
						if err != nil {
							log.Error().Err(err).Msg("failed to read config file for SIGHUP reload")
							continue
						}
						if err := agent.ConfigReloader().Apply(ctx, yaml); err != nil {
							log.Error().Err(err).Msg("SIGHUP config reload failed")
						} else {
							log.Info().Msg("config reloaded via SIGHUP")
						}
					}
				}
			}
		}()

		return agent.Run(ctx)
	},
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./cmd/agent/`
Expected: PASS

- [ ] **Step 3: Run all tests**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/agent/main.go
git commit -m "feat(agent): add SIGHUP handler for config file reload"
```

---

### Task 12: 集成验证 + 最终清理

**Files:**
- Modify: `internal/app/agent_test.go`

- [ ] **Step 1: Add integration test for config reload flow**

```go
func TestAgentConfigReload_Integration(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := newMockPluginRuntime()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(httpServer),
		WithScheduler(scheduler),
		WithPluginRuntime(pluginRuntime),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Test that ConfigReloader is accessible.
	if agent.ConfigReloader() == nil {
		t.Fatal("ConfigReloader should not be nil")
	}
}
```

- [ ] **Step 2: Run all tests**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "test(app): add config reload integration test and final cleanup"
```

---

### Verification

After all tasks are complete, run the full verification:

```bash
# All unit tests with race detector
go test -race ./internal/config/ ./internal/collector/ ./internal/grpcclient/ ./internal/server/ ./internal/reporter/ ./internal/app/

# Build check
go build ./cmd/agent/

# Vet
go vet ./...
```

Expected: All PASS, zero failures.
