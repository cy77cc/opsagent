# Spec 5: Rust 插件运行时完善

## Context

OpsAgent 的 Rust 插件运行时（`rust-runtime/`）通过 UDS JSON-RPC 协议与 Go 侧通信。当前 6 个 handler 中只有 2 个真正实现：

| Handler | 状态 | 行号 |
|---------|------|------|
| `handle_log_parse` | 已实现 | 196 |
| `handle_text_process` | 已实现 | 223 |
| `plugin_ebpf_collect` | "degraded" 空壳 | 157 |
| `plugin_fs_scan` | "stub" 空壳 | 162 |
| `plugin_conn_analyze` | "stub" 空壳 | 162 |
| `plugin_local_probe` | "stub" 空壳 | 162 |

此外，`main.rs` 是 291 行的单体文件，所有 handler 混在一起。本 spec 补全所有空壳 handler，重构为模块化结构，并建立插件契约文档（为 Spec 6 SDK 打基础）。

**依赖：** Spec 1

## 目标

1. 6 个 handler 全部有工作实现（无空壳）
2. 代码模块化拆分
3. ebpf_collect 在内核 <5.4 上优雅降级
4. 插件契约文档完成
5. Rust 测试覆盖率 ≥70%

## 设计

### 1. 模块化重构

将 `rust-runtime/src/main.rs` 拆分为：

```
rust-runtime/src/
├── main.rs              # UDS 监听、JSON-RPC 分发、生命周期
├── error.rs             # 错误类型（thiserror）
├── chunking.rs          # 分块响应逻辑
├── handlers/
│   ├── mod.rs           # handler 注册和分发
│   ├── log_parse.rs     # 已实现，迁移
│   ├── text_process.rs  # 已实现，迁移
│   ├── fs_scan.rs       # 新实现
│   ├── conn_analyze.rs  # 新实现
│   ├── local_probe.rs   # 新实现
│   └── ebpf_collect.rs  # 新实现（带 feature flag）
└── protocol.rs          # JSON-RPC 请求/响应类型定义
```

**main.rs 精简为：**
```rust
#[tokio::main]
async fn main() -> Result<()> {
    let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
        .unwrap_or_else(|_| "/tmp/opsagent-plugin.sock".into());

    let listener = UnixListener::bind(&socket_path)?;
    tracing::info!(socket = %socket_path, "Plugin runtime started");

    loop {
        let (stream, _) = listener.accept().await?;
        tokio::spawn(handle_connection(stream));
    }
}
```

### 2. 错误类型

新增 `rust-runtime/src/error.rs`：

```rust
use thiserror::Error;

#[derive(Error, Debug)]
pub enum PluginError {
    #[error("Configuration error: {0}")]
    Config(String),

    #[error("Execution error: {0}")]
    Execution(String),

    #[error("Resource error: {0}")]
    Resource(String),

    #[error("Unsupported on this system: {0}")]
    Unsupported(String),

    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
}
```

所有 handler 返回 `Result<T, PluginError>`，JSON-RPC 层转换为错误响应。

### 3. fs_scan Handler

**功能：** 递归扫描目录，返回文件统计信息。

**输入参数：**
```json
{
    "root_path": "/var/log",
    "max_depth": 3,
    "include_patterns": ["*.log", "*.txt"],
    "min_size_bytes": 1024
}
```

**输出：**
```json
{
    "total_files": 156,
    "total_size_bytes": 52428800,
    "by_extension": {".log": 120, ".txt": 36},
    "largest_files": [
        {"path": "/var/log/syslog", "size_bytes": 10485760}
    ],
    "scan_duration_ms": 45
}
```

**实现：**
- 使用 `walkdir` crate 进行递归遍历
- `max_depth` 限制遍历深度
- `include_patterns` 使用 `glob` crate 过滤
- `min_size_bytes` 过滤小文件
- 收集统计信息，返回 top-10 最大文件

**依赖：** `walkdir`、`glob`、`serde`

**工作量：** M

### 4. conn_analyze Handler

**功能：** 分析系统网络连接状态。

**输入参数：**
```json
{
    "include_states": ["ESTABLISHED", "LISTEN", "TIME_WAIT"],
    "top_n": 10
}
```

**输出：**
```json
{
    "total_connections": 523,
    "by_state": {"ESTABLISHED": 45, "LISTEN": 12, "TIME_WAIT": 466},
    "by_protocol": {"tcp": 500, "udp": 23},
    "top_remote_addresses": [
        {"address": "10.0.0.1:443", "count": 15}
    ],
    "top_local_ports": [
        {"port": 8080, "count": 20}
    ]
}
```

**实现：**
- 解析 `/proc/net/tcp`、`/proc/net/tcp6`、`/proc/net/udp`、`/proc/net/udp6`
- 使用 `procfs` crate（或手动解析）
- 按状态分组计数
- 按远程地址分组，取 top-N
- 按本地端口分组，取 top-N

**依赖：** `procfs`（可选，也可手动解析）

**工作量：** M

### 5. local_probe Handler

**功能：** 系统健康探针，检查多项指标并返回 pass/warn/fail。

**输入参数：**
```json
{
    "checks": [
        {"type": "disk_space", "threshold_percent": 90},
        {"type": "memory_pressure", "threshold_percent": 90},
        {"type": "oom_killer", "lookback_minutes": 60},
        {"type": "zombie_processes", "max_count": 5},
        {"type": "ntp_drift", "max_offset_ms": 100}
    ]
}
```

**输出：**
```json
{
    "overall_status": "warn",
    "checks": [
        {"type": "disk_space", "status": "pass", "detail": "/ at 45%"},
        {"type": "memory_pressure", "status": "warn", "detail": "92% used"},
        {"type": "oom_killer", "status": "pass", "detail": "no OOM events"},
        {"type": "zombie_processes", "status": "pass", "detail": "0 zombies"},
        {"type": "ntp_drift", "status": "fail", "detail": "offset 250ms"}
    ]
}
```

**实现：**
- `disk_space`: 读取 `/proc/mounts` + `statvfs`
- `memory_pressure`: 读取 `/proc/meminfo`
- `oom_killer`: 搜索 `dmesg` 或 `/var/log/kern.log` 中的 OOM 记录
- `zombie_processes`: 扫描 `/proc/*/status` 中 `State: Z`
- `ntp_drift`: 调用 `chronyc tracking` 或解析 `/proc/...`

**依赖：** 标准库

**工作量：** M

### 6. ebpf_collect Handler

**功能：** 通过 eBPF 程序采集内核级指标。

**Cargo feature flag：**
```toml
[features]
default = []
ebpf = ["aya"]
```

**实现策略：**
1. 编译时：`cfg(feature = "ebpf")` 控制是否编译 eBPF 代码
2. 运行时：检查内核版本（`uname -r` 解析），< 5.4 返回 `PluginError::Unsupported`
3. 基础 kprobe：syscall counting（`sys_enter` tracepoint）
4. 后续可扩展：网络延迟、文件 IO 追踪

**输入参数：**
```json
{
    "programs": ["syscall_count"],
    "duration_seconds": 10
}
```

**输出：**
```json
{
    "syscall_counts": {
        "read": 15000,
        "write": 8000,
        "open": 500,
        "close": 500
    },
    "collection_duration_ms": 10000
}
```

**降级行为：**
- 内核 < 5.4 → 返回 `PluginError::Unsupported("eBPF requires kernel >= 5.4")`
- 未启用 feature → 返回 `PluginError::Unsupported("eBPF support not compiled")`
- 权限不足 → 返回 `PluginError::Resource("eBPF requires root or CAP_BPF")`

**依赖：** `aya`（feature-gated）

**工作量：** L

### 7. 结构化日志

替换 `eprintln!` 为 `tracing`：

```rust
use tracing::{info, warn, error, instrument};

#[instrument(skip(request), fields(task_id = %request.task_id))]
async fn handle_request(request: JsonRequest) -> JsonResult {
    info!("Processing request");
    // ...
}
```

**依赖：** `tracing`、`tracing-subscriber`

### 8. 插件契约文档

新增 `docs/plugin-contract.md`，内容：

1. **协议概述** — UDS JSON-RPC 2.0 over Unix socket
2. **请求格式** — `{"jsonrpc":"2.0","method":"task_type","params":{...},"id":"..."}`
3. **响应格式** — 成功/错误响应、分块响应
4. **分块协议** — 大响应分块：`{"chunk":"base64...","index":0,"total":5,"id":"..."}`
5. **生命周期** — init → execute (repeat) → shutdown
6. **错误码** — 标准 JSON-RPC 错误码 + 自定义错误码
7. **健康检查** — `ping`/`pong` 心跳
8. **版本策略** — 协议版本化、向后兼容保证

**工作量：** M

## 测试要求

### Rust 单元测试
- 每个 handler 的正常输入 → 正确输出
- 每个 handler的错误输入 → 正确错误类型
- 分块逻辑：大响应正确分块和重组
- 错误类型：每种 PluginError 的序列化

### Rust 集成测试
- 启动运行时 → UDS 连接 → 发送每个 task type → 验证响应
- 发送无效 JSON → 验证错误响应
- 并发请求 → 验证正确处理

### Go 侧集成测试
- 通过 `pluginruntime.Client` 发送任务到 Rust 运行时 → 验证结果

## 验证方式

```bash
# Rust 构建和测试
cd rust-runtime
cargo build
cargo test
cargo clippy -- -D warnings

# 带 eBPF feature 构建（需要内核头文件）
cargo build --features ebpf

# Go 侧集成测试
go test -race -tags=integration ./internal/integration/ -run TestPluginRuntime
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `rust-runtime/Cargo.toml` | 修改 — 添加依赖和 feature flag |
| `rust-runtime/src/main.rs` | 重构 — 精简为 UDS 监听和分发 |
| `rust-runtime/src/error.rs` | 新建 |
| `rust-runtime/src/protocol.rs` | 新建 |
| `rust-runtime/src/chunking.rs` | 新建 |
| `rust-runtime/src/handlers/mod.rs` | 新建 |
| `rust-runtime/src/handlers/log_parse.rs` | 从 main.rs 迁移 |
| `rust-runtime/src/handlers/text_process.rs` | 从 main.rs 迁移 |
| `rust-runtime/src/handlers/fs_scan.rs` | 新建 |
| `rust-runtime/src/handlers/conn_analyze.rs` | 新建 |
| `rust-runtime/src/handlers/local_probe.rs` | 新建 |
| `rust-runtime/src/handlers/ebpf_collect.rs` | 新建 |
| `rust-runtime/tests/integration_test.rs` | 新建 |
| `docs/plugin-contract.md` | 新建 |
