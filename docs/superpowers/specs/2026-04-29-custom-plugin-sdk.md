# Spec 6: 自定义插件 SDK

## Context

OpsAgent 的内置插系统通过 Go 的 `init()` + registry 模式工作，需要重编译才能添加新插件。用户需要一种方式来编写自定义插件，在运行时加载，无需重编译 Agent。

Spec 5 建立的 UDS JSON-RPC 协议和插件契约是本 spec 的基础。自定义插件使用完全相同的协议与 Agent 通信。

**依赖：** Spec 5（插件契约必须稳定）、Spec 2（配置热更新支持不停机添加插件）

## 目标

1. 插件清单 schema 文档化并验证
2. 插件加载器从目录发现并启动插件
3. 插件路由器按 task 类型分发到正确的插件进程
4. Go SDK 和 Rust SDK 可用
5. 至少 2 个示例插件端到端工作
6. 插件热重载通过配置更新工作
7. 每个插件在独立沙箱进程中运行

## 设计

### 1. 插件清单 Schema

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

# 处理的 task 类型
task_types:
  - "custom_audit"
  - "custom_report"

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

# 系统需求
requirements:
  min_kernel_version: "4.18"
  capabilities: ["net_admin"]
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
```

### 2. 插件加载器

新增 `internal/pluginruntime/loader.go`：

```go
type PluginLoader struct {
    pluginsDir string
    config     PluginConfig
    plugins    map[string]*ManagedPlugin
    mu         sync.RWMutex
}

type ManagedPlugin struct {
    Manifest   *PluginManifest
    Process    *os.Process
    Client     *PluginClient  // UDS JSON-RPC client
    SocketPath string
    Status     PluginStatus   // starting, running, stopped, error
}

// LoadAll 扫描 pluginsDir，加载所有有效插件
func (l *PluginLoader) LoadAll(ctx context.Context) error

// Load 加载单个插件
func (l *PluginLoader) Load(ctx context.Context, manifestPath string) error

// Stop 停止单个插件
func (l *PluginLoader) Stop(name string) error

// StopAll 停止所有插件
func (l *PluginLoader) StopAll(ctx context.Context) error

// HealthCheck 检查所有插件健康状态
func (l *PluginLoader) HealthCheck(ctx context.Context) map[string]error
```

**加载流程：**
1. 扫描 `pluginsDir` 下所有 `plugin.yaml` 文件
2. 解析并验证清单
3. 为每个插件生成唯一的 UDS socket 路径：`/tmp/opsagent-plugin-{name}-{random}.sock`
4. 启动插件进程：`exec.Command(manifest.BinaryPath)`，设置环境变量 `OPSAGENT_PLUGIN_SOCKET`
5. 等待插件连接到 UDS（带超时）
6. 发送 `ping` 验证插件就绪
7. 创建 `PluginClient` 用于后续通信

### 3. 插件路由器

新增 `internal/pluginruntime/router.go`：

```go
type PluginRouter struct {
    builtinHandlers map[string]TaskHandler    // 内置 handler
    pluginHandlers  map[string]*PluginRoute   // 插件 handler
    mu              sync.RWMutex
}

type PluginRoute struct {
    PluginName string
    Client     *PluginClient
}

// Register 注册插件的 task types
func (r *PluginRouter) Register(manifest *PluginManifest, client *PluginClient)

// Unregister 注销插件
func (r *PluginRouter) Unregister(pluginName string)

// Route 根据 task type 找到对应的 handler
func (r *PluginRouter) Route(taskType string) (TaskHandler, error)
```

**路由逻辑：**
1. 先查 `builtinHandlers`（内置 Rust 运行时的 handler）
2. 再查 `pluginHandlers`（自定义插件）
3. 都没有 → 返回错误 "unknown task type"

### 4. Go SDK

新增 Go module `sdk/plugin/`：

```go
package plugin

// Handler 是插件必须实现的接口
type Handler interface {
    // Init 初始化插件，接收配置
    Init(cfg map[string]interface{}) error

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
    TaskType string                 `json:"task_type"`
    Params   map[string]interface{} `json:"params"`
    Timeout  int                    `json:"timeout_seconds"`
}

// TaskResponse 是插件返回的任务结果
type TaskResponse struct {
    TaskID   string      `json:"task_id"`
    Status   string      `json:"status"` // success, error
    Data     interface{} `json:"data"`
    Error    string      `json:"error,omitempty"`
}

// Serve 启动插件服务，阻塞运行
func Serve(handler Handler) error

// ServeWithOptions 带选项启动
func ServeWithOptions(handler Handler, opts ...Option) error
```

**SDK 内部处理：**
1. 从 `OPSAGENT_PLUGIN_SOCKET` 环境变量获取 socket 路径
2. 连接到 UDS
3. 读取 JSON-RPC 请求，分发到 handler
4. 处理分块响应（自动分块大响应）
5. 处理 `ping` 心跳
6. 信号处理（SIGTERM → 调用 `handler.Shutdown()`）

### 5. Rust SDK

新增 Rust crate `sdk/opsagent-plugin/`：

```rust
use opsagent_plugin::{Plugin, TaskRequest, TaskResponse, Result};
use async_trait::async_trait;

#[async_trait]
pub trait Plugin: Send + Sync {
    async fn init(&mut self, config: serde_json::Value) -> Result<()>;
    async fn execute(&self, request: TaskRequest) -> Result<TaskResponse>;
    async fn shutdown(&mut self) -> Result<()>;
    async fn health_check(&self) -> Result<()>;
}

// 启动插件服务
pub async fn serve<P: Plugin + 'static>(plugin: P) -> Result<()>
```

**依赖：** `tokio`、`serde`、`serde_json`、`async-trait`、`tracing`

### 6. 配置集成

`internal/config/config.go` 新增配置段：

```yaml
plugins:
  # 插件目录路径
  plugins_dir: "/etc/opsagent/plugins"
  # 启用的插件列表（空 = 全部启用）
  enabled_plugins: []
  # 插件健康检查间隔
  health_check_interval_seconds: 30
  # 插件启动超时
  startup_timeout_seconds: 10
```

### 7. Agent 接入

修改 `internal/app/agent.go`：

```go
type Agent struct {
    // ... existing fields
    pluginLoader  *pluginruntime.PluginLoader
    pluginRouter  *pluginruntime.PluginRouter
}

// startSubsystems 中：
// 1. 创建 PluginRouter，注册内置 handler
// 2. 创建 PluginLoader，加载自定义插件
// 3. 将插件的 task types 注册到 PluginRouter
// 4. 将 PluginRouter 接入 task dispatcher

// shutdown 中：
// 1. 停止所有自定义插件
```

### 8. 插件沙箱化

每个插件进程通过 nsjail 限制：

```go
func (l *PluginLoader) sandboxCommand(manifest *PluginManifest, cmd *exec.Cmd) *exec.Cmd {
    // 基于 manifest.limits 生成 nsjail 配置
    // - memory limit: manifest.Limits.MaxMemoryMB
    // - CPU limit: manifest.Limits.MaxCPUPercent
    // - 网络限制: 根据 manifest.Requirements.Capabilities
    // - 文件系统: 只读挂载 + 插件专用 tmp
}
```

### 9. 示例插件

**Go 示例 — Echo Plugin：**
```
sdk/examples/go-echo/
├── plugin.yaml
├── main.go          # 10 行，调用 plugin.Serve(&EchoHandler{})
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

### 加载器测试
- 发现并加载有效插件
- 拒绝无效清单（缺字段、版本格式错误）
- 插件进程崩溃后状态更新
- 超时未连接的处理

### 路由器测试
- 正确路由到内置 handler
- 正确路由到插件 handler
- 未知 task type 返回错误
- 插件注销后路由更新

### SDK 测试
- Go SDK: 协议合规、分块、错误处理、信号处理
- Rust SDK: 同上
- 端到端: Agent → 加载插件 → 发送任务 → 收到响应

### 沙箱测试
- 内存限制生效
- CPU 限制生效
- 文件系统隔离

## 验证方式

```bash
# Go SDK 测试
cd sdk/plugin && go test -race ./...

# Rust SDK 测试
cd sdk/opsagent-plugin && cargo test

# 示例插件构建
cd sdk/examples/go-echo && go build -o echo-plugin
cd sdk/examples/rust-audit && cargo build

# 端到端测试
go test -race -tags=integration ./internal/integration/ -run TestCustomPlugin
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/pluginruntime/loader.go` | 新建 |
| `internal/pluginruntime/loader_test.go` | 新建 |
| `internal/pluginruntime/router.go` | 新建 |
| `internal/pluginruntime/router_test.go` | 新建 |
| `internal/pluginruntime/sandbox.go` | 新建 |
| `sdk/plugin/` | 新建 Go module |
| `sdk/plugin/handler.go` | 新建 — Handler 接口 |
| `sdk/plugin/serve.go` | 新建 — Serve 函数 |
| `sdk/plugin/client.go` | 新建 — UDS 客户端 |
| `sdk/plugin/protocol.go` | 新建 — JSON-RPC 类型 |
| `sdk/plugin/*_test.go` | 新建 |
| `sdk/opsagent-plugin/` | 新建 Rust crate |
| `sdk/opsagent-plugin/src/lib.rs` | 新建 — Plugin trait |
| `sdk/opsagent-plugin/src/serve.rs` | 新建 — serve 函数 |
| `sdk/opsagent-plugin/src/protocol.rs` | 新建 |
| `sdk/opsagent-plugin/tests/` | 新建 |
| `sdk/examples/go-echo/` | 新建 |
| `sdk/examples/rust-audit/` | 新建 |
| `internal/config/config.go` | 修改 — plugins 配置段 |
| `internal/app/agent.go` | 修改 — 接入 PluginLoader/Router |
| `docs/plugin-sdk-guide.md` | 新建 — SDK 使用指南 |
