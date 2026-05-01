# OpsAgent 插件 SDK 开发指南

> 本文档面向插件开发者，说明如何使用 OpsAgent 提供的 SDK 编写自定义插件。OpsAgent 是一个宿主机侧的指标采集与沙箱执行 Agent，其插件系统通过 UDS (Unix Domain Socket) JSON-RPC 2.0 协议实现外部进程与主 Agent 之间的通信。

---

## 目录

1. [概述](#1-概述)
2. [插件清单（plugin.yaml）](#2-插件清单pluginyaml)
3. [Go SDK 开发](#3-go-sdk-开发)
4. [Rust SDK 开发](#4-rust-sdk-开发)
5. [插件部署](#5-插件部署)
6. [协议参考](#6-协议参考)
7. [调试与测试](#7-调试与测试)

---

## 1. 概述

### 1.1 插件架构

OpsAgent 的插件系统采用 **进程外** 架构。PluginGateway 组件负责插件的发现、启动和生命周期管理：

```
┌─────────────────────────────────────────────────────┐
│                  OpsAgent 主进程                      │
│                                                       │
│   ┌───────────────────────────────────────────────┐  │
│   │              PluginGateway                     │  │
│   │                                               │  │
│   │  1. 扫描 plugin.yaml 清单                      │  │
│   │  2. 启动插件进程                                │  │
│   │  3. 通过 UDS 发送 JSON-RPC 请求                 │  │
│   │  4. 健康检查 / 自动重启                         │  │
│   └───────────┬───────────────┬───────────────────┘  │
│               │               │                       │
└───────────────┼───────────────┼───────────────────────┘
                │               │
         UDS (Unix Socket)  UDS (Unix Socket)
                │               │
    ┌───────────┴───┐   ┌──────┴──────────┐
    │  Go 插件进程   │   │  Rust 插件进程   │
    │  (go-echo)    │   │  (rust-audit)   │
    └───────────────┘   └─────────────────┘
```

**核心特性**：

- **语言无关**：任何能通过 Unix Socket 发送/接收 JSON 的语言均可编写插件
- **进程隔离**：插件作为独立进程运行，崩溃不影响主 Agent
- **自动发现**：PluginGateway 通过扫描插件目录下的 `plugin.yaml` 文件自动发现插件
- **健康检查**：定期发送 `ping` 请求检测插件存活状态
- **自动重启**：插件异常退出后自动重启，支持指数退避（最大重试 3 次）

### 1.2 内置任务类型

OpsAgent Rust 运行时内置了 6 种任务类型，无需编写插件即可使用：

| 任务类型 | 说明 |
|----------|------|
| `plugin_log_parse` | 解析日志文本，统计错误/警告数量 |
| `plugin_text_process` | 文本操作：大写转换、小写转换、词数统计 |
| `plugin_fs_scan` | 递归目录扫描，统计文件信息 |
| `plugin_conn_analyze` | 分析 `/proc/net` 中的网络连接 |
| `plugin_local_probe` | 系统健康检查：磁盘、内存、OOM、僵尸进程 |
| `plugin_ebpf_collect` | eBPF 系统调用计数（需要编译时启用 `ebpf` feature） |

自定义插件可以定义自己的任务类型名称，PluginGateway 会根据 `plugin.yaml` 中声明的 `task_types` 将请求路由到对应的插件进程。

---

## 2. 插件清单（plugin.yaml）

每个插件必须在插件目录下提供一个 `plugin.yaml` 清单文件，用于描述插件的元数据和运行配置。

### 2.1 完整格式

```yaml
# 插件唯一名称
name: go-echo

# 语义化版本号
version: "1.0.0"

# 插件描述
description: "Echo plugin for testing the SDK"

# 作者信息
author: "opsagent@example.com"

# 运行时类型（当前仅支持 process）
runtime: process

# 插件可执行文件路径（相对于插件目录）
binary_path: ./go-echo

# 此插件支持的任务类型列表
task_types:
  - echo

# 资源限制
limits:
  # 最大内存使用量（MB）
  max_memory_mb: 64
  # 单次任务超时时间（秒）
  timeout_seconds: 10
```

### 2.2 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `name` | string | 是 | 插件唯一标识符，建议使用小写字母和连字符 |
| `version` | string | 是 | 语义化版本号 |
| `description` | string | 否 | 插件功能描述 |
| `author` | string | 否 | 作者或维护者联系方式 |
| `runtime` | string | 是 | 运行时类型，当前固定为 `process` |
| `binary_path` | string | 是 | 可执行文件路径，相对于插件目录 |
| `task_types` | list[string] | 是 | 插件处理的任务类型列表，至少一项 |
| `limits.max_memory_mb` | int | 否 | 内存上限（MB），默认由 Agent 配置决定 |
| `limits.timeout_seconds` | int | 否 | 任务超时（秒），默认由 Agent 配置决定 |

### 2.3 示例

**Go Echo 插件** (`sdk/examples/go-echo/plugin.yaml`)：

```yaml
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

**Rust Audit 插件** (`sdk/examples/rust-audit/plugin.yaml`)：

```yaml
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

---

## 3. Go SDK 开发

### 3.1 安装

```bash
go get github.com/cy77cc/opsagent/sdk/plugin
```

### 3.2 Handler 接口

Go SDK 的核心是 `Handler` 接口（定义在 `sdk/plugin/handler.go`）。插件开发者需要实现此接口的所有方法：

```go
type Handler interface {
    // Init 在插件启动时调用一次。cfg 可能为 nil。
    Init(cfg map[string]interface{}) error

    // TaskTypes 返回此 handler 支持的任务类型字符串列表。
    TaskTypes() []string

    // Execute 处理单个任务请求并返回响应。
    Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error)

    // Shutdown 在插件被优雅终止时调用。
    Shutdown(ctx context.Context) error

    // HealthCheck 返回 nil 表示插件健康。
    HealthCheck(ctx context.Context) error
}
```

### 3.3 请求与响应结构

**TaskRequest**（定义在 `sdk/plugin/protocol.go`）：

```go
type TaskRequest struct {
    TaskID   string                 `json:"task_id"`    // 任务唯一 ID
    TaskType string                 `json:"task_type"`  // 任务类型
    Params   map[string]interface{} `json:"params"`     // 任务参数
    Deadline int64                  `json:"deadline_ms"` // 截止时间（Unix 毫秒时间戳）
}
```

**TaskResponse**：

```go
type TaskResponse struct {
    TaskID string      `json:"task_id"`          // 任务唯一 ID（需与请求一致）
    Status string      `json:"status"`           // "ok" 或 "error"
    Data   interface{} `json:"data,omitempty"`   // 成功时的返回数据
    Error  string      `json:"error,omitempty"`  // 失败时的错误信息
}
```

### 3.4 启动服务

Go SDK 提供两个启动函数：

```go
// 使用默认选项启动
plugin.Serve(handler)

// 使用自定义选项启动
plugin.ServeWithOptions(handler,
    plugin.WithLogger(logger),              // 自定义 slog.Logger
    plugin.WithGracefulTimeout(30*time.Second), // 自定义优雅关闭超时
)
```

`Serve` 函数会从 `OPSAGENT_PLUGIN_SOCKET` 环境变量读取 Unix Socket 路径，初始化 handler，然后监听 JSON-RPC 请求，直到收到 SIGTERM 或 SIGINT 信号。

### 3.5 完整示例：Echo 插件

以下是一个完整的 Go echo 插件实现（源码位于 `sdk/examples/go-echo/main.go`）：

```go
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
            "echo": req.Params,
            "task": req.TaskType,
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

**编译与部署**：

```bash
# 编译
cd sdk/examples/go-echo
go build -o go-echo .

# 部署到插件目录
mkdir -p /etc/opsagent/plugins/go-echo
cp go-echo plugin.yaml /etc/opsagent/plugins/go-echo/
```

### 3.6 自定义选项示例

```go
func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    }))

    if err := plugin.ServeWithOptions(&MyHandler{},
        plugin.WithLogger(logger),
        plugin.WithGracefulTimeout(30*time.Second),
    ); err != nil {
        log.Fatalf("serve: %v", err)
    }
}
```

---

## 4. Rust SDK 开发

### 4.1 安装

在 `Cargo.toml` 中添加依赖：

```toml
[dependencies]
opsagent-plugin = { path = "../../opsagent-plugin" }
# 或从 crates.io（发布后）
# opsagent-plugin = "1.0"

async-trait = "0.1"
serde_json = "1"
tokio = { version = "1", features = ["full"] }
tracing = "0.1"
tracing-subscriber = "0.3"
```

### 4.2 Plugin Trait

Rust SDK 的核心是 `Plugin` trait（定义在 `sdk/opsagent-plugin/src/lib.rs`）。插件开发者需要实现此 trait：

```rust
#[async_trait]
pub trait Plugin: Send + Sync {
    /// 返回此插件支持的任务类型字符串列表。
    fn task_types(&self) -> Vec<String>;

    /// 插件启动时调用一次。cfg 可能为 Value::Null。
    async fn init(&self, cfg: Value) -> Result<()>;

    /// 处理单个任务请求并返回响应。
    async fn execute(&self, req: &TaskRequest) -> Result<TaskResponse>;

    /// 插件被优雅终止时调用。
    async fn shutdown(&self) -> Result<()>;

    /// 返回 Ok(()) 表示插件健康。
    async fn health_check(&self) -> Result<()>;
}
```

### 4.3 请求与响应结构

**TaskRequest**（定义在 `sdk/opsagent-plugin/src/protocol.rs`）：

```rust
pub struct TaskRequest {
    pub task_id: String,      // 任务唯一 ID
    pub task_type: String,    // 任务类型
    pub params: Value,        // 任务参数（serde_json::Value）
    pub deadline_ms: i64,     // 截止时间（Unix 毫秒时间戳）
}
```

**TaskResponse**：

```rust
pub struct TaskResponse {
    pub task_id: String,              // 任务唯一 ID（需与请求一致）
    pub status: String,               // "ok" 或 "error"
    pub data: Option<Value>,          // 成功时的返回数据
    pub error: Option<String>,        // 失败时的错误信息
}
```

### 4.4 错误类型

SDK 定义了 `PluginError` 枚举（定义在 `sdk/opsagent-plugin/src/error.rs`），用于统一的错误处理：

```rust
#[derive(Error, Debug)]
pub enum PluginError {
    #[error("configuration error: {0}")]
    Config(String),       // 配置错误，映射到 JSON-RPC 错误码 -32602

    #[error("execution error: {0}")]
    Execution(String),    // 执行错误，映射到 JSON-RPC 错误码 -32000

    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),  // IO 错误，映射到 JSON-RPC 错误码 -32603

    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),  // JSON 错误，映射到 JSON-RPC 错误码 -32700
}

pub type Result<T> = std::result::Result<T, PluginError>;
```

### 4.5 启动服务

Rust SDK 提供两个启动函数：

```rust
// 使用默认选项启动（优雅关闭超时 10 秒）
opsagent_plugin::serve(plugin).await

// 使用自定义选项启动
opsagent_plugin::serve_with_options(plugin, ServeOptions {
    graceful_timeout: Duration::from_secs(30),
}).await
```

`serve` 函数会从 `OPSAGENT_PLUGIN_SOCKET` 环境变量读取 Unix Socket 路径，初始化插件，然后监听 JSON-RPC 请求，直到收到 ctrl-c 信号。

### 4.6 完整示例：Audit 插件

以下是一个完整的 Rust 系统审计插件实现（源码位于 `sdk/examples/rust-audit/src/main.rs`）：

```rust
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

    async fn init(&self, _config: Value) -> Result<()> {
        tracing_subscriber::fmt::init();
        tracing::info!("audit plugin initialized");
        Ok(())
    }

    async fn execute(&self, request: &TaskRequest) -> Result<TaskResponse> {
        tracing::info!(task_id = %request.task_id, "executing audit");

        let disk_usage = get_disk_usage();
        let memory_info = get_memory_info();

        Ok(TaskResponse {
            task_id: request.task_id.clone(),
            status: "ok".into(),
            data: Some(json!({
                "disk": disk_usage,
                "memory": memory_info,
            })),
            error: None,
        })
    }

    async fn shutdown(&self) -> Result<()> {
        tracing::info!("audit plugin shutting down");
        Ok(())
    }

    async fn health_check(&self) -> Result<()> {
        Ok(())
    }
}

fn get_disk_usage() -> Value {
    json!({"status": "ok", "note": "disk check placeholder"})
}

fn get_memory_info() -> Value {
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
            return line
                .split_whitespace()
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

**编译与部署**：

```bash
# 编译
cd sdk/examples/rust-audit
cargo build --release

# 部署到插件目录
mkdir -p /etc/opsagent/plugins/rust-audit
cp target/release/rust-audit plugin.yaml /etc/opsagent/plugins/rust-audit/
```

---

## 5. 插件部署

### 5.1 目录结构

插件部署到 `plugin_gateway.plugins_dir` 配置指定的目录（默认 `/etc/opsagent/plugins/`）。每个插件占用一个子目录，包含清单文件和可执行文件：

```
/etc/opsagent/plugins/
├── go-echo/
│   ├── plugin.yaml
│   └── go-echo
└── rust-audit/
    ├── plugin.yaml
    └── rust-audit
```

### 5.2 部署步骤

```bash
# 1. 创建插件目录
sudo mkdir -p /etc/opsagent/plugins/my-plugin

# 2. 复制清单文件和可执行文件
sudo cp plugin.yaml my-plugin /etc/opsagent/plugins/my-plugin/

# 3. 设置可执行权限
sudo chmod +x /etc/opsagent/plugins/my-plugin/my-plugin

# 4. 重启 OpsAgent（或等待 fsnotify 自动检测文件变更）
sudo systemctl restart opsagent
```

### 5.3 生命周期管理

PluginGateway 对插件的生命周期管理如下：

| 阶段 | 行为 |
|------|------|
| **发现** | 启动时扫描插件目录，解析所有 `plugin.yaml` 文件 |
| **启动** | 为每个插件创建 Unix Socket，设置 `OPSAGENT_PLUGIN_SOCKET` 环境变量，启动插件进程 |
| **健康检查** | 定期向每个插件发送 `ping` 请求，检测存活状态 |
| **自动重启** | 插件异常退出后自动重启，使用指数退避策略（最大重试 3 次） |
| **文件监听** | 通过 fsnotify 监听插件目录文件变更，自动发现新增插件 |
| **优雅关闭** | Agent 停止时向插件进程发送 SIGTERM，等待优雅关闭 |

### 5.4 运行时约束

| 约束 | 说明 |
|------|------|
| Socket 路径 | 通过 `OPSAGENT_PLUGIN_SOCKET` 环境变量传递给插件进程 |
| Socket 权限 | 0600（仅 Owner 可读写） |
| 内存限制 | 由 `plugin.yaml` 中的 `limits.max_memory_mb` 或全局配置决定 |
| 任务超时 | 由 `plugin.yaml` 中的 `limits.timeout_seconds` 或全局配置决定 |

### 5.5 编写其他语言的插件

由于协议是标准的 UDS JSON-RPC 2.0，任何支持 Unix Socket 和 JSON 的语言均可编写插件。核心步骤：

1. 从 `OPSAGENT_PLUGIN_SOCKET` 环境变量读取 Socket 路径
2. 连接到 Unix Socket
3. 读取换行符分隔的 JSON-RPC 请求
4. 处理 `ping` 方法（返回 `"pong"`）和 `execute_task` 方法
5. 写入换行符分隔的 JSON-RPC 响应
6. 收到 SIGTERM 时优雅关闭

**Python 最小示例**：

```python
import json
import os
import signal
import socket
import sys

socket_path = os.environ["OPSAGENT_PLUGIN_SOCKET"]

server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(socket_path)
server.listen(1)
os.chmod(socket_path, 0o600)

running = True
def handle_signal(sig, frame):
    global running
    running = False

signal.signal(signal.SIGTERM, handle_signal)
signal.signal(signal.SIGINT, handle_signal)

while running:
    try:
        server.settimeout(1.0)
        conn, _ = server.accept()
    except socket.timeout:
        continue

    data = conn.recv(65536).strip()
    if not data:
        conn.close()
        continue

    req = json.loads(data)
    method = req.get("method", "")
    req_id = req.get("id", 0)

    if method == "ping":
        resp = {"id": req_id, "result": "pong"}
    elif method == "execute_task":
        params = req.get("params", {})
        resp = {
            "id": req_id,
            "result": {
                "task_id": params.get("task_id", ""),
                "status": "ok",
                "data": {"echo": params},
            },
        }
    else:
        resp = {
            "id": req_id,
            "error": {"code": -32601, "message": f"method not found: {method}"},
        }

    conn.sendall(json.dumps(resp).encode() + b"\n")
    conn.close()

server.close()
os.unlink(socket_path)
```

---

## 6. 协议参考

> 完整的协议规范请参阅 [plugin-contract.md](./plugin-contract.md)。

### 6.1 传输层

- **协议**：UDS (Unix Domain Socket) JSON-RPC 2.0
- **消息格式**：换行符分隔的 JSON（每个请求/响应占一行，以 `\n` 结尾）
- **连接模型**：每个连接处理一个请求-响应对，处理完毕后关闭连接

### 6.2 方法

| 方法 | 说明 | 参数 |
|------|------|------|
| `ping` | 健康检查 | 空对象 `{}` |
| `execute_task` | 执行任务 | 包含 `task_id`、`task_type`、`params`、`deadline_ms` 的对象 |

### 6.3 请求示例

**健康检查**：

```json
{"jsonrpc":"2.0","method":"ping","id":"health-1","params":{}}
```

**执行任务**：

```json
{
    "jsonrpc": "2.0",
    "method": "execute_task",
    "id": "task-001",
    "params": {
        "task_id": "task-001",
        "task_type": "echo",
        "params": {"message": "hello"},
        "deadline_ms": 1714300000000
    }
}
```

### 6.4 响应示例

**成功**：

```json
{"id":"task-001","result":{"task_id":"task-001","status":"ok","data":{"echo":{"message":"hello"}}}}
```

**错误**：

```json
{"id":"task-001","error":{"code":-32602,"message":"Configuration error: root_path is required"}}
```

### 6.5 错误码

| 错误码 | 含义 | 对应场景 |
|--------|------|----------|
| -32700 | Parse error | 无效的 JSON |
| -32600 | Invalid request | 缺少必填字段 |
| -32601 | Method not found | 未知的方法名 |
| -32602 | Invalid params | 插件配置错误（`PluginError::Config`） |
| -32603 | Internal error | IO 错误或序列化错误（`PluginError::Io`） |
| -32000 | Server error | 任务执行失败（`PluginError::Execution`） |

### 6.6 大输出分块协议

当任务输出较大时，响应中的 `chunks` 字段用于分块传输：

```json
{
    "chunks": [
        {"seq": 1, "eof": false, "data_b64": "base64..."},
        {"seq": 2, "eof": false, "data_b64": "base64..."},
        {"seq": 3, "eof": true, "data_b64": "base64..."}
    ]
}
```

| 字段 | 说明 |
|------|------|
| `seq` | 从 1 开始的序列号 |
| `eof` | 最后一个分块为 `true` |
| `data_b64` | Base64 编码的分块数据 |

客户端按 `seq` 顺序拼接所有 `data_b64` 即可还原完整输出。

### 6.7 Go SDK 中的分块结构

```go
type Chunk struct {
    Seq    int    `json:"seq"`
    EOF    bool   `json:"eof"`
    DataB64 string `json:"data_b64"`
}

type TaskStats struct {
    DurationMS   int64 `json:"duration_ms"`
    CPUMS        int64 `json:"cpu_ms"`
    MemPeakBytes int64 `json:"mem_peak_bytes"`
}
```

---

## 7. 调试与测试

### 7.1 本地测试

使用 `socat` 工具可以直接连接到插件的 Unix Socket 进行手动测试：

```bash
# 连接到插件 socket
socat - UNIX-CONNECT:/tmp/opsagent/plugin.sock

# 发送 ping 请求
echo '{"jsonrpc":"2.0","method":"ping","id":"1","params":{}}' | socat - UNIX-CONNECT:/path/to/socket

# 发送 execute_task 请求
echo '{"jsonrpc":"2.0","method":"execute_task","id":"2","params":{"task_id":"test-001","task_type":"echo","params":{"msg":"hello"},"deadline_ms":0}}' | socat - UNIX-CONNECT:/path/to/socket
```

### 7.2 查看日志

```bash
# 实时跟踪 OpsAgent 日志
sudo journalctl -u opsagent -f

# 查看今日日志
sudo journalctl -u opsagent --since today

# 过滤插件相关日志
sudo journalctl -u opsagent | grep -i plugin
```

### 7.3 常见问题排查

| 问题 | 可能原因 | 排查方法 |
|------|----------|----------|
| `OPSAGENT_PLUGIN_SOCKET environment variable is not set` | 插件不是由 PluginGateway 启动的 | 确保插件通过 PluginGateway 启动，而非手动运行 |
| `permission denied` | Socket 文件权限不足 | 检查 Socket 文件权限是否为 0600，插件进程是否有访问权限 |
| `plugin binary not found` | `binary_path` 配置错误 | 检查 `plugin.yaml` 中的 `binary_path` 是否相对于插件目录正确 |
| 插件反复重启 | 插件启动时崩溃 | 查看插件进程的 stderr 输出，检查初始化逻辑 |
| `method not found` | 任务类型不匹配 | 检查 `plugin.yaml` 中的 `task_types` 是否与请求的 `task_type` 一致 |
| 任务超时 | 执行时间超过限制 | 检查 `limits.timeout_seconds` 配置，优化插件执行逻辑 |

### 7.4 开发调试技巧

**1. 本地手动启动插件进行调试**：

```bash
# 设置 socket 路径
export OPSAGENT_PLUGIN_SOCKET=/tmp/test-plugin.sock

# 启动插件（前台运行，可直接看到日志输出）
./my-plugin
```

在另一个终端中使用 `socat` 发送请求进行测试。

**2. 启用详细日志**：

- **Go 插件**：使用 `WithLogger` 选项传入 `slog.LevelDebug` 级别的 logger
- **Rust 插件**：使用 `tracing_subscriber` 的 `EnvFilter` 设置 `RUST_LOG=debug`

**3. 检查插件是否被 PluginGateway 正确发现**：

```bash
# 查看 PluginGateway 加载的插件列表
curl http://127.0.0.1:18080/api/v1/health | jq '.plugins'
```

---

## 附录：快速参考

### Go SDK 速查

```go
import "github.com/cy77cc/opsagent/sdk/plugin"

// 实现 Handler 接口
type MyHandler struct{}
func (h *MyHandler) Init(cfg map[string]interface{}) error { ... }
func (h *MyHandler) TaskTypes() []string { return []string{"my-task"} }
func (h *MyHandler) Execute(ctx context.Context, req *plugin.TaskRequest) (*plugin.TaskResponse, error) { ... }
func (h *MyHandler) Shutdown(ctx context.Context) error { ... }
func (h *MyHandler) HealthCheck(ctx context.Context) error { ... }

// 启动
func main() {
    if err := plugin.Serve(&MyHandler{}); err != nil {
        log.Fatalf("serve: %v", err)
    }
}
```

### Rust SDK 速查

```rust
use async_trait::async_trait;
use opsagent_plugin::{Plugin, protocol::{TaskRequest, TaskResponse}, error::Result};
use serde_json::Value;

struct MyPlugin;

#[async_trait]
impl Plugin for MyPlugin {
    fn task_types(&self) -> Vec<String> { vec!["my-task".into()] }
    async fn init(&self, _cfg: Value) -> Result<()> { Ok(()) }
    async fn execute(&self, req: &TaskRequest) -> Result<TaskResponse> { ... }
    async fn shutdown(&self) -> Result<()> { Ok(()) }
    async fn health_check(&self) -> Result<()> { Ok(()) }
}

#[tokio::main]
async fn main() -> Result<()> {
    opsagent_plugin::serve(MyPlugin).await
}
```

### plugin.yaml 速查

```yaml
name: my-plugin
version: "1.0.0"
description: "My custom plugin"
author: "me@example.com"
runtime: process
binary_path: ./my-plugin
task_types:
  - my-task
limits:
  max_memory_mb: 128
  timeout_seconds: 30
```
