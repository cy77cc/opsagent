# Spec: 自定义插件 SDK

> 日期: 2026-04-29
> 状态: Draft
> 版本: 2.0 (重构)

## Context

OpsAgent 的内置插系统通过 Go 的 `init()` + registry 模式工作，需要重编译才能添加新插件。用户需要一种方式来编写自定义插件，在运行时加载，无需重编译 Agent。

Spec 5 建立的 UDS JSON-RPC 协议和插件契约是本 spec 的基础。自定义插件使用完全相同的协议与 Agent 通信。

**依赖：** Spec 5（插件契约必须稳定）、Spec 2（配置热更新支持不停机添加插件）

## 目标

1. 插件清单 schema 文档化并验证
2. PluginGateway 统一管理插件生命周期（发现、加载、重启、健康检查、热重载）
3. 插件路由器按 task 类型分发到正确的插件进程
4. Go SDK 和 Rust SDK 可用
5. 至少 2 个示例插件端到端工作
6. 插件可通过文件变更自动热重载
7. 沙箱化可选，manifest 中声明启用

## 设计

### 1. 整体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│  OpsAgent (Go)                                                     │
│                                                                     │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────────────────┐ │
│  │ Task         │  │ Collector    │  │ Sandbox Executor           │ │
│  │ Dispatcher   │  │ Pipeline     │  │ (nsjail)                   │ │
│  └──────┬───┬──┘  └──────────────┘  └────────────────────────────┘ │
│         │   │                                                       │
│    ┌────┘   └──────────────────────┐                               │
│    ▼                               ▼                               │
│  Rust Runtime               PluginGateway                          │
│  (内置插件)                  (自定义插件管理)                        │
│    │                               │                               │
│    │ UDS Socket                    │ 监听插件目录                   │
│    │                               │ 自动发现 plugin.yaml          │
│    ▼                               ▼                               │
│  ┌──────────┐              ┌──────────────────┐                    │
│  │ 6 个内置  │              │ 插件进程 A        │                    │
│  │ handler  │              │ 插件进程 B        │                    │
│  └──────────┘              │ 插件进程 C        │                    │
│                            └──────────────────┘                    │
└─────────────────────────────────────────────────────────────────────┘
```

**关键设计决策：**

| 决策 | 选择 | 理由 |
|------|------|------|
| 插件进程模型 | 独立进程 | 隔离性最好，一个插件崩溃不影响其他 |
| 管理方式 | PluginGateway 统一管理 | 封装生命周期管理，Agent 侧代码简洁 |
| Task type 命名 | 插件名前缀 (`{name}/{type}`) | 避免冲突，全局唯一 |
| 沙箱化 | 可选 | 降低部署复杂度，用户按需启用 |
| SDK 语言 | Go + Rust 同时 | 覆盖主要用户群体 |

### 2. 插件清单 Schema

每个插件有一个 YAML 清单文件：

```yaml
# plugin.yaml
name: "my-custom-plugin"
version: "1.0.0"
description: "Custom system audit plugin"
author: "user@example.com"

runtime: process  # process | wasm (future)

# 插件二进制路径（相对于清单文件或绝对路径）
binary_path: "./my-plugin"

# 环境变量（传递给插件进程）
env:
  LOG_LEVEL: "info"

# 处理的 task 类型（自动加插件名前缀）
# 最终 task type: "my-custom-plugin/audit"
task_types:
  - "audit"
  - "report"

# 插件特定配置的 JSON Schema
config_schema:
  type: object
  properties:
    check_interval_seconds:
      type: integer
      default: 60
    thresholds:
      type: object
      properties:
        cpu_percent:
          type: number
        memory_percent:
          type: number

# 插件运行时配置（用户在 agent config 中覆盖）
config:
  check_interval_seconds: 120

# 系统需求
requirements:
  min_kernel_version: "4.18"
  os: ["linux"]

# 资源限制
limits:
  max_memory_mb: 256
  max_cpu_percent: 50
  max_concurrent_tasks: 5
  timeout_seconds: 30

# 健康检查
health_check:
  interval_seconds: 30
  timeout_seconds: 5

# 沙箱配置（可选）
sandbox:
  enabled: false
  network_access: false
  allowed_paths:
    - "/var/log"
    - "/proc"
```

### 3. PluginGateway

新增 `internal/pluginruntime/gateway.go`，封装所有插件生命周期管理。

```go
type Gateway struct {
    pluginsDir  string
    config      GatewayConfig
    plugins     map[string]*ManagedPlugin  // name → plugin
    routes      map[string]*ManagedPlugin  // task_type → plugin
    mu          sync.RWMutex
    watcher     *fsnotify.Watcher
    logger      zerolog.Logger
}

type GatewayConfig struct {
    PluginsDir            string
    StartupTimeout        time.Duration
    HealthCheckInterval   time.Duration
    MaxRestarts           int
    RestartBackoff        time.Duration
    FileWatchDebounce     time.Duration
}

type ManagedPlugin struct {
    Manifest     *PluginManifest
    Process      *os.Process
    Client       *PluginClient
    SocketPath   string
    Status       PluginStatus
    RestartCount int
    LastHealth   time.Time
    mu           sync.Mutex
}

type PluginStatus string
const (
    PluginStatusStarting PluginStatus = "starting"
    PluginStatusRunning  PluginStatus = "running"
    PluginStatusStopped  PluginStatus = "stopped"
    PluginStatusError    PluginStatus = "error"
    PluginStatusDisabled PluginStatus = "disabled"
)
```

**类型定义：**

```go
// PluginManifest 解析后的插件清单
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
}

type Requirements struct {
    MinKernelVersion string   `yaml:"min_kernel_version"`
    OS               []string `yaml:"os"`
}

type Limits struct {
    MaxMemoryMB        int `yaml:"max_memory_mb"`
    MaxCPUPercent      int `yaml:"max_cpu_percent"`
    MaxConcurrentTasks int `yaml:"max_concurrent_tasks"`
    TimeoutSeconds     int `yaml:"timeout_seconds"`
}

type HealthCheckConfig struct {
    IntervalSeconds int `yaml:"interval_seconds"`
    TimeoutSeconds  int `yaml:"timeout_seconds"`
}

type SandboxConfig struct {
    Enabled       bool     `yaml:"enabled"`
    NetworkAccess bool     `yaml:"network_access"`
    AllowedPaths  []string `yaml:"allowed_paths"`
}

// PluginInfo 是插件的运行时信息
type PluginInfo struct {
    Name          string            `json:"name"`
    Version       string            `json:"version"`
    Status        PluginStatus      `json:"status"`
    TaskTypes     []string          `json:"task_types"`
    SocketPath    string            `json:"socket_path"`
    RestartCount  int               `json:"restart_count"`
    LastHealth    time.Time         `json:"last_health"`
    Uptime        time.Duration     `json:"uptime"`
}
```

**Gateway 接口：**

```go
type PluginGateway interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    ExecuteTask(ctx context.Context, req *TaskRequest) (*TaskResponse, error)
    ListPlugins() []PluginInfo
    GetPlugin(name string) *PluginInfo
    ReloadPlugin(name string) error
    EnablePlugin(name string) error
    DisablePlugin(name string) error
}
```

**PluginClient 复用：**

Gateway 复用现有 `internal/pluginruntime/client.go` 中的 `PluginClient` 与插件进程通信。每个 `ManagedPlugin` 持有一个独立的 `PluginClient` 实例，连接到该插件的 UDS socket。

**启动流程：**
1. 扫描 `pluginsDir` 下所有 `plugin.yaml` 文件
2. 解析并验证清单
3. 为每个插件生成 socket 路径：`/tmp/opsagent-plugin-{name}.sock`
4. 启动插件进程：`exec.Command(manifest.BinaryPath)`，设置环境变量 `OPSAGENT_PLUGIN_SOCKET`
5. 等待插件连接到 UDS（带超时）
6. 发送 `ping` 验证插件就绪
7. 注册 task type 路由

**健康检查：**
- 定期发送 `ping` 到每个插件
- 插件无响应 → 标记为 error → 触发重启
- 重启采用指数退避，最多重试 `MaxRestarts` 次

**文件变更监听：**
- 使用 `fsnotify` 监听 `pluginsDir` 目录
- 检测到 `plugin.yaml` 变更 → 防抖后重新加载该插件
- 检测到新 `plugin.yaml` → 加载新插件
- 检测到 `plugin.yaml` 删除 → 停止并卸载插件

**路由查询：**
```go
func (g *Gateway) Route(taskType string) (*PluginClient, error)
// taskType 格式: "plugin-name/task-type"
// 解析出插件名，查找对应 ManagedPlugin，返回其 Client
```

### 4. 插件路由器

Task Dispatcher 集成：

```go
// 内置 task type → Rust Runtime
dispatcher.Register("plugin_log_parse", pluginTaskHandler)
dispatcher.Register("plugin_text_process", pluginTaskHandler)
// ...

// 自定义插件 task type → Gateway（动态注册）
gateway.OnPluginLoaded(func(name string, taskTypes []string) {
    for _, tt := range taskTypes {
        fullType := name + "/" + tt
        dispatcher.Register(fullType, gatewayTaskHandler)
    }
})
gateway.OnPluginUnloaded(func(name string, taskTypes []string) {
    for _, tt := range taskTypes {
        fullType := name + "/" + tt
        dispatcher.Unregister(fullType)
    }
})
```

**路由逻辑：**
1. 先查内置 Rust Runtime handler（`plugin_*` 前缀）
2. 再查 Gateway 路由表（`{name}/{type}` 格式）
3. 都没有 → 返回错误 "unknown task type"

### 5. Go SDK

新增 Go module `sdk/plugin/`：

```go
package plugin

// Handler 是插件必须实现的接口
type Handler interface {
    // Init 初始化插件，接收配置
    Init(cfg map[string]interface{}) error

    // TaskTypes 返回此插件处理的 task type 列表（不含前缀）
    TaskTypes() []string

    // Execute 执行任务
    Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error)

    // Shutdown 优雅关闭
    Shutdown(ctx context.Context) error

    // HealthCheck 健康检查
    HealthCheck(ctx context.Context) error
}

// TaskRequest 是平台下发的任务请求
type TaskRequest struct {
    TaskID   string                 `json:"task_id"`
    TaskType string                 `json:"task_type"` // 已带前缀
    Params   map[string]interface{} `json:"params"`
    Deadline time.Time              `json:"deadline"`
}

// TaskResponse 是插件返回的任务结果
type TaskResponse struct {
    TaskID string      `json:"task_id"`
    Status string      `json:"status"` // "ok", "error"
    Data   interface{} `json:"data,omitempty"`
    Error  string      `json:"error,omitempty"`
}

// Serve 启动插件服务，阻塞运行
func Serve(handler Handler) error

// ServeWithOptions 带选项启动
func ServeWithOptions(handler Handler, opts ...Option) error

type Option func(*ServeOptions)
func WithLogger(logger *slog.Logger) Option
func WithGracefulTimeout(d time.Duration) Option
```

**SDK 内部处理：**
1. 从 `OPSAGENT_PLUGIN_SOCKET` 环境变量获取 socket 路径
2. 连接到 UDS
3. 读取 JSON-RPC 请求，分发到 handler
4. 处理分块响应（自动分块大响应）
5. 处理 `ping` 心跳
6. 信号处理（SIGTERM → 调用 `handler.Shutdown()`）

### 6. Rust SDK

新增 Rust crate `sdk/opsagent-plugin/`：

```rust
use opsagent_plugin::{Plugin, TaskRequest, TaskResponse, Result};
use async_trait::async_trait;
use serde_json::Value;

#[async_trait]
pub trait Plugin: Send + Sync {
    /// 返回此插件处理的 task type 列表（不含前缀）
    fn task_types(&self) -> Vec<String>;

    /// 初始化插件
    async fn init(&mut self, config: Value) -> Result<()>;

    /// 执行任务
    async fn execute(&self, request: TaskRequest) -> Result<TaskResponse>;

    /// 优雅关闭
    async fn shutdown(&mut self) -> Result<()>;

    /// 健康检查
    async fn health_check(&self) -> Result<()>;
}

/// 启动插件服务，阻塞运行
pub async fn serve<P: Plugin + 'static>(plugin: P) -> Result<()>

/// 带配置启动
pub async fn serve_with_options<P: Plugin + 'static>(
    plugin: P,
    options: ServeOptions,
) -> Result<()>
```

**依赖：** `tokio`、`serde`、`serde_json`、`async-trait`、`tracing`

**SDK 与现有 Rust Runtime 的关系：**
- SDK 是独立 crate，不依赖 `opsagent-rust-runtime`
- SDK 实现的是 UDS **服务端**（插件进程侧），而 `pluginruntime` 包中的 client.go 是 UDS **客户端**（Agent 侧）
- 现有 Rust Runtime 保持不变，继续处理内置 6 个 task type

### 7. 配置集成

`internal/config/config.go` 新增配置段：

```yaml
plugin_gateway:
  enabled: true
  plugins_dir: "/etc/opsagent/plugins"
  startup_timeout_seconds: 10
  health_check_interval_seconds: 30
  max_restarts: 3
  restart_backoff_seconds: 5
  file_watch_debounce_seconds: 2

  # 插件级配置覆盖（按插件名）
  plugin_configs:
    my-custom-plugin:
      check_interval_seconds: 120
      thresholds:
        cpu_percent: 90
```

**配置结构体：**

```go
type PluginGatewayConfig struct {
    Enabled                 bool                           `yaml:"enabled"`
    PluginsDir              string                         `yaml:"plugins_dir"`
    StartupTimeoutSeconds   int                            `yaml:"startup_timeout_seconds"`
    HealthCheckIntervalSecs int                            `yaml:"health_check_interval_seconds"`
    MaxRestarts             int                            `yaml:"max_restarts"`
    RestartBackoffSeconds   int                            `yaml:"restart_backoff_seconds"`
    FileWatchDebounceSecs   int                            `yaml:"file_watch_debounce_seconds"`
    PluginConfigs           map[string]map[string]interface{} `yaml:"plugin_configs"`
}
```

**配置合并策略：**

插件最终配置 = manifest 中的 `config`（默认值）+ agent config 中的 `plugin_configs.{name}`（用户覆盖）。合并采用浅合并（shallow merge），用户配置覆盖默认值。

```go
func mergePluginConfig(manifestCfg, agentCfg map[string]interface{}) map[string]interface{} {
    result := make(map[string]interface{})
    for k, v := range manifestCfg {
        result[k] = v
    }
    for k, v := range agentCfg {
        result[k] = v  // 覆盖
    }
    return result
}
```

**热重载策略：**
- `plugin_gateway` 配置段标记为 **non-reloadable**（需要重启）
- 插件级配置 (`plugin_configs`) 可通过 Gateway 的 reload API 热更新
- 插件自身的热重载通过文件监听实现，不依赖 config reload

### 8. Agent 接入

修改 `internal/app/agent.go`：

```go
type Agent struct {
    // ... existing fields
    pluginGateway pluginruntime.PluginGateway  // 新增
}

// NewAgent 中：
// 1. 如果 plugin_gateway.enabled，创建 Gateway
// 2. Gateway 启动时自动发现并加载插件

// registerTaskHandlers 中：
// 对于自定义插件 task type，路由到 Gateway.ExecuteTask

// shutdown 中：
// 1. 停止 Gateway（优雅停止所有插件）
```

**Shutdown 顺序：**
1. 标记 shuttingDown
2. 等待活跃任务完成
3. 停止 Scheduler
4. Flush gRPC 缓存
5. 停止 PluginGateway（优雅停止所有插件）
6. 停止 Rust Runtime
7. 关闭 HTTP Server

### 9. 插件沙箱化（可选）

当 manifest 中 `sandbox.enabled: true` 时，Gateway 使用 nsjail 包装插件进程：

```go
func (g *Gateway) startPlugin(manifest *PluginManifest) error {
    cmd := exec.Command(manifest.BinaryPath)
    cmd.Env = append(os.Environ(), "OPSAGENT_PLUGIN_SOCKET="+socketPath)

    if manifest.Sandbox != nil && manifest.Sandbox.Enabled {
        nsjailCfg := g.buildNsjailConfig(manifest)
        cmd = g.wrapWithNsjail(cmd, nsjailCfg)
    }

    return cmd.Start()
}
```

**沙箱配置映射：**
- `limits.max_memory_mb` → nsjail `rlimit_as`
- `limits.max_cpu_percent` → nsjail `cgroup_cpu`
- `sandbox.network_access` → nsjail `net` 配置
- `sandbox.allowed_paths` → nsjail bind mounts

### 10. 示例插件

**Go 示例 — Echo Plugin：**
```
sdk/examples/go-echo/
├── plugin.yaml
├── main.go          # 调用 plugin.Serve(&EchoHandler{})
└── go.mod
```

**Rust 示例 — System Audit Plugin：**
```
sdk/examples/rust-audit/
├── plugin.yaml
├── Cargo.toml
└── src/
    └── main.rs      # 检查磁盘、内存、进程，返回审计结果
```

## 测试要求

### Gateway 测试
- 发现并加载有效插件
- 拒绝无效清单（缺字段、版本格式错误）
- 插件进程崩溃后自动重启
- 超时未连接的处理
- 文件变更触发热重载
- 禁用/启用插件
- 路由查询正确性

### 路由器测试
- 正确路由到内置 Rust Runtime handler
- 正确路由到 Gateway 插件 handler
- 未知 task type 返回错误
- 插件卸载后路由更新

### SDK 测试
- Go SDK: 协议合规、分块、错误处理、信号处理
- Rust SDK: 同上
- 端到端: Agent → Gateway → 插件进程 → 任务执行 → 响应

### 沙箱测试
- 内存限制生效
- CPU 限制生效
- 文件系统隔离
- 网络隔离

## 验证方式

```bash
# Go SDK 测试
cd sdk/plugin && go test -race ./...

# Rust SDK 测试
cd sdk/opsagent-plugin && cargo test

# 示例插件构建
cd sdk/examples/go-echo && go build -o echo-plugin
cd sdk/examples/rust-audit && cargo build

# Gateway 单元测试
go test -race ./internal/pluginruntime/ -run TestGateway

# 端到端测试
go test -race -tags=integration ./internal/integration/ -run TestCustomPlugin
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/pluginruntime/gateway.go` | **新建** — PluginGateway 核心 |
| `internal/pluginruntime/gateway_test.go` | **新建** |
| `internal/pluginruntime/loader.go` | **重构** — 从现有 runtime.go 提取 |
| `internal/pluginruntime/watcher.go` | **新建** — 文件变更监听 |
| `internal/pluginruntime/sandbox.go` | **新建** — 插件沙箱包装 |
| `internal/config/config.go` | **修改** — 新增 plugin_gateway 配置段 |
| `internal/app/agent.go` | **修改** — 接入 PluginGateway |
| `internal/app/interfaces.go` | **修改** — 新增 PluginGateway 接口 |
| `sdk/plugin/` | **新建** — Go SDK module |
| `sdk/plugin/handler.go` | **新建** — Handler 接口 |
| `sdk/plugin/serve.go` | **新建** — Serve 函数 |
| `sdk/plugin/protocol.go` | **新建** — JSON-RPC 类型 |
| `sdk/plugin/*_test.go` | **新建** |
| `sdk/opsagent-plugin/` | **新建** — Rust crate |
| `sdk/opsagent-plugin/src/lib.rs` | **新建** — Plugin trait |
| `sdk/opsagent-plugin/src/serve.rs` | **新建** — serve 函数 |
| `sdk/opsagent-plugin/src/protocol.rs` | **新建** |
| `sdk/opsagent-plugin/tests/` | **新建** |
| `sdk/examples/go-echo/` | **新建** |
| `sdk/examples/rust-audit/` | **新建** |
| `docs/plugin-sdk-guide.md` | **新建** — SDK 使用指南 |
