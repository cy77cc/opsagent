# Custom Plugin SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable users to write custom plugins in Go or Rust that run as独立进程, discovered and managed by a PluginGateway, communicating via UDS JSON-RPC.

**Architecture:** PluginGateway scans a directory for `plugin.yaml` manifests, launches each plugin as an独立进程, manages lifecycle (health check, auto-restart, file-watch hot-reload), and routes task types (`{name}/{type}`) to the correct plugin. Go and Rust SDKs provide the plugin-side UDS server.

**Tech Stack:** Go (gateway, SDK), Rust (SDK), UDS JSON-RPC (protocol), fsnotify (file watching), zerolog (logging)

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `internal/pluginruntime/manifest.go` | PluginManifest types, YAML parsing, validation |
| `internal/pluginruntime/manifest_test.go` | Manifest tests |
| `internal/pluginruntime/gateway.go` | PluginGateway core: start, stop, load/unload plugins |
| `internal/pluginruntime/gateway_test.go` | Gateway tests |
| `internal/pluginruntime/health.go` | Health check loop, auto-restart with backoff |
| `internal/pluginruntime/health_test.go` | Health check tests |
| `internal/pluginruntime/watcher.go` | fsnotify file watcher, debounce, reload |
| `internal/pluginruntime/watcher_test.go` | Watcher tests |
| `internal/pluginruntime/sandbox.go` | Optional nsjail wrapper for plugin processes |
| `sdk/plugin/handler.go` | Go SDK Handler interface, TaskRequest/TaskResponse |
| `sdk/plugin/serve.go` | Go SDK Serve function (UDS server) |
| `sdk/plugin/protocol.go` | Go SDK JSON-RPC wire types |
| `sdk/plugin/chunking.go` | Go SDK response chunking |
| `sdk/plugin/serve_test.go` | Go SDK tests |
| `sdk/plugin/go.mod` | Go SDK module definition |
| `sdk/opsagent-plugin/Cargo.toml` | Rust SDK crate manifest |
| `sdk/opsagent-plugin/src/lib.rs` | Rust SDK Plugin trait, serve function |
| `sdk/opsagent-plugin/src/protocol.rs` | Rust SDK JSON-RPC types |
| `sdk/opsagent-plugin/src/error.rs` | Rust SDK error types |
| `sdk/opsagent-plugin/tests/integration.rs` | Rust SDK integration tests |
| `sdk/examples/go-echo/plugin.yaml` | Go echo plugin manifest |
| `sdk/examples/go-echo/main.go` | Go echo plugin implementation |
| `sdk/examples/go-echo/go.mod` | Go echo plugin module |
| `sdk/examples/rust-audit/plugin.yaml` | Rust audit plugin manifest |
| `sdk/examples/rust-audit/Cargo.toml` | Rust audit plugin crate |
| `sdk/examples/rust-audit/src/main.rs` | Rust audit plugin implementation |

### Modified Files
| File | Changes |
|------|---------|
| `internal/task/dispatcher.go` | Add `Unregister` method |
| `internal/config/config.go` | Add `PluginGatewayConfig`, defaults, validation |
| `internal/app/interfaces.go` | Add `PluginGateway` interface |
| `internal/app/agent.go` | Wire PluginGateway into Agent lifecycle |
| `go.mod` | Add `fsnotify` dependency |

---

### Task 1: Manifest Types and Validation

**Files:**
- Create: `internal/pluginruntime/manifest.go`
- Create: `internal/pluginruntime/manifest_test.go`

- [ ] **Step 1: Write manifest validation tests**

```go
// internal/pluginruntime/manifest_test.go
package pluginruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseManifest_Valid(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
description: "A test plugin"
author: "test@example.com"
runtime: process
binary_path: ./my-plugin
task_types:
  - audit
  - report
limits:
  max_memory_mb: 256
  timeout_seconds: 30
`
	manifest, err := ParseManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Name != "test-plugin" {
		t.Errorf("name = %q, want %q", manifest.Name, "test-plugin")
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", manifest.Version, "1.0.0")
	}
	if len(manifest.TaskTypes) != 2 {
		t.Errorf("task_types len = %d, want 2", len(manifest.TaskTypes))
	}
	if manifest.Limits == nil {
		t.Fatal("limits should not be nil")
	}
	if manifest.Limits.MaxMemoryMB != 256 {
		t.Errorf("max_memory_mb = %d, want 256", manifest.Limits.MaxMemoryMB)
	}
}

func TestParseManifest_MissingName(t *testing.T) {
	yaml := `
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseManifest_MissingBinaryPath(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing binary_path")
	}
}

func TestParseManifest_MissingTaskTypes(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing task_types")
	}
}

func TestParseManifest_InvalidVersion(t *testing.T) {
	yaml := `
name: test-plugin
version: "not-semver"
binary_path: ./my-plugin
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestParseManifest_Defaults(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	manifest, err := ParseManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Runtime != "process" {
		t.Errorf("default runtime = %q, want %q", manifest.Runtime, "process")
	}
}

func TestValidateManifest_OS(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
requirements:
  os:
    - darwin
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestLoadManifestFromFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: file-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Name != "file-plugin" {
		t.Errorf("name = %q, want %q", manifest.Name, "file-plugin")
	}
	if manifest.BinaryPath != filepath.Join(dir, "my-plugin") {
		t.Errorf("binary_path = %q, want %q", manifest.BinaryPath, filepath.Join(dir, "my-plugin"))
	}
}

func TestFullTaskType(t *testing.T) {
	tests := []struct {
		plugin   string
		taskType string
		want     string
	}{
		{"my-plugin", "audit", "my-plugin/audit"},
		{"my-plugin", "report", "my-plugin/report"},
	}
	for _, tt := range tests {
		got := FullTaskType(tt.plugin, tt.taskType)
		if got != tt.want {
			t.Errorf("FullTaskType(%q, %q) = %q, want %q", tt.plugin, tt.taskType, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/pluginruntime/ -run TestParseManifest -v`
Expected: FAIL with "undefined: ParseManifest"

- [ ] **Step 3: Implement manifest types and parsing**

```go
// internal/pluginruntime/manifest.go
package pluginruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"gopkg.in/yaml.v3"
)

// PluginManifest represents a parsed plugin.yaml.
type PluginManifest struct {
	Name         string                 `yaml:"name"`
	Version      string                 `yaml:"version"`
	Description  string                 `yaml:"description"`
	Author       string                 `yaml:"author"`
	Runtime      string                 `yaml:"runtime"`
	BinaryPath   string                 `yaml:"binary_path"`
	Env          map[string]string      `yaml:"env"`
	TaskTypes    []string               `yaml:"task_types"`
	ConfigSchema map[string]interface{} `yaml:"config_schema"`
	Config       map[string]interface{} `yaml:"config"`
	Requirements *Requirements          `yaml:"requirements"`
	Limits       *Limits                `yaml:"limits"`
	HealthCheck  *HealthCheckConfig     `yaml:"health_check"`
	Sandbox      *SandboxConfig         `yaml:"sandbox"`

	// resolvedDir is the directory containing the manifest file.
	resolvedDir string
}

// Requirements specifies system requirements for the plugin.
type Requirements struct {
	MinKernelVersion string   `yaml:"min_kernel_version"`
	OS               []string `yaml:"os"`
}

// Limits specifies resource limits for the plugin process.
type Limits struct {
	MaxMemoryMB        int `yaml:"max_memory_mb"`
	MaxCPUPercent      int `yaml:"max_cpu_percent"`
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks"`
	TimeoutSeconds     int `yaml:"timeout_seconds"`
}

// HealthCheckConfig specifies health check parameters.
type HealthCheckConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
	TimeoutSeconds  int `yaml:"timeout_seconds"`
}

// SandboxConfig specifies optional sandbox settings.
type SandboxConfig struct {
	Enabled       bool     `yaml:"enabled"`
	NetworkAccess bool     `yaml:"network_access"`
	AllowedPaths  []string `yaml:"allowed_paths"`
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)

// ParseManifest parses YAML bytes into a PluginManifest and validates it.
func ParseManifest(data []byte) (*PluginManifest, error) {
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadManifest loads and parses a plugin.yaml from a file path.
// BinaryPath is resolved relative to the manifest directory.
func LoadManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	m.resolvedDir = filepath.Dir(path)
	if !filepath.IsAbs(m.BinaryPath) {
		m.BinaryPath = filepath.Join(m.resolvedDir, m.BinaryPath)
	}
	return m, nil
}

// Validate checks required fields and sane values.
func (m *PluginManifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("manifest: version %q is not valid semver", m.Version)
	}
	if m.BinaryPath == "" {
		return fmt.Errorf("manifest: binary_path is required")
	}
	if len(m.TaskTypes) == 0 {
		return fmt.Errorf("manifest: task_types must not be empty")
	}
	if m.Runtime == "" {
		m.Runtime = "process"
	}
	if m.Runtime != "process" {
		return fmt.Errorf("manifest: runtime must be 'process', got %q", m.Runtime)
	}
	if m.Requirements != nil && len(m.Requirements.OS) > 0 {
		currentOS := runtime.GOOS
		found := false
		for _, osName := range m.Requirements.OS {
			if osName == currentOS {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("manifest: unsupported OS %q, plugin requires %v", currentOS, m.Requirements.OS)
		}
	}
	return nil
}

// FullTaskType returns the namespaced task type: "plugin-name/task-type".
func FullTaskType(pluginName, taskType string) string {
	return pluginName + "/" + taskType
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/pluginruntime/ -run TestParseManifest -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pluginruntime/manifest.go internal/pluginruntime/manifest_test.go
git commit -m "feat(pluginruntime): add PluginManifest types, parsing, and validation"
```

---

### Task 2: Dispatcher Unregister

**Files:**
- Modify: `internal/task/dispatcher.go`

- [ ] **Step 1: Write unregister test**

```go
// Add to internal/task/dispatcher_test.go (create if needed)
package task

import (
	"context"
	"testing"
)

func TestDispatcher_Unregister(t *testing.T) {
	d := NewDispatcher()
	d.Register("test-type", func(_ context.Context, _ AgentTask) (any, error) {
		return "ok", nil
	})

	// Verify it works before unregister.
	result, err := d.Dispatch(context.Background(), AgentTask{Type: "test-type"})
	if err != nil {
		t.Fatalf("dispatch before unregister: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %v, want ok", result)
	}

	// Unregister and verify it fails.
	d.Unregister("test-type")
	_, err = d.Dispatch(context.Background(), AgentTask{Type: "test-type"})
	if err == nil {
		t.Fatal("expected error after unregister")
	}
}

func TestDispatcher_Unregister_NonExistent(t *testing.T) {
	d := NewDispatcher()
	// Should not panic.
	d.Unregister("non-existent")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/task/ -run TestDispatcher_Unregister -v`
Expected: FAIL with "undefined: Unregister"

- [ ] **Step 3: Implement Unregister**

Add to `internal/task/dispatcher.go`:

```go
// Unregister removes a task type handler.
func (d *Dispatcher) Unregister(taskType string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.handlers, taskType)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/task/ -run TestDispatcher_Unregister -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/task/dispatcher.go
git commit -m "feat(task): add Dispatcher.Unregister method"
```

---

### Task 3: PluginGateway Types and Interface

**Files:**
- Modify: `internal/app/interfaces.go`

- [ ] **Step 1: Define PluginGateway interface**

Add to `internal/app/interfaces.go`:

```go
// PluginGateway manages custom plugin lifecycle and routing.
type PluginGateway interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ExecuteTask(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error)
	ListPlugins() []PluginInfo
	GetPlugin(name string) *PluginInfo
	ReloadPlugin(name string) error
	EnablePlugin(name string) error
	DisablePlugin(name string) error
	OnPluginLoaded(fn func(name string, taskTypes []string))
	OnPluginUnloaded(fn func(name string, taskTypes []string))
}

// PluginInfo is the runtime status of a managed plugin.
type PluginInfo struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Status       PluginStatus `json:"status"`
	TaskTypes    []string     `json:"task_types"`
	SocketPath   string       `json:"socket_path"`
	RestartCount int          `json:"restart_count"`
	LastHealth   time.Time    `json:"last_health"`
	Uptime       time.Duration `json:"uptime"`
}

// PluginStatus represents the state of a managed plugin.
type PluginStatus string

const (
	PluginStatusStarting PluginStatus = "starting"
	PluginStatusRunning  PluginStatus = "running"
	PluginStatusStopped  PluginStatus = "stopped"
	PluginStatusError    PluginStatus = "error"
	PluginStatusDisabled PluginStatus = "disabled"
)
```

- [ ] **Step 2: Add compile-time check**

Add to the `var` block in `internal/app/interfaces.go`:

```go
// PluginGateway will be checked after Gateway is implemented.
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/app/`
Expected: PASS (interface is defined but not yet used)

- [ ] **Step 4: Commit**

```bash
git add internal/app/interfaces.go
git commit -m "feat(app): add PluginGateway interface and PluginInfo types"
```

---

### Task 4: PluginGateway Core

**Files:**
- Create: `internal/pluginruntime/gateway.go`
- Create: `internal/pluginruntime/gateway_test.go`

- [ ] **Step 1: Write Gateway creation and basic lifecycle tests**

```go
// internal/pluginruntime/gateway_test.go
package pluginruntime

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestGateway_New(t *testing.T) {
	cfg := GatewayConfig{
		PluginsDir:          t.TempDir(),
		StartupTimeout:      5 * time.Second,
		HealthCheckInterval: 30 * time.Second,
		MaxRestarts:         3,
		RestartBackoff:      5 * time.Second,
		FileWatchDebounce:   2 * time.Second,
	}
	gw := NewGateway(cfg, zerolog.Nop())
	if gw == nil {
		t.Fatal("NewGateway returned nil")
	}
}

func TestGateway_StartStop_EmptyDir(t *testing.T) {
	cfg := GatewayConfig{
		PluginsDir:          t.TempDir(),
		StartupTimeout:      5 * time.Second,
		HealthCheckInterval: 30 * time.Second,
		MaxRestarts:         3,
		RestartBackoff:      5 * time.Second,
		FileWatchDebounce:   2 * time.Second,
	}
	gw := NewGateway(cfg, zerolog.Nop())

	ctx := context.Background()
	if err := gw.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	plugins := gw.ListPlugins()
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}

	if err := gw.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestGateway_ListPlugins_Empty(t *testing.T) {
	cfg := GatewayConfig{
		PluginsDir: t.TempDir(),
	}
	gw := NewGateway(cfg, zerolog.Nop())

	plugins := gw.ListPlugins()
	if plugins == nil {
		plugins = []PluginInfo{} // normalize
	}
	if len(plugins) != 0 {
		t.Errorf("expected empty list, got %d", len(plugins))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/pluginruntime/ -run TestGateway -v`
Expected: FAIL with "undefined: GatewayConfig"

- [ ] **Step 3: Implement Gateway core**

```go
// internal/pluginruntime/gateway.go
package pluginruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// GatewayConfig configures the PluginGateway.
type GatewayConfig struct {
	PluginsDir            string
	StartupTimeout        time.Duration
	HealthCheckInterval   time.Duration
	MaxRestarts           int
	RestartBackoff        time.Duration
	FileWatchDebounce     time.Duration
	PluginConfigs         map[string]map[string]interface{}
}

// Gateway manages custom plugin processes.
type Gateway struct {
	cfg       GatewayConfig
	logger    zerolog.Logger
	plugins   map[string]*ManagedPlugin
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	onLoaded   func(name string, taskTypes []string)
	onUnloaded func(name string, taskTypes []string)
}

// ManagedPlugin represents a running plugin process.
type ManagedPlugin struct {
	Manifest     *PluginManifest
	Process      *ManagedProcess
	Client       *RPCClient
	SocketPath   string
	Status       PluginStatus
	RestartCount int
	LastHealth   time.Time
	StartedAt    time.Time
	mu           sync.Mutex
}

// ManagedProcess wraps an os.Process with lifecycle management.
type ManagedProcess struct {
	pid    int
	waitCh chan error
}

// NewGateway creates a PluginGateway.
func NewGateway(cfg GatewayConfig, logger zerolog.Logger) *Gateway {
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 10 * time.Second
	}
	if cfg.HealthCheckInterval <= 0 {
		cfg.HealthCheckInterval = 30 * time.Second
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.RestartBackoff <= 0 {
		cfg.RestartBackoff = 5 * time.Second
	}
	if cfg.FileWatchDebounce <= 0 {
		cfg.FileWatchDebounce = 2 * time.Second
	}
	return &Gateway{
		cfg:     cfg,
		logger:  logger,
		plugins: make(map[string]*ManagedPlugin),
	}
}

// Start discovers and loads all plugins from the plugins directory.
func (g *Gateway) Start(ctx context.Context) error {
	g.ctx, g.cancel = context.WithCancel(ctx)

	manifests, err := DiscoverManifests(g.cfg.PluginsDir)
	if err != nil {
		return fmt.Errorf("discover plugins: %w", err)
	}

	for _, manifest := range manifests {
		if err := g.loadPlugin(manifest); err != nil {
			g.logger.Error().Err(err).Str("plugin", manifest.Name).Msg("failed to load plugin")
			continue
		}
	}

	g.logger.Info().Int("count", len(g.plugins)).Msg("plugin gateway started")
	return nil
}

// Stop gracefully stops all managed plugins.
func (g *Gateway) Stop(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}
	g.wg.Wait()

	g.mu.Lock()
	defer g.mu.Unlock()

	for name, p := range g.plugins {
		g.stopPlugin(name, p)
	}

	g.logger.Info().Msg("plugin gateway stopped")
	return nil
}

// ExecuteTask routes a task to the appropriate plugin.
func (g *Gateway) ExecuteTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	pluginName, taskType, err := ParseFullTaskType(req.Type)
	if err != nil {
		return nil, err
	}

	g.mu.RLock()
	p, ok := g.plugins[pluginName]
	g.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", pluginName)
	}

	p.mu.Lock()
	status := p.Status
	client := p.Client
	p.mu.Unlock()

	if status != PluginStatusRunning {
		return nil, fmt.Errorf("plugin %s is not running (status: %s)", pluginName, status)
	}

	// Rewrite the task type to the short form for the plugin.
	req.Type = taskType
	return client.ExecuteTask(ctx, req)
}

// ListPlugins returns info about all managed plugins.
func (g *Gateway) ListPlugins() []PluginInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	infos := make([]PluginInfo, 0, len(g.plugins))
	for _, p := range g.plugins {
		p.mu.Lock()
		infos = append(infos, PluginInfo{
			Name:         p.Manifest.Name,
			Version:      p.Manifest.Version,
			Status:       p.Status,
			TaskTypes:    p.Manifest.TaskTypes,
			SocketPath:   p.SocketPath,
			RestartCount: p.RestartCount,
			LastHealth:   p.LastHealth,
		})
		p.mu.Unlock()
	}
	return infos
}

// GetPlugin returns info about a specific plugin.
func (g *Gateway) GetPlugin(name string) *PluginInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	p, ok := g.plugins[name]
	if !ok {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return &PluginInfo{
		Name:         p.Manifest.Name,
		Version:      p.Manifest.Version,
		Status:       p.Status,
		TaskTypes:    p.Manifest.TaskTypes,
		SocketPath:   p.SocketPath,
		RestartCount: p.RestartCount,
		LastHealth:   p.LastHealth,
	}
}

// ReloadPlugin stops and restarts a plugin.
func (g *Gateway) ReloadPlugin(name string) error {
	g.mu.Lock()
	p, ok := g.plugins[name]
	if !ok {
		g.mu.Unlock()
		return fmt.Errorf("plugin not found: %s", name)
	}
	manifest := p.Manifest
	g.stopPlugin(name, p)
	delete(g.plugins, name)
	g.mu.Unlock()

	// Reload manifest from file.
	newManifest, err := LoadManifest(findManifestPath(manifest))
	if err != nil {
		return fmt.Errorf("reload manifest: %w", err)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	return g.loadPlugin(newManifest)
}

// EnablePlugin enables a disabled plugin.
func (g *Gateway) EnablePlugin(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.mu.Lock()
	if p.Status != PluginStatusDisabled {
		p.mu.Unlock()
		return fmt.Errorf("plugin %s is not disabled", name)
	}
	p.mu.Unlock()

	return g.loadPlugin(p.Manifest)
}

// DisablePlugin stops and disables a plugin.
func (g *Gateway) DisablePlugin(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	p, ok := g.plugins[name]
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	g.stopPlugin(name, p)
	p.Status = PluginStatusDisabled
	return nil
}

// OnPluginLoaded registers a callback for when a plugin is loaded.
func (g *Gateway) OnPluginLoaded(fn func(name string, taskTypes []string)) {
	g.onLoaded = fn
}

// OnPluginUnloaded registers a callback for when a plugin is unloaded.
func (g *Gateway) OnPluginUnloaded(fn func(name string, taskTypes []string)) {
	g.onUnloaded = fn
}

// loadPlugin starts a plugin process and registers its routes.
func (g *Gateway) loadPlugin(manifest *PluginManifest) error {
	socketPath := fmt.Sprintf("/tmp/opsagent-plugin-%s.sock", manifest.Name)

	p := &ManagedPlugin{
		Manifest:   manifest,
		SocketPath: socketPath,
		Status:     PluginStatusStarting,
		StartedAt:  time.Now(),
	}

	// Merge config: manifest defaults + agent overrides.
	mergedConfig := manifest.Config
	if agentCfg, ok := g.cfg.PluginConfigs[manifest.Name]; ok {
		mergedConfig = mergePluginConfig(manifest.Config, agentCfg)
	}
	_ = mergedConfig // Will be passed to plugin via init

	// Start process.
	proc, err := g.startProcess(manifest, socketPath)
	if err != nil {
		p.Status = PluginStatusError
		g.plugins[manifest.Name] = p
		return fmt.Errorf("start plugin process: %w", err)
	}
	p.Process = proc

	// Wait for socket and create client.
	client, err := g.waitForPlugin(socketPath, g.cfg.StartupTimeout)
	if err != nil {
		p.Status = PluginStatusError
		g.plugins[manifest.Name] = p
		return fmt.Errorf("wait for plugin: %w", err)
	}
	p.Client = client
	p.Status = PluginStatusRunning
	p.LastHealth = time.Now()

	g.plugins[manifest.Name] = p

	// Notify loaded callback.
	if g.onLoaded != nil {
		g.onLoaded(manifest.Name, manifest.TaskTypes)
	}

	g.logger.Info().
		Str("plugin", manifest.Name).
		Str("version", manifest.Version).
		Strs("task_types", manifest.TaskTypes).
		Msg("plugin loaded")

	return nil
}

// stopPlugin stops a plugin process and unregisters its routes.
func (g *Gateway) stopPlugin(name string, p *ManagedPlugin) {
	if p.Client != nil {
		p.Client.Close()
	}
	if p.Process != nil {
		// Send SIGTERM, wait, then SIGKILL if needed.
		g.killProcess(p.Process)
	}
	p.Status = PluginStatusStopped

	if g.onUnloaded != nil {
		g.onUnloaded(name, p.Manifest.TaskTypes)
	}
}

// ParseFullTaskType splits "plugin-name/task-type" into parts.
func ParseFullTaskType(fullType string) (pluginName, taskType string, err error) {
	for i, c := range fullType {
		if c == '/' {
			return fullType[:i], fullType[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid task type format (expected 'name/type'): %s", fullType)
}

// DiscoverManifests scans a directory for plugin.yaml files.
func DiscoverManifests(dir string) ([]*PluginManifest, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var manifests []*PluginManifest
	for _, entry := range entries {
		if entry.IsDir() {
			// Check for plugin.yaml in subdirectory.
			manifestPath := filepath.Join(dir, entry.Name(), "plugin.yaml")
			if _, statErr := os.Stat(manifestPath); statErr == nil {
				m, loadErr := LoadManifest(manifestPath)
				if loadErr != nil {
					continue
				}
				manifests = append(manifests, m)
			}
		}
	}

	// Also check for plugin.yaml directly in dir.
	manifestPath := filepath.Join(dir, "plugin.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		m, loadErr := LoadManifest(manifestPath)
		if loadErr == nil {
			manifests = append(manifests, m)
		}
	}

	return manifests, nil
}

func mergePluginConfig(manifestCfg, agentCfg map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range manifestCfg {
		result[k] = v
	}
	for k, v := range agentCfg {
		result[k] = v
	}
	return result
}

func findManifestPath(m *PluginManifest) string {
	if m.resolvedDir != "" {
		return filepath.Join(m.resolvedDir, "plugin.yaml")
	}
	return ""
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/pluginruntime/ -run TestGateway -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pluginruntime/gateway.go internal/pluginruntime/gateway_test.go
git commit -m "feat(pluginruntime): add PluginGateway core with lifecycle management"
```

---

### Task 5: Gateway Process Management

**Files:**
- Modify: `internal/pluginruntime/gateway.go`
- Create: `internal/pluginruntime/gateway_test.go` (extend)

- [ ] **Step 1: Write process management tests**

```go
// Add to internal/pluginruntime/gateway_test.go

func TestParseFullTaskType(t *testing.T) {
	tests := []struct {
		input      string
		wantPlugin string
		wantTask   string
		wantErr    bool
	}{
		{"my-plugin/audit", "my-plugin", "audit", false},
		{"foo/bar/baz", "foo", "bar/baz", false},
		{"no-slash", "", "", true},
	}
	for _, tt := range tests {
		plugin, taskType, err := ParseFullTaskType(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFullTaskType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			continue
		}
		if plugin != tt.wantPlugin {
			t.Errorf("plugin = %q, want %q", plugin, tt.wantPlugin)
		}
		if taskType != tt.wantTask {
			t.Errorf("taskType = %q, want %q", taskType, tt.wantTask)
		}
	}
}

func TestMergePluginConfig(t *testing.T) {
	manifest := map[string]interface{}{
		"a": 1,
		"b": 2,
	}
	agent := map[string]interface{}{
		"b": 20,
		"c": 30,
	}
	result := mergePluginConfig(manifest, agent)
	if result["a"] != 1 {
		t.Errorf("a = %v, want 1", result["a"])
	}
	if result["b"] != 20 {
		t.Errorf("b = %v, want 20 (agent override)", result["b"])
	}
	if result["c"] != 30 {
		t.Errorf("c = %v, want 30", result["c"])
	}
}

func TestDiscoverManifests_EmptyDir(t *testing.T) {
	manifests, err := DiscoverManifests(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("expected 0 manifests, got %d", len(manifests))
	}
}

func TestDiscoverManifests_NonExistentDir(t *testing.T) {
	manifests, err := DiscoverManifests("/nonexistent/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifests != nil {
		t.Errorf("expected nil, got %v", manifests)
	}
}

func TestDiscoverManifests_WithPlugins(t *testing.T) {
	dir := t.TempDir()

	// Create a plugin in a subdirectory.
	pluginDir := filepath.Join(dir, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `
name: my-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	manifests, err := DiscoverManifests(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(manifests))
	}
	if manifests[0].Name != "my-plugin" {
		t.Errorf("name = %q, want %q", manifests[0].Name, "my-plugin")
	}
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test -race ./internal/pluginruntime/ -run "TestParseFullTaskType|TestMergePluginConfig|TestDiscoverManifests" -v`
Expected: PASS

- [ ] **Step 3: Add process management stubs to gateway.go**

```go
// Add to internal/pluginruntime/gateway.go

// startProcess launches a plugin binary with the socket env var.
func (g *Gateway) startProcess(manifest *PluginManifest, socketPath string) (*ManagedProcess, error) {
	_ = os.Remove(socketPath)

	cmd := exec.Command(manifest.BinaryPath)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "OPSAGENT_PLUGIN_SOCKET="+socketPath)
	for k, v := range manifest.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin %s: %w", manifest.Name, err)
	}

	proc := &ManagedProcess{
		pid:    cmd.Process.Pid,
		waitCh: make(chan error, 1),
	}

	go func() {
		proc.waitCh <- cmd.Wait()
	}()

	return proc, nil
}

// waitForPlugin polls for the socket and creates an RPC client.
func (g *Gateway) waitForPlugin(socketPath string, timeout time.Duration) (*RPCClient, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for plugin socket: %s", socketPath)
		case <-ticker.C:
			if _, err := os.Stat(socketPath); err == nil {
				// Socket exists, try to connect.
				client, err := NewRPCClient(socketPath)
				if err != nil {
					continue
				}
				// Ping to verify plugin is ready.
				if err := client.Ping(); err != nil {
					client.Close()
					continue
				}
				return client, nil
			}
		}
	}
}

// killProcess sends SIGTERM, waits 5s, then SIGKILL.
func (g *Gateway) killProcess(proc *ManagedProcess) {
	p, err := os.FindProcess(proc.pid)
	if err != nil {
		return
	}
	_ = p.Signal(os.Interrupt)

	select {
	case <-proc.waitCh:
		return
	case <-time.After(5 * time.Second):
		_ = p.Kill()
		<-proc.waitCh
	}
}

// RPCClient is a UDS JSON-RPC client for communicating with a plugin.
type RPCClient struct {
	socketPath string
	dial       func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewRPCClient creates a new RPC client connected to the given socket.
func NewRPCClient(socketPath string) (*RPCClient, error) {
	client := &RPCClient{
		socketPath: socketPath,
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return net.DialTimeout(network, address, 5*time.Second)
		},
	}
	return client, nil
}

// Ping sends a ping to verify the plugin is responsive.
func (c *RPCClient) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := c.dial(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	req := rpcRequest{
		ID:     "ping",
		Method: "ping",
		Params: TaskRequest{},
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("encode ping: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read pong: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decode pong: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("ping error: %s", resp.Error.Message)
	}
	return nil
}

// ExecuteTask sends a task to the plugin and returns the response.
func (c *RPCClient) ExecuteTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	conn, err := c.dial(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial plugin: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	rpcReq := rpcRequest{
		ID:     req.TaskID,
		Method: "execute_task",
		Params: req,
	}
	if err := json.NewEncoder(conn).Encode(rpcReq); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("plugin error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("empty plugin response")
	}
	return resp.Result, nil
}

// Close closes the RPC client.
func (c *RPCClient) Close() error {
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/pluginruntime/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pluginruntime/gateway.go
git commit -m "feat(pluginruntime): add process management and RPC client to Gateway"
```

---

### Task 6: Health Check and Auto-Restart

**Files:**
- Create: `internal/pluginruntime/health.go`
- Create: `internal/pluginruntime/health_test.go`

- [ ] **Step 1: Write health check tests**

```go
// internal/pluginruntime/health_test.go
package pluginruntime

import (
	"testing"
	"time"
)

func TestGateway_ShouldRestart(t *testing.T) {
	cfg := GatewayConfig{
		MaxRestarts: 3,
	}
	gw := &Gateway{cfg: cfg}

	tests := []struct {
		restartCount int
		want         bool
	}{
		{0, true},
		{1, true},
		{2, true},
		{3, false},
		{4, false},
	}
	for _, tt := range tests {
		p := &ManagedPlugin{RestartCount: tt.restartCount}
		got := gw.shouldRestart(p)
		if got != tt.want {
			t.Errorf("shouldRestart(count=%d) = %v, want %v", tt.restartCount, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/pluginruntime/ -run TestGateway_ShouldRestart -v`
Expected: FAIL with "undefined: shouldRestart"

- [ ] **Step 3: Implement health check**

```go
// internal/pluginruntime/health.go
package pluginruntime

import (
	"time"
)

// shouldRestart checks if a plugin can be restarted.
func (g *Gateway) shouldRestart(p *ManagedPlugin) bool {
	return p.RestartCount < g.cfg.MaxRestarts
}

// restartBackoff returns the backoff duration for a given restart count.
func (g *Gateway) restartBackoff(restartCount int) time.Duration {
	backoff := g.cfg.RestartBackoff
	for i := 0; i < restartCount; i++ {
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
			break
		}
	}
	return backoff
}

// startHealthCheck begins the periodic health check loop.
func (g *Gateway) startHealthCheck() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(g.cfg.HealthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-g.ctx.Done():
				return
			case <-ticker.C:
				g.checkAllPlugins()
			}
		}
	}()
}

// checkAllPlugins pings each running plugin and restarts unresponsive ones.
func (g *Gateway) checkAllPlugins() {
	g.mu.RLock()
	var toCheck []string
	for name, p := range g.plugins {
		p.mu.Lock()
		if p.Status == PluginStatusRunning {
			toCheck = append(toCheck, name)
		}
		p.mu.Unlock()
	}
	g.mu.RUnlock()

	for _, name := range toCheck {
		g.mu.RLock()
		p := g.plugins[name]
		g.mu.RUnlock()

		if err := p.Client.Ping(); err != nil {
			g.logger.Warn().Str("plugin", name).Err(err).Msg("plugin health check failed")
			g.handlePluginFailure(name, p)
		} else {
			p.mu.Lock()
			p.LastHealth = time.Now()
			p.mu.Unlock()
		}
	}
}

// handlePluginFailure restarts a failed plugin if possible.
func (g *Gateway) handlePluginFailure(name string, p *ManagedPlugin) {
	p.mu.Lock()
	if !g.shouldRestart(p) {
		p.Status = PluginStatusError
		p.mu.Unlock()
		g.logger.Error().Str("plugin", name).Msg("plugin exceeded max restarts")
		return
	}
	restartCount := p.RestartCount + 1
	p.mu.Unlock()

	backoff := g.restartBackoff(restartCount)
	g.logger.Info().Str("plugin", name).Dur("backoff", backoff).Msg("restarting plugin")
	time.Sleep(backoff)

	g.mu.Lock()
	g.stopPlugin(name, p)
	delete(g.plugins, name)
	g.mu.Unlock()

	// Reload manifest and restart.
	manifestPath := findManifestPath(p.Manifest)
	if manifestPath == "" {
		g.logger.Error().Str("plugin", name).Msg("cannot find manifest path for restart")
		return
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		g.logger.Error().Err(err).Str("plugin", name).Msg("failed to reload manifest for restart")
		return
	}

	g.mu.Lock()
	if err := g.loadPlugin(manifest); err != nil {
		g.logger.Error().Err(err).Str("plugin", name).Msg("failed to restart plugin")
	} else {
		g.plugins[name].RestartCount = restartCount
		g.logger.Info().Str("plugin", name).Int("restart_count", restartCount).Msg("plugin restarted")
	}
	g.mu.Unlock()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/pluginruntime/ -run TestGateway_ShouldRestart -v`
Expected: PASS

- [ ] **Step 5: Wire health check into Gateway.Start**

Add to `Gateway.Start()` after loading plugins:

```go
g.startHealthCheck()
```

- [ ] **Step 6: Commit**

```bash
git add internal/pluginruntime/health.go internal/pluginruntime/health_test.go internal/pluginruntime/gateway.go
git commit -m "feat(pluginruntime): add health check and auto-restart to Gateway"
```

---

### Task 7: File Watcher for Hot Reload

**Files:**
- Create: `internal/pluginruntime/watcher.go`
- Create: `internal/pluginruntime/watcher_test.go`

- [ ] **Step 1: Write watcher tests**

```go
// internal/pluginruntime/watcher_test.go
package pluginruntime

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_Debounce(t *testing.T) {
	w := &watcherState{
		debounce: 100 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
	}

	called := false
	w.debounceEvent("test-plugin", func() {
		called = true
	})

	// Should not be called immediately.
	if called {
		t.Fatal("should not be called before debounce")
	}

	// Wait for debounce.
	time.Sleep(200 * time.Millisecond)
	if !called {
		t.Fatal("should be called after debounce")
	}
}

func TestWatcher_Debounce_MultipleEvents(t *testing.T) {
	w := &watcherState{
		debounce: 100 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
	}

	callCount := 0
	w.debounceEvent("test-plugin", func() { callCount++ })
	w.debounceEvent("test-plugin", func() { callCount++ })
	w.debounceEvent("test-plugin", func() { callCount++ })

	time.Sleep(200 * time.Millisecond)
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (debounced)", callCount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -race ./internal/pluginruntime/ -run TestWatcher -v`
Expected: FAIL with "undefined: watcherState"

- [ ] **Step 3: Implement file watcher**

```go
// internal/pluginruntime/watcher.go
package pluginruntime

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watcherState manages debounced file system events.
type watcherState struct {
	debounce time.Duration
	timers   map[string]*time.Timer
}

// debounceEvent fires fn after the debounce period for the given key.
func (w *watcherState) debounceEvent(key string, fn func()) {
	if timer, ok := w.timers[key]; ok {
		timer.Stop()
	}
	w.timers[key] = time.AfterFunc(w.debounce, fn)
}

// startWatcher begins watching the plugins directory for changes.
func (g *Gateway) startWatcher() error {
	if g.cfg.PluginsDir == "" {
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(g.cfg.PluginsDir); err != nil {
		watcher.Close()
		return err
	}

	ws := &watcherState{
		debounce: g.cfg.FileWatchDebounce,
		timers:   make(map[string]*time.Timer),
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer watcher.Close()

		for {
			select {
			case <-g.ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				g.handleFsEvent(event, ws)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				g.logger.Error().Err(err).Msg("file watcher error")
			}
		}
	}()

	return nil
}

// handleFsEvent processes a file system event.
func (g *Gateway) handleFsEvent(event fsnotify.Event, ws *watcherState) {
	// Only care about plugin.yaml files.
	if !isPluginManifest(event.Name) {
		return
	}

	pluginName := extractPluginName(event.Name)
	if pluginName == "" {
		return
	}

	switch {
	case event.Op&(fsnotify.Create|fsnotify.Write) != 0:
		ws.debounceEvent(pluginName, func() {
			g.logger.Info().Str("plugin", pluginName).Msg("plugin manifest changed, reloading")
			if err := g.ReloadPlugin(pluginName); err != nil {
				g.logger.Error().Err(err).Str("plugin", pluginName).Msg("failed to reload plugin")
			}
		})
	case event.Op&fsnotify.Remove != 0:
		ws.debounceEvent(pluginName, func() {
			g.logger.Info().Str("plugin", pluginName).Msg("plugin manifest removed, unloading")
			g.mu.Lock()
			if p, ok := g.plugins[pluginName]; ok {
				g.stopPlugin(pluginName, p)
				delete(g.plugins, pluginName)
			}
			g.mu.Unlock()
		})
	}
}

// isPluginManifest checks if the path is a plugin.yaml file.
func isPluginManifest(path string) bool {
	return filepath.Base(path) == "plugin.yaml"
}

// extractPluginName extracts the plugin name from a manifest path.
// For /etc/opsagent/plugins/my-plugin/plugin.yaml -> "my-plugin"
// For /etc/opsagent/plugins/plugin.yaml -> ""
func extractPluginName(manifestPath string) string {
	dir := filepath.Dir(manifestPath)
	parentDir := filepath.Dir(dir)
	if parentDir == filepath.Dir(manifestPath) {
		return ""
	}
	return filepath.Base(dir)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./internal/pluginruntime/ -run TestWatcher -v`
Expected: PASS

- [ ] **Step 5: Wire watcher into Gateway.Start**

Add to `Gateway.Start()` after starting health check:

```go
if err := g.startWatcher(); err != nil {
	g.logger.Error().Err(err).Msg("failed to start file watcher")
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/pluginruntime/watcher.go internal/pluginruntime/watcher_test.go internal/pluginruntime/gateway.go
git commit -m "feat(pluginruntime): add file watcher for plugin hot-reload"
```

---

### Task 8: Config Integration

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add PluginGatewayConfig to Config struct**

Add to `internal/config/config.go`:

```go
// PluginGatewayConfig manages custom plugin discovery and lifecycle.
type PluginGatewayConfig struct {
	Enabled                 bool                           `mapstructure:"enabled"`
	PluginsDir              string                         `mapstructure:"plugins_dir"`
	StartupTimeoutSeconds   int                            `mapstructure:"startup_timeout_seconds"`
	HealthCheckIntervalSecs int                            `mapstructure:"health_check_interval_seconds"`
	MaxRestarts             int                            `mapstructure:"max_restarts"`
	RestartBackoffSeconds   int                            `mapstructure:"restart_backoff_seconds"`
	FileWatchDebounceSecs   int                            `mapstructure:"file_watch_debounce_seconds"`
	PluginConfigs           map[string]map[string]interface{} `mapstructure:"plugin_configs"`
}
```

Add field to `Config` struct:

```go
PluginGateway PluginGatewayConfig `mapstructure:"plugin_gateway"`
```

- [ ] **Step 2: Add defaults in Load()**

Add to the defaults section in `Load()`:

```go
v.SetDefault("plugin_gateway.enabled", false)
v.SetDefault("plugin_gateway.plugins_dir", "/etc/opsagent/plugins")
v.SetDefault("plugin_gateway.startup_timeout_seconds", 10)
v.SetDefault("plugin_gateway.health_check_interval_seconds", 30)
v.SetDefault("plugin_gateway.max_restarts", 3)
v.SetDefault("plugin_gateway.restart_backoff_seconds", 5)
v.SetDefault("plugin_gateway.file_watch_debounce_seconds", 2)
```

- [ ] **Step 3: Add validation in Validate()**

Add to `Validate()`:

```go
// PluginGateway validation (only when enabled).
if c.PluginGateway.Enabled {
	if strings.TrimSpace(c.PluginGateway.PluginsDir) == "" {
		return fmt.Errorf("plugin_gateway.plugins_dir is required when plugin_gateway.enabled=true")
	}
	if c.PluginGateway.StartupTimeoutSeconds <= 0 {
		return fmt.Errorf("plugin_gateway.startup_timeout_seconds must be > 0")
	}
	if c.PluginGateway.HealthCheckIntervalSecs <= 0 {
		return fmt.Errorf("plugin_gateway.health_check_interval_seconds must be > 0")
	}
	if c.PluginGateway.MaxRestarts < 0 {
		return fmt.Errorf("plugin_gateway.max_restarts must be >= 0")
	}
}
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add PluginGatewayConfig with defaults and validation"
```

---

### Task 9: Agent Integration

**Files:**
- Modify: `internal/app/agent.go`

- [ ] **Step 1: Add pluginGateway field to Agent**

Add to the `Agent` struct:

```go
pluginGateway PluginGateway
```

- [ ] **Step 2: Build Gateway in NewAgent**

Add after building plugin runtime:

```go
// Build plugin gateway if enabled and not injected.
if a.pluginGateway == nil && cfg.PluginGateway.Enabled {
	gw := pluginruntime.NewGateway(pluginruntime.GatewayConfig{
		PluginsDir:          cfg.PluginGateway.PluginsDir,
		StartupTimeout:      time.Duration(cfg.PluginGateway.StartupTimeoutSeconds) * time.Second,
		HealthCheckInterval: time.Duration(cfg.PluginGateway.HealthCheckIntervalSecs) * time.Second,
		MaxRestarts:         cfg.PluginGateway.MaxRestarts,
		RestartBackoff:      time.Duration(cfg.PluginGateway.RestartBackoffSeconds) * time.Second,
		FileWatchDebounce:   time.Duration(cfg.PluginGateway.FileWatchDebounceSecs) * time.Second,
		PluginConfigs:       cfg.PluginGateway.PluginConfigs,
	}, log)
	a.pluginGateway = gw
}
```

- [ ] **Step 3: Register gateway task handlers**

Add to `registerTaskHandlers()`:

```go
// Register gateway plugin task handlers dynamically.
if gw, ok := a.pluginGateway.(*pluginruntime.Gateway); ok {
	gw.OnPluginLoaded(func(name string, taskTypes []string) {
		for _, tt := range taskTypes {
			fullType := pluginruntime.FullTaskType(name, tt)
			dispatcher.Register(fullType, func(ctx context.Context, t task.AgentTask) (any, error) {
				return a.executeGatewayTask(ctx, t)
			})
			a.log.Info().Str("task_type", fullType).Msg("registered gateway task handler")
		}
	})
	gw.OnPluginUnloaded(func(name string, taskTypes []string) {
		for _, tt := range taskTypes {
			fullType := pluginruntime.FullTaskType(name, tt)
			dispatcher.Unregister(fullType)
			a.log.Info().Str("task_type", fullType).Msg("unregistered gateway task handler")
		}
	})
}
```

- [ ] **Step 4: Add executeGatewayTask method**

```go
func (a *Agent) executeGatewayTask(ctx context.Context, t task.AgentTask) (any, error) {
	if a.shuttingDown.Load() {
		return nil, fmt.Errorf("agent is shutting down")
	}
	if a.pluginGateway == nil {
		return nil, fmt.Errorf("plugin gateway is not enabled")
	}

	taskID := t.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("gw-%d", time.Now().UnixNano())
	}

	deadline := time.Now().Add(30 * time.Second).UnixMilli()
	return a.pluginGateway.ExecuteTask(ctx, pluginruntime.TaskRequest{
		TaskID:     taskID,
		Type:       t.Type,
		DeadlineMS: deadline,
		Payload:    t.Payload,
		Chunking: pluginruntime.ChunkingConfig{
			Enabled:       true,
			MaxChunkBytes: a.cfg.Plugin.ChunkSizeBytes,
			MaxTotalBytes: a.cfg.Plugin.MaxResultBytes,
		},
	})
}
```

- [ ] **Step 5: Wire Gateway into startSubsystems**

Add to `startSubsystems()`:

```go
if a.pluginGateway != nil {
	if err := a.pluginGateway.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start plugin gateway: %w", err)
	}
}
```

- [ ] **Step 6: Wire Gateway into shutdown**

Add to `shutdown()` after stopping plugin runtime:

```go
// 5b. Stop plugin gateway.
if a.pluginGateway != nil {
	if err := a.pluginGateway.Stop(ctx); err != nil {
		a.log.Error().Err(err).Msg("failed to stop plugin gateway")
	}
}
```

- [ ] **Step 7: Add WithPluginGateway option**

Add to `internal/app/options.go`:

```go
func WithPluginGateway(gw PluginGateway) Option {
	return func(a *Agent) { a.pluginGateway = gw }
}
```

- [ ] **Step 8: Verify compilation**

Run: `go build ./internal/app/`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/app/agent.go internal/app/interfaces.go internal/app/options.go
git commit -m "feat(app): wire PluginGateway into Agent lifecycle"
```

---

### Task 10: Go SDK

**Files:**
- Create: `sdk/plugin/go.mod`
- Create: `sdk/plugin/handler.go`
- Create: `sdk/plugin/protocol.go`
- Create: `sdk/plugin/chunking.go`
- Create: `sdk/plugin/serve.go`
- Create: `sdk/plugin/serve_test.go`

- [ ] **Step 1: Create Go module**

```go
// sdk/plugin/go.mod
module github.com/cy77cc/opsagent/sdk/plugin

go 1.21
```

- [ ] **Step 2: Write SDK tests**

```go
// sdk/plugin/serve_test.go
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testHandler struct {
	initCalled    bool
	shutdownErr   error
	taskTypes     []string
	executeFunc   func(ctx context.Context, req *TaskRequest) (*TaskResponse, error)
}

func (h *testHandler) Init(cfg map[string]interface{}) error {
	h.initCalled = true
	return nil
}

func (h *testHandler) TaskTypes() []string {
	if h.taskTypes == nil {
		return []string{"test"}
	}
	return h.taskTypes
}

func (h *testHandler) Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error) {
	if h.executeFunc != nil {
		return h.executeFunc(ctx, req)
	}
	return &TaskResponse{
		TaskID: req.TaskID,
		Status: "ok",
		Data:   map[string]string{"echo": "hello"},
	}, nil
}

func (h *testHandler) Shutdown(ctx context.Context) error {
	return h.shutdownErr
}

func (h *testHandler) HealthCheck(ctx context.Context) error {
	return nil
}

func TestServe_PingPong(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	os.Setenv("OPSAGENT_PLUGIN_SOCKET", socketPath)
	defer os.Unsetenv("OPSAGENT_PLUGIN_SOCKET")

	handler := &testHandler{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(handler)
	}()

	// Wait for socket to appear.
	time.Sleep(200 * time.Millisecond)

	// Send ping.
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := rpcRequest{
		ID:     "ping-1",
		Method: "ping",
	}
	json.NewEncoder(conn).Encode(req)

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read pong: %v", err)
	}

	var resp rpcResponse
	json.Unmarshal(line, &resp)
	if resp.Result == nil {
		t.Fatal("expected result in pong")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit")
	}
}

func TestServe_ExecuteTask(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	os.Setenv("OPSAGENT_PLUGIN_SOCKET", socketPath)
	defer os.Unsetenv("OPSAGENT_PLUGIN_SOCKET")

	handler := &testHandler{
		executeFunc: func(_ context.Context, req *TaskRequest) (*TaskResponse, error) {
			return &TaskResponse{
				TaskID: req.TaskID,
				Status: "ok",
				Data:   "result-data",
			}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go Serve(handler)
	time.Sleep(200 * time.Millisecond)

	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := rpcRequest{
		ID:     "task-1",
		Method: "execute_task",
		Params: TaskRequest{
			TaskID:   "task-1",
			TaskType: "test/echo",
			Params:   map[string]interface{}{"key": "value"},
		},
	}
	json.NewEncoder(conn).Encode(req)

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	var resp rpcResponse
	json.Unmarshal(line, &resp)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
	if resp.Result.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Result.Status, "ok")
	}

	cancel()
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd sdk/plugin && go test -race ./...`
Expected: FAIL with compilation errors

- [ ] **Step 4: Implement protocol types**

```go
// sdk/plugin/protocol.go
package plugin

// TaskRequest is the task request from the agent.
type TaskRequest struct {
	TaskID   string                 `json:"task_id"`
	TaskType string                 `json:"task_type"`
	Params   map[string]interface{} `json:"params"`
	Deadline int64                  `json:"deadline_ms"`
}

// TaskResponse is the task response to the agent.
type TaskResponse struct {
	TaskID string      `json:"task_id"`
	Status string      `json:"status"` // "ok", "error"
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// Chunk carries a slice of large output.
type Chunk struct {
	Seq     int    `json:"seq"`
	EOF     bool   `json:"eof"`
	DataB64 string `json:"data_b64"`
}

// TaskStats contains execution statistics.
type TaskStats struct {
	DurationMS   int64 `json:"duration_ms"`
	CPUMS        int64 `json:"cpu_ms,omitempty"`
	MemPeakBytes int64 `json:"mem_peak_bytes,omitempty"`
}

type rpcRequest struct {
	ID     string      `json:"id"`
	Method string      `json:"method"`
	Params TaskRequest `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	ID     string        `json:"id"`
	Result interface{}   `json:"result,omitempty"`
	Error  *rpcError     `json:"error,omitempty"`
}
```

- [ ] **Step 5: Implement handler interface**

```go
// sdk/plugin/handler.go
package plugin

import (
	"context"
)

// Handler is the interface plugins must implement.
type Handler interface {
	// Init initializes the plugin with configuration.
	Init(cfg map[string]interface{}) error

	// TaskTypes returns the task types this plugin handles (without prefix).
	TaskTypes() []string

	// Execute processes a task request.
	Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error)

	// Shutdown performs graceful cleanup.
	Shutdown(ctx context.Context) error

	// HealthCheck verifies the plugin is healthy.
	HealthCheck(ctx context.Context) error
}
```

- [ ] **Step 6: Implement chunking**

```go
// sdk/plugin/chunking.go
package plugin

import (
	"encoding/base64"
)

// ChunkSize is the default max chunk size in bytes.
const ChunkSize = 256 * 1024

// ChunkOutput splits a large output string into base64-encoded chunks.
func ChunkOutput(output string, maxChunkBytes int) []Chunk {
	if maxChunkBytes <= 0 {
		maxChunkBytes = ChunkSize
	}

	data := []byte(output)
	if len(data) <= maxChunkBytes {
		return nil
	}

	var chunks []Chunk
	seq := 1
	for len(data) > 0 {
		end := maxChunkBytes
		if end > len(data) {
			end = len(data)
		}
		chunk := data[:end]
		data = data[end:]

		chunks = append(chunks, Chunk{
			Seq:     seq,
			EOF:     len(data) == 0,
			DataB64: base64.StdEncoding.EncodeToString(chunk),
		})
		seq++
	}
	return chunks
}
```

- [ ] **Step 7: Implement Serve function**

```go
// sdk/plugin/serve.go
package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Serve starts the plugin server, blocking until shutdown.
func Serve(handler Handler) error {
	return ServeWithOptions(handler)
}

// ServeOptions configures the plugin server.
type ServeOptions struct {
	Logger          *slog.Logger
	GracefulTimeout time.Duration
}

// Option configures ServeWithOptions.
type Option func(*ServeOptions)

// WithLogger sets the logger for the plugin server.
func WithLogger(logger *slog.Logger) Option {
	return func(o *ServeOptions) { o.Logger = logger }
}

// WithGracefulTimeout sets the graceful shutdown timeout.
func WithGracefulTimeout(d time.Duration) Option {
	return func(o *ServeOptions) { o.GracefulTimeout = d }
}

// ServeWithOptions starts the plugin server with options.
func ServeWithOptions(handler Handler, opts ...Option) error {
	cfg := &ServeOptions{
		Logger:          slog.Default(),
		GracefulTimeout: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	socketPath := os.Getenv("OPSAGENT_PLUGIN_SOCKET")
	if socketPath == "" {
		return fmt.Errorf("OPSAGENT_PLUGIN_SOCKET environment variable is not set")
	}

	if err := handler.Init(nil); err != nil {
		return fmt.Errorf("handler init: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	defer func() {
		listener.Close()
		os.Remove(socketPath)
	}()

	cfg.Logger.Info("plugin started", "socket", socketPath, "task_types", handler.TaskTypes())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cfg.Logger.Info("received shutdown signal")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.GracefulTimeout)
		defer shutdownCancel()
		handler.Shutdown(shutdownCtx)
		cancel()
		listener.Close()
	}()

	// Accept loop.
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				cfg.Logger.Error("accept error", "error", err)
				continue
			}
		}
		go handleConnection(conn, handler, cfg.Logger)
	}
}

func handleConnection(conn net.Conn, handler Handler, logger *slog.Logger) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		logger.Error("read error", "error", err)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeError(conn, req.ID, -32700, "parse error")
		return
	}

	switch req.Method {
	case "ping":
		writeResult(conn, req.ID, "pong")
	case "execute_task":
		resp, err := handler.Execute(context.Background(), &req.Params)
		if err != nil {
			writeError(conn, req.ID, -32000, err.Error())
			return
		}
		writeResult(conn, req.ID, resp)
	default:
		writeError(conn, req.ID, -32601, "method not found: "+req.Method)
	}
}

func writeResult(conn net.Conn, id string, result interface{}) {
	resp := rpcResponse{ID: id, Result: result}
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}

func writeError(conn net.Conn, id string, code int, message string) {
	resp := rpcResponse{
		ID:    id,
		Error: &rpcError{Code: code, Message: message},
	}
	data, _ := json.Marshal(resp)
	conn.Write(append(data, '\n'))
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `cd sdk/plugin && go test -race ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add sdk/plugin/
git commit -m "feat(sdk): add Go plugin SDK with Handler interface and Serve function"
```

---

### Task 11: Rust SDK

**Files:**
- Create: `sdk/opsagent-plugin/Cargo.toml`
- Create: `sdk/opsagent-plugin/src/lib.rs`
- Create: `sdk/opsagent-plugin/src/protocol.rs`
- Create: `sdk/opsagent-plugin/src/error.rs`
- Create: `sdk/opsagent-plugin/tests/integration.rs`

- [ ] **Step 1: Create Cargo.toml**

```toml
[package]
name = "opsagent-plugin"
version = "0.1.0"
edition = "2021"
description = "SDK for writing OpsAgent custom plugins"

[dependencies]
tokio = { version = "1", features = ["full"] }
serde = { version = "1", features = ["derive"] }
serde_json = "1"
async-trait = "0.1"
tracing = "0.1"
thiserror = "1"

[dev-dependencies]
tokio-test = "0.4"
```

- [ ] **Step 2: Implement error types**

```rust
// sdk/opsagent-plugin/src/error.rs
use thiserror::Error;

#[derive(Error, Debug)]
pub enum PluginError {
    #[error("configuration error: {0}")]
    Config(String),
    #[error("execution error: {0}")]
    Execution(String),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),
}

pub type Result<T> = std::result::Result<T, PluginError>;
```

- [ ] **Step 3: Implement protocol types**

```rust
// sdk/opsagent-plugin/src/protocol.rs
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Deserialize)]
pub struct RpcRequest {
    pub id: String,
    pub method: String,
    #[serde(default)]
    pub params: TaskRequest,
}

#[derive(Debug, Serialize)]
pub struct RpcResponse {
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<RpcError>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RpcError {
    pub code: i32,
    pub message: String,
}

#[derive(Debug, Deserialize, Default)]
pub struct TaskRequest {
    #[serde(default)]
    pub task_id: String,
    #[serde(default)]
    pub task_type: String,
    #[serde(default)]
    pub params: Value,
    #[serde(default)]
    pub deadline_ms: i64,
}

#[derive(Debug, Serialize)]
pub struct TaskResponse {
    pub task_id: String,
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl RpcResponse {
    pub fn success(id: &str, result: Value) -> Self {
        Self {
            id: id.to_string(),
            result: Some(result),
            error: None,
        }
    }

    pub fn error(id: &str, code: i32, message: &str) -> Self {
        Self {
            id: id.to_string(),
            result: None,
            error: Some(RpcError {
                code,
                message: message.to_string(),
            }),
        }
    }

    pub fn to_json_line(&self) -> Vec<u8> {
        let mut data = serde_json::to_vec(self).unwrap_or_default();
        data.push(b'\n');
        data
    }
}
```

- [ ] **Step 4: Implement Plugin trait and serve function**

```rust
// sdk/opsagent-plugin/src/lib.rs
pub mod error;
pub mod protocol;

use async_trait::async_trait;
use error::{PluginError, Result};
use protocol::{RpcRequest, RpcResponse, TaskRequest, TaskResponse};
use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixListener;
use tracing::{error, info};

#[async_trait]
pub trait Plugin: Send + Sync {
    fn task_types(&self) -> Vec<String>;

    async fn init(&mut self, config: Value) -> Result<()>;

    async fn execute(&self, request: TaskRequest) -> Result<TaskResponse>;

    async fn shutdown(&mut self) -> Result<()>;

    async fn health_check(&self) -> Result<()>;
}

pub struct ServeOptions {
    pub graceful_timeout: std::time::Duration,
}

impl Default for ServeOptions {
    fn default() -> Self {
        Self {
            graceful_timeout: std::time::Duration::from_secs(10),
        }
    }
}

pub async fn serve<P: Plugin + 'static>(plugin: P) -> Result<()> {
    serve_with_options(plugin, ServeOptions::default()).await
}

pub async fn serve_with_options<P: Plugin + 'static>(
    mut plugin: P,
    options: ServeOptions,
) -> Result<()> {
    let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
        .map_err(|_| PluginError::Config("OPSAGENT_PLUGIN_SOCKET not set".into()))?;

    plugin.init(Value::Null).await?;

    // Remove existing socket file.
    let _ = std::fs::remove_file(&socket_path);

    let listener = UnixListener::bind(&socket_path)
        .map_err(|e| PluginError::Config(format!("bind socket {}: {}", socket_path, e)))?;

    info!(socket = %socket_path, task_types = ?plugin.task_types(), "plugin started");

    let plugin = std::sync::Arc::new(plugin);

    // Signal handling.
    let (shutdown_tx, mut shutdown_rx) = tokio::sync::watch::channel(false);
    let socket_path_clone = socket_path.clone();
    tokio::spawn(async move {
        let _ = tokio::signal::ctrl_c().await;
        info!("received shutdown signal");
        let _ = shutdown_tx.send(true);
        let _ = std::fs::remove_file(&socket_path_clone);
    });

    loop {
        tokio::select! {
            result = listener.accept() => {
                match result {
                    Ok((stream, _)) => {
                        let plugin = plugin.clone();
                        tokio::spawn(async move {
                            if let Err(e) = handle_connection(stream, &*plugin).await {
                                error!(error = %e, "connection error");
                            }
                        });
                    }
                    Err(e) => {
                        error!(error = %e, "accept error");
                    }
                }
            }
            _ = shutdown_rx.changed() => {
                info!("shutting down");
                let mut p = plugin.as_ref();
                // Note: can't call &mut self on Arc, so shutdown is best-effort
                let _ = std::fs::remove_file(&socket_path);
                return Ok(());
            }
        }
    }
}

async fn handle_connection(
    stream: tokio::net::UnixStream,
    plugin: &dyn Plugin,
) -> Result<()> {
    let (reader, mut writer) = stream.into_split();
    let mut reader = BufReader::new(reader);
    let mut line = String::new();

    reader.read_line(&mut line).await?;

    let request: RpcRequest = serde_json::from_str(&line)?;

    let response = match request.method.as_str() {
        "ping" => RpcResponse::success(&request.id, Value::String("pong".into())),
        "execute_task" => {
            match plugin.execute(request.params).await {
                Ok(resp) => {
                    RpcResponse::success(&request.id, serde_json::to_value(resp).unwrap_or_default())
                }
                Err(e) => RpcResponse::error(&request.id, -32000, &e.to_string()),
            }
        }
        _ => RpcResponse::error(&request.id, -32601, &format!("method not found: {}", request.method)),
    };

    writer.write_all(&response.to_json_line()).await?;
    Ok(())
}
```

- [ ] **Step 5: Write integration test**

```rust
// sdk/opsagent-plugin/tests/integration.rs
use async_trait::async_trait;
use opsagent_plugin::error::Result;
use opsagent_plugin::protocol::{TaskRequest, TaskResponse};
use opsagent_plugin::Plugin;
use serde_json::Value;

struct EchoPlugin;

#[async_trait]
impl Plugin for EchoPlugin {
    fn task_types(&self) -> Vec<String> {
        vec!["echo".into()]
    }

    async fn init(&mut self, _config: Value) -> Result<()> {
        Ok(())
    }

    async fn execute(&self, request: TaskRequest) -> Result<TaskResponse> {
        Ok(TaskResponse {
            task_id: request.task_id,
            status: "ok".into(),
            data: Some(request.params),
            error: None,
        })
    }

    async fn shutdown(&mut self) -> Result<()> {
        Ok(())
    }

    async fn health_check(&self) -> Result<()> {
        Ok(())
    }
}

#[tokio::test]
async fn test_echo_plugin() {
    let plugin = EchoPlugin;
    let request = TaskRequest {
        task_id: "test-1".into(),
        task_type: "echo".into(),
        params: serde_json::json!({"key": "value"}),
        deadline_ms: 0,
    };
    let response = plugin.execute(request).await.unwrap();
    assert_eq!(response.status, "ok");
    assert_eq!(response.task_id, "test-1");
}
```

- [ ] **Step 6: Run tests**

Run: `cd sdk/opsagent-plugin && cargo test`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add sdk/opsagent-plugin/
git commit -m "feat(sdk): add Rust plugin SDK with Plugin trait and serve function"
```

---

### Task 12: Go Example Plugin

**Files:**
- Create: `sdk/examples/go-echo/plugin.yaml`
- Create: `sdk/examples/go-echo/main.go`
- Create: `sdk/examples/go-echo/go.mod`

- [ ] **Step 1: Create plugin manifest**

```yaml
# sdk/examples/go-echo/plugin.yaml
name: go-echo
version: "1.0.0"
description: "Echo plugin for testing the SDK"
author: "opsagent@example.com"
runtime: process
binary_path: ./go-echo
task_types:
  - echo
limits:
  max_memory_mb: 64
  timeout_seconds: 10
```

- [ ] **Step 2: Create go.mod**

```
// sdk/examples/go-echo/go.mod
module go-echo

go 1.21

require github.com/cy77cc/opsagent/sdk/plugin v0.0.0

replace github.com/cy77cc/opsagent/sdk/plugin => ../../plugin
```

- [ ] **Step 3: Implement echo plugin**

```go
// sdk/examples/go-echo/main.go
package main

import (
	"context"
	"log"

	"github.com/cy77cc/opsagent/sdk/plugin"
)

type EchoHandler struct{}

func (h *EchoHandler) Init(cfg map[string]interface{}) error {
	log.Println("echo plugin initialized")
	return nil
}

func (h *EchoHandler) TaskTypes() []string {
	return []string{"echo"}
}

func (h *EchoHandler) Execute(_ context.Context, req *plugin.TaskRequest) (*plugin.TaskResponse, error) {
	log.Printf("executing task %s with params: %v", req.TaskID, req.Params)
	return &plugin.TaskResponse{
		TaskID: req.TaskID,
		Status: "ok",
		Data: map[string]interface{}{
			"echo":  req.Params,
			"task":  req.TaskType,
		},
	}, nil
}

func (h *EchoHandler) Shutdown(_ context.Context) error {
	log.Println("echo plugin shutting down")
	return nil
}

func (h *EchoHandler) HealthCheck(_ context.Context) error {
	return nil
}

func main() {
	if err := plugin.Serve(&EchoHandler{}); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
```

- [ ] **Step 4: Build example**

Run: `cd sdk/examples/go-echo && go build -o go-echo`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sdk/examples/go-echo/
git commit -m "feat(sdk): add Go echo example plugin"
```

---

### Task 13: Rust Example Plugin

**Files:**
- Create: `sdk/examples/rust-audit/plugin.yaml`
- Create: `sdk/examples/rust-audit/Cargo.toml`
- Create: `sdk/examples/rust-audit/src/main.rs`

- [ ] **Step 1: Create plugin manifest**

```yaml
# sdk/examples/rust-audit/plugin.yaml
name: rust-audit
version: "1.0.0"
description: "System audit plugin for testing the Rust SDK"
author: "opsagent@example.com"
runtime: process
binary_path: ./target/release/rust-audit
task_types:
  - audit
limits:
  max_memory_mb: 128
  timeout_seconds: 30
```

- [ ] **Step 2: Create Cargo.toml**

```toml
[package]
name = "rust-audit"
version = "0.1.0"
edition = "2021"

[dependencies]
opsagent-plugin = { path = "../../opsagent-plugin" }
tokio = { version = "1", features = ["full"] }
serde_json = "1"
async-trait = "0.1"
tracing = "0.1"
tracing-subscriber = "0.3"
```

- [ ] **Step 3: Implement audit plugin**

```rust
// sdk/examples/rust-audit/src/main.rs
use async_trait::async_trait;
use opsagent_plugin::error::Result;
use opsagent_plugin::protocol::{TaskRequest, TaskResponse};
use opsagent_plugin::Plugin;
use serde_json::{json, Value};

struct AuditPlugin;

#[async_trait]
impl Plugin for AuditPlugin {
    fn task_types(&self) -> Vec<String> {
        vec!["audit".into()]
    }

    async fn init(&mut self, _config: Value) -> Result<()> {
        tracing_subscriber::fmt::init();
        tracing::info!("audit plugin initialized");
        Ok(())
    }

    async fn execute(&self, request: TaskRequest) -> Result<TaskResponse> {
        tracing::info!(task_id = %request.task_id, "executing audit");

        let disk_usage = get_disk_usage();
        let memory_info = get_memory_info();

        Ok(TaskResponse {
            task_id: request.task_id,
            status: "ok".into(),
            data: Some(json!({
                "disk": disk_usage,
                "memory": memory_info,
                "timestamp": chrono::Utc::now().to_rfc3339(),
            })),
            error: None,
        })
    }

    async fn shutdown(&mut self) -> Result<()> {
        tracing::info!("audit plugin shutting down");
        Ok(())
    }

    async fn health_check(&self) -> Result<()> {
        Ok(())
    }
}

fn get_disk_usage() -> Value {
    // Read /proc/mounts and stat filesystem
    json!({"status": "ok", "note": "disk check placeholder"})
}

fn get_memory_info() -> Value {
    // Read /proc/meminfo
    match std::fs::read_to_string("/proc/meminfo") {
        Ok(content) => {
            let total = parse_meminfo_field(&content, "MemTotal");
            let available = parse_meminfo_field(&content, "MemAvailable");
            json!({
                "total_kb": total,
                "available_kb": available,
            })
        }
        Err(e) => json!({"error": e.to_string()}),
    }
}

fn parse_meminfo_field(content: &str, field: &str) -> Option<u64> {
    for line in content.lines() {
        if line.starts_with(field) {
            return line.split_whitespace()
                .nth(1)
                .and_then(|s| s.parse().ok());
        }
    }
    None
}

#[tokio::main]
async fn main() -> Result<()> {
    opsagent_plugin::serve(AuditPlugin).await
}
```

- [ ] **Step 4: Build example**

Run: `cd sdk/examples/rust-audit && cargo build --release`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add sdk/examples/rust-audit/
git commit -m "feat(sdk): add Rust audit example plugin"
```

---

### Task 14: End-to-End Integration Test

**Files:**
- Create: `internal/integration/plugin_gateway_test.go`

- [ ] **Step 1: Write integration test**

```go
//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/rs/zerolog"
)

func TestCustomPluginGateway_EndToEnd(t *testing.T) {
	// Build the echo plugin.
	echoDir := filepath.Join("..", "..", "sdk", "examples", "go-echo")
	build := exec.Command("go", "build", "-o", filepath.Join(echoDir, "go-echo"), ".")
	build.Dir = echoDir
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build echo plugin: %v\n%s", err, out)
	}
	defer os.Remove(filepath.Join(echoDir, "go-echo"))

	// Create a temp plugins directory with the echo plugin.
	pluginsDir := t.TempDir()
	pluginDir := filepath.Join(pluginsDir, "go-echo")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Copy manifest.
	manifestSrc := filepath.Join(echoDir, "plugin.yaml")
	manifestData, _ := os.ReadFile(manifestSrc)
	// Update binary_path to absolute.
	manifest := string(manifestData)
	manifest = replaceBinaryPath(manifest, filepath.Join(pluginDir, "go-echo"))
	os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(manifest), 0644)

	// Copy binary.
	binSrc := filepath.Join(echoDir, "go-echo")
	binDst := filepath.Join(pluginDir, "go-echo")
	copyFile(binSrc, binDst)

	// Create and start Gateway.
	cfg := pluginruntime.GatewayConfig{
		PluginsDir:          pluginsDir,
		StartupTimeout:      10 * time.Second,
		HealthCheckInterval: 60 * time.Second,
		MaxRestarts:         0,
		RestartBackoff:      5 * time.Second,
		FileWatchDebounce:   2 * time.Second,
	}
	gw := pluginruntime.NewGateway(cfg, zerolog.Nop())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := gw.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer gw.Stop(ctx)

	// Verify plugin is loaded.
	plugins := gw.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].Name != "go-echo" {
		t.Errorf("plugin name = %q, want %q", plugins[0].Name, "go-echo")
	}
	if plugins[0].Status != pluginruntime.PluginStatusRunning {
		t.Errorf("plugin status = %q, want %q", plugins[0].Status, pluginruntime.PluginStatusRunning)
	}

	// Execute a task.
	resp, err := gw.ExecuteTask(ctx, pluginruntime.TaskRequest{
		TaskID:     "e2e-test-1",
		Type:       "go-echo/echo",
		DeadlineMS: time.Now().Add(10 * time.Second).UnixMilli(),
		Payload:    map[string]any{"hello": "world"},
		Chunking:   pluginruntime.ChunkingConfig{Enabled: true, MaxChunkBytes: 256 * 1024, MaxTotalBytes: 8 * 1024 * 1024},
	})
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("response status = %q, want %q", resp.Status, "ok")
	}
}

func replaceBinaryPath(yaml, newPath string) string {
	// Simple replacement for the binary_path field.
	lines := splitLines(yaml)
	for i, line := range lines {
		if len(line) > 12 && line[:12] == "binary_path:" {
			lines[i] = "binary_path: " + newPath
		}
	}
	return joinLines(lines)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func copyFile(src, dst string) {
	data, _ := os.ReadFile(src)
	os.WriteFile(dst, data, 0755)
}
```

- [ ] **Step 2: Run integration test**

Run: `go test -race -tags=integration ./internal/integration/ -run TestCustomPluginGateway_EndToEnd -v -timeout 60s`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/integration/plugin_gateway_test.go
git commit -m "test: add end-to-end integration test for PluginGateway"
```

---

### Task 15: Final Verification

- [ ] **Step 1: Run all Go tests**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 2: Run all Rust tests**

Run: `cd sdk/opsagent-plugin && cargo test`
Expected: PASS

- [ ] **Step 3: Build all examples**

Run: `cd sdk/examples/go-echo && go build -o go-echo`
Run: `cd sdk/examples/rust-audit && cargo build --release`
Expected: Both PASS

- [ ] **Step 4: Verify go vet**

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 5: Final commit with all remaining changes**

```bash
git add -A
git commit -m "feat: complete custom plugin SDK with Gateway, Go SDK, Rust SDK, and examples"
```
