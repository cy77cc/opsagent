# Spec: Rust Plugin Runtime Completion

## Context

OpsAgent's Rust plugin runtime (`rust-runtime/`) communicates with Go via UDS JSON-RPC. Current state:

| Handler | Status | Location |
|---------|--------|----------|
| `handle_log_parse` | Implemented | main.rs:196 |
| `handle_text_process` | Implemented | main.rs:223 |
| `plugin_ebpf_collect` | Hardcoded "degraded" stub | main.rs:157 |
| `plugin_fs_scan` | Empty stub | main.rs:162 |
| `plugin_conn_analyze` | Empty stub | main.rs:162 |
| `plugin_local_probe` | Empty stub | main.rs:162 |

The runtime is a 291-line single-file `main.rs` using synchronous I/O with `std::os::unix::net::UnixListener`. This spec completes all handlers, refactors to modular async architecture with a trait-based plugin system, and establishes the plugin contract documentation.

**Dependency:** Spec 1 (config hot reload) must be completed first — the runtime needs stable config before adding new handlers.

## Goals

1. All 6 handlers have working implementations (no stubs)
2. Modular code structure with trait-based plugin abstraction
3. Async runtime (tokio) with concurrent request handling
4. eBPF handler compiles as stub by default, real implementation behind feature flag
5. Plugin contract documentation complete
6. Rust test coverage >= 70%

## Design

### 1. Module Structure

Refactor `rust-runtime/src/main.rs` into:

```
rust-runtime/src/
├── main.rs              # Entry point: UDS listener, graceful shutdown
├── error.rs             # PluginError enum (thiserror)
├── protocol.rs          # JSON-RPC request/response types
├── chunking.rs          # Response chunking logic
├── registry.rs          # PluginRegistry: register + dispatch
├── plugin.rs            # Plugin trait definition
└── handlers/
    ├── mod.rs           # Re-exports all handlers
    ├── log_parse.rs     # LogParsePlugin (migrated from main.rs)
    ├── text_process.rs  # TextProcessPlugin (migrated from main.rs)
    ├── fs_scan.rs       # FsScanPlugin (new)
    ├── conn_analyze.rs  # ConnAnalyzePlugin (new)
    ├── local_probe.rs   # LocalProbePlugin (new)
    └── ebpf_collect.rs  # EbpfCollectPlugin (new, feature-gated)
```

**main.rs after refactor:**
```rust
use tokio::net::UnixListener;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tracing::{info, error, instrument};
use std::sync::Arc;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt()
        .with_target(false)
        .init();

    let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
        .unwrap_or_else(|_| "/tmp/opsagent-plugin.sock".into());

    let _ = std::fs::remove_file(&socket_path);
    let listener = UnixListener::bind(&socket_path)?;
    info!(socket = %socket_path, "Plugin runtime started");

    let registry = Arc::new(build_registry());

    loop {
        let (stream, _addr) = listener.accept().await?;
        let reg = registry.clone();

        tokio::spawn(async move {
            if let Err(e) = handle_connection(stream, reg).await {
                error!("Connection error: {}", e);
            }
        });
    }
}

fn build_registry() -> PluginRegistry {
    let mut reg = PluginRegistry::new();
    reg.register(Box::new(LogParsePlugin));
    reg.register(Box::new(TextProcessPlugin));
    reg.register(Box::new(FsScanPlugin::new()));
    reg.register(Box::new(ConnAnalyzePlugin));
    reg.register(Box::new(LocalProbePlugin));

    #[cfg(feature = "ebpf")]
    reg.register(Box::new(EbpfCollectPlugin::new()));

    reg
}
```

### 2. Plugin Trait

Define `rust-runtime/src/plugin.rs`:

```rust
use async_trait::async_trait;
use serde_json::Value;
use crate::error::PluginError;

/// Result returned by plugin execution.
pub struct PluginResult {
    pub status: String,           // "ok", "error", "degraded"
    pub summary: Option<Value>,   // structured metadata
    pub output: String,           // text output (chunked if large)
}

/// Core trait that all plugins implement.
#[async_trait]
pub trait Plugin: Send + Sync {
    /// Human-readable plugin name (for logging).
    fn name(&self) -> &str;

    /// Task type string this plugin handles (matches JSON-RPC "type" field).
    fn task_type(&self) -> &str;

    /// Execute the plugin with the given payload.
    async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError>;
}
```

### 3. Plugin Registry

Define `rust-runtime/src/registry.rs`:

```rust
use std::collections::HashMap;
use serde_json::Value;
use crate::plugin::{Plugin, PluginResult};
use crate::error::PluginError;

pub struct PluginRegistry {
    plugins: HashMap<String, Box<dyn Plugin>>,
}

impl PluginRegistry {
    pub fn new() -> Self {
        Self { plugins: HashMap::new() }
    }

    pub fn register(&mut self, plugin: Box<dyn Plugin>) {
        let task_type = plugin.task_type().to_string();
        self.plugins.insert(task_type, plugin);
    }

    pub async fn dispatch(
        &self,
        task_type: &str,
        payload: &Value,
    ) -> Result<PluginResult, PluginError> {
        match self.plugins.get(task_type) {
            Some(plugin) => plugin.execute(payload).await,
            None => Err(PluginError::Execution(
                format!("unsupported task type: {}", task_type)
            )),
        }
    }

    pub fn registered_types(&self) -> Vec<&str> {
        self.plugins.keys().map(|s| s.as_str()).collect()
    }
}
```

### 4. Error Type

Define `rust-runtime/src/error.rs`:

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

impl PluginError {
    pub fn rpc_code(&self) -> i32 {
        match self {
            Self::Config(_)      => -32602,  // Invalid params
            Self::Execution(_)   => -32000,  // Server error
            Self::Resource(_)    => -32001,  // Server error
            Self::Unsupported(_) => -32002,  // Server error
            Self::Io(_)          => -32603,  // Internal error
        }
    }
}
```

### 5. Handler: fs_scan

**File:** `rust-runtime/src/handlers/fs_scan.rs`

**Task type:** `plugin_fs_scan`

**Input (payload field from JSON-RPC request):**
```json
{
    "root_path": "/var/log",
    "max_depth": 3,
    "include_patterns": ["*.log", "*.txt"],
    "min_size_bytes": 1024
}
```

**Output:**
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

**Implementation:**
- Uses `walkdir` crate for recursive directory traversal
- `max_depth` limits traversal depth (default: 5)
- `include_patterns` uses `glob` crate for file filtering
- `min_size_bytes` filters out small files
- Collects file count, total size, extension histogram
- Returns top-10 largest files by size
- Safety limit: abort if >1M files encountered → `PluginError::Resource`

**Error cases:**
- `root_path` doesn't exist → `PluginError::Config`
- Permission denied → `PluginError::Resource`
- >1M files → `PluginError::Resource("too many files: safety limit exceeded")`

**Dependencies:** `walkdir`, `glob`, `serde`

### 6. Handler: conn_analyze

**File:** `rust-runtime/src/handlers/conn_analyze.rs`

**Task type:** `plugin_conn_analyze`

**Input (payload field from JSON-RPC request):**
```json
{
    "include_states": ["ESTABLISHED", "LISTEN", "TIME_WAIT"],
    "top_n": 10
}
```

**Output:**
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

**Implementation:**
- Parse `/proc/net/tcp`, `/proc/net/tcp6`, `/proc/net/udp`, `/proc/net/udp6`
- Manual hex parsing of IP:port fields (no external dependency)
- Map TCP state codes to human-readable names
- Group by state, protocol, remote address, local port
- Sort and return top-N for address and port groups

**Error cases:**
- `/proc/net/*` unreadable → `PluginError::Resource`
- Malformed line → skip (log warning), continue parsing

**Dependencies:** Standard library only

### 7. Handler: local_probe

**File:** `rust-runtime/src/handlers/local_probe.rs`

**Task type:** `plugin_local_probe`

**Input (payload field from JSON-RPC request):**
```json
{
    "checks": [
        {"type": "disk_space", "threshold_percent": 90},
        {"type": "memory_pressure", "threshold_percent": 90},
        {"type": "oom_killer", "lookback_minutes": 60},
        {"type": "zombie_processes", "max_count": 5}
    ]
}
```

**Output:**
```json
{
    "overall_status": "warn",
    "checks": [
        {"type": "disk_space", "status": "pass", "detail": "/ at 45%"},
        {"type": "memory_pressure", "status": "warn", "detail": "92% used"},
        {"type": "oom_killer", "status": "pass", "detail": "no OOM events"},
        {"type": "zombie_processes", "status": "pass", "detail": "0 zombies"}
    ]
}
```

**Check implementations:**
- `disk_space`: `statvfs()` on mount points from `/proc/mounts`, compare used% to threshold
- `memory_pressure`: Parse `/proc/meminfo` for MemTotal/MemAvailable, compute used%
- `oom_killer`: Search `dmesg` output for "Out of memory" within lookback window
- `zombie_processes`: Scan `/proc/*/status` for `State: Z`, count occurrences

**Status logic:** Each check returns pass/warn/fail. `overall_status` = worst of all checks.

**Error cases:**
- Unknown check type → `PluginError::Config`
- `/proc` unreadable → `PluginError::Resource`
- `dmesg` unavailable → degrade oom_killer to "unknown" status

**Dependencies:** Standard library only

### 8. Handler: ebpf_collect (feature-gated)

**File:** `rust-runtime/src/handlers/ebpf_collect.rs`

**Task type:** `plugin_ebpf_collect`

**Cargo.toml feature flag:**
```toml
[features]
default = []
ebpf = ["aya"]
```

**Input (payload field from JSON-RPC request):**
```json
{
    "programs": ["syscall_count"],
    "duration_seconds": 10
}
```

**Output:**
```json
{
    "syscall_counts": {"read": 15000, "write": 8000, "open": 500, "close": 500},
    "collection_duration_ms": 10000
}
```

**Implementation:**
- Default (no feature): Returns `PluginError::Unsupported("eBPF support not compiled")`
- With `--features ebpf`:
  1. Check kernel version via `uname -r`, require >= 5.4
  2. Check permissions (root or CAP_BPF)
  3. Load kprobe on `sys_enter` tracepoint via `aya`
  4. Collect syscall counts for configured duration
  5. Return aggregated counts

**Degradation cascade:**
1. Feature not compiled → `PluginError::Unsupported("eBPF support not compiled")`
2. Kernel < 5.4 → `PluginError::Unsupported("eBPF requires kernel >= 5.4")`
3. No root/CAP_BPF → `PluginError::Resource("eBPF requires root or CAP_BPF")`
4. Program load failure → `PluginError::Execution("failed to load eBPF program: ...")`

**Dependencies:** `aya` (feature-gated)

### 9. Connection Handler

```rust
#[instrument(skip(stream, registry))]
async fn handle_connection(
    mut stream: UnixStream,
    registry: Arc<PluginRegistry>,
) -> anyhow::Result<()> {
    let (reader, mut writer) = stream.split();
    let mut reader = BufReader::new(reader);
    let mut line = String::new();

    reader.read_line(&mut line).await?;

    let req: RpcRequest = serde_json::from_str(&line)?;
    if req.method != "execute_task" {
        let resp = RpcResponse::error(req.id, -32601, "method not found");
        writer.write_all(resp.to_json_line().as_bytes()).await?;
        return Ok(());
    }

    let result = registry.dispatch(
        &req.params.task_type,
        &req.params.payload,
    ).await;

    let resp = match result {
        Ok(plugin_result) => {
            let chunks = chunk_output(
                &plugin_result.output,
                req.params.chunking,
            );
            RpcResponse::success(req.id, TaskResponse {
                task_id: req.params.task_id,
                status: plugin_result.status,
                summary: plugin_result.summary,
                chunks,
                stats: TaskStats::default(),
            })
        }
        Err(e) => RpcResponse::error(req.id, e.rpc_code(), &e.to_string()),
    };

    writer.write_all(resp.to_json_line().as_bytes()).await?;
    Ok(())
}
```

### 10. Structured Logging

Replace all `eprintln!` with `tracing`:

```rust
use tracing::{info, warn, error, instrument};

#[instrument(skip(request), fields(task_id = %request.task_id))]
async fn handle_request(request: JsonRequest) -> JsonResult {
    info!("Processing request");
    // ...
}
```

**Dependencies:** `tracing`, `tracing-subscriber`

## Plugin Contract Documentation

New file: `docs/plugin-contract.md`

### Contents

1. **Protocol Overview** — UDS JSON-RPC 2.0 over Unix socket, newline-delimited
2. **Request Format** — `{"jsonrpc":"2.0","method":"execute_task","params":{...},"id":"..."}`
3. **Response Format** — Success: `{"id":"...","result":{...}}`, Error: `{"id":"...","error":{"code":...,"message":"..."}}`
4. **Chunking Protocol** — Large outputs split into chunks with `seq`, `eof_flag`, `data_b64` fields. Client reassembles by seq order.
5. **Error Codes** — Standard JSON-RPC codes (-32700 to -32600) plus custom range (-32000 to -32002)
6. **Lifecycle** — Connect → execute_task (repeat) → disconnect
7. **Health Check** — method: "ping" → response: "pong"
8. **Versioning** — Current: 1.0. Backward-compatible additions only.

### Error Code Reference

| Code | Meaning | Maps to |
|------|---------|---------|
| -32700 | Parse error | Invalid JSON |
| -32600 | Invalid request | Missing required fields |
| -32601 | Method not found | Unknown method |
| -32602 | Invalid params | `PluginError::Config` |
| -32603 | Internal error | `PluginError::Io` |
| -32000 | Server error | `PluginError::Execution` |
| -32001 | Server error | `PluginError::Resource` |
| -32002 | Server error | `PluginError::Unsupported` |

## Testing Requirements

### Rust Unit Tests (per handler)

- `fs_scan`: valid dir → correct stats; nonexistent path → Config error; permission denied → Resource error
- `conn_analyze`: mock /proc/net content → correct grouping by state/protocol/port
- `local_probe`: all pass → overall pass; one warn → overall warn; one fail → overall fail; unknown check type → Config error
- `ebpf_collect`: feature disabled → Unsupported error
- `log_parse`: migrated existing tests
- `text_process`: migrated existing tests

### Rust Registry Tests

- Register + dispatch by task_type → correct handler called
- Dispatch unknown type → Execution error
- Concurrent dispatches → correct results (no data races)

### Rust Integration Tests

- Start runtime → UDS connect → send each task type → verify response format
- Send invalid JSON → verify parse error response
- Send unknown method → verify -32601 error
- Large output → verify chunking produces correct seq/eof_flag

### Go Integration Tests

- `pluginruntime.Client` → send task → verify result
- Concurrent Go goroutines → verify Rust runtime handles parallel requests

## Verification

```bash
# Rust build and test
cd rust-runtime
cargo build
cargo test
cargo clippy -- -D warnings

# With eBPF feature (requires kernel headers)
cargo build --features ebpf
cargo test --features ebpf

# Go integration test
go test -race -tags=integration ./internal/integration/ -run TestPluginRuntime
```

## Implementation Phases

### Phase 1: Foundation (no behavior change)

1. Add new Cargo dependencies: `tokio` (full), `async-trait`, `tracing`, `tracing-subscriber`, `thiserror`, `anyhow`
2. Create `error.rs` — PluginError enum
3. Create `protocol.rs` — extract RPC types from main.rs
4. Create `chunking.rs` — extract chunk logic from main.rs
5. Create `plugin.rs` — Plugin trait definition
6. Create `registry.rs` — PluginRegistry with HashMap dispatch
7. Verify: `cargo build`, `cargo test` (existing tests still pass)

### Phase 2: Migrate existing handlers

8. Create `handlers/mod.rs` — re-export module
9. Create `handlers/log_parse.rs` — LogParsePlugin implements Plugin
10. Create `handlers/text_process.rs` — TextProcessPlugin implements Plugin
11. Refactor `main.rs`: switch to `tokio::main` async, use PluginRegistry for dispatch, use tracing instead of eprintln!
12. Verify: `cargo build`, `cargo test` (all existing tests pass)
13. Update Go integration tests — verify protocol unchanged

### Phase 3: New handlers

14. Create `handlers/fs_scan.rs` — FsScanPlugin
15. Create `handlers/conn_analyze.rs` — ConnAnalyzePlugin
16. Create `handlers/local_probe.rs` — LocalProbePlugin
17. Verify: `cargo test` for each new handler

### Phase 4: eBPF + polish

18. Add eBPF feature flag to Cargo.toml
19. Create `handlers/ebpf_collect.rs` (feature-gated)
20. Write `docs/plugin-contract.md`
21. Add integration tests (UDS round-trip)
22. Verify: `cargo test`, `cargo clippy`, Go integration tests

## Key Files

| File | Action |
|------|--------|
| `rust-runtime/Cargo.toml` | Modify — add dependencies and feature flags |
| `rust-runtime/src/main.rs` | Refactor — async + registry dispatch |
| `rust-runtime/src/error.rs` | New |
| `rust-runtime/src/protocol.rs` | New — extract from main.rs |
| `rust-runtime/src/chunking.rs` | New — extract from main.rs |
| `rust-runtime/src/plugin.rs` | New |
| `rust-runtime/src/registry.rs` | New |
| `rust-runtime/src/handlers/mod.rs` | New |
| `rust-runtime/src/handlers/log_parse.rs` | New — migrated from main.rs |
| `rust-runtime/src/handlers/text_process.rs` | New — migrated from main.rs |
| `rust-runtime/src/handlers/fs_scan.rs` | New |
| `rust-runtime/src/handlers/conn_analyze.rs` | New |
| `rust-runtime/src/handlers/local_probe.rs` | New |
| `rust-runtime/src/handlers/ebpf_collect.rs` | New (feature-gated) |
| `rust-runtime/tests/integration_test.rs` | New |
| `docs/plugin-contract.md` | New |
