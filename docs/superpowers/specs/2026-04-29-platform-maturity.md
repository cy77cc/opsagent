# Spec 7: 平台成熟度

## Context

前 6 个 spec 完成了核心功能和扩展能力。本 spec 聚焦于生产级可观测性、运维工具和长期可维护性 — 这些是将 OpsAgent 从"功能完整"提升到"生产就绪"的关键。

**依赖：** Spec 1-6 全部

## 目标

1. Agent 自指标通过 Prometheus 端点暴露
2. JSON-lines 审计日志覆盖所有重要事件
3. CLI 工具支持配置验证、dry-run、插件列表
4. CI 强制 80% 覆盖率
5. E2E 测试覆盖完整 Agent 生命周期
6. 基准测试建立性能基线

## 设计

### 1. Agent 自指标

新增 `internal/app/metrics.go`：

```go
package app

import "github.com/prometheus/client_golang/prometheus"

var (
    uptime = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_uptime_seconds",
        Help: "Agent uptime in seconds",
    })

    grpcConnected = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_grpc_connected",
        Help: "Whether gRPC connection is active (1=connected, 0=disconnected)",
    })

    tasksRunning = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_tasks_running",
        Help: "Number of currently running tasks",
    })

    tasksCompleted = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_tasks_completed_total",
        Help: "Total completed tasks",
    })

    tasksFailed = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_tasks_failed_total",
        Help: "Total failed tasks",
        // Label: task_type, error_code
    })

    metricsCollected = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_metrics_collected_total",
        Help: "Total metrics collected by pipeline",
    })

    pipelineErrors = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_pipeline_errors_total",
        Help: "Total pipeline processing errors",
        // Label: stage (input/processor/aggregator/output), plugin
    })

    pluginRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "opsagent_plugin_requests_total",
        Help: "Total plugin runtime requests",
    }, []string{"plugin", "task_type", "status"})

    gRPCReconnects = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_grpc_reconnects_total",
        Help: "Total gRPC reconnection attempts",
    })
)
```

**集成点：**
- `Agent.Run()` 启动时注册指标
- Scheduler 每次 gather 后递增 `metricsCollected`
- Task dispatcher 递增 `tasksCompleted`/`tasksFailed`
- gRPC client 连接/断开时更新 `grpcConnected`
- Plugin runtime 请求后递增 `pluginRequests`

### 2. 结构化审计日志

新增 `internal/app/audit.go`：

```go
type AuditLogger struct {
    logger zerolog.Logger
}

type AuditEvent struct {
    Timestamp   time.Time              `json:"timestamp"`
    EventType   string                 `json:"event_type"`
    Component   string                 `json:"component"`
    Action      string                 `json:"action"`
    Status      string                 `json:"status"` // success, failure
    Details     map[string]interface{} `json:"details,omitempty"`
    Error       string                 `json:"error,omitempty"`
}
```

**审计事件类型：**

| 事件类型 | 组件 | 触发时机 |
|----------|------|----------|
| `config.loaded` | agent | 启动加载配置 |
| `config.reloaded` | agent | 热更新配置 |
| `config.rejected` | agent | 配置变更被拒绝 |
| `task.started` | dispatcher | 任务开始执行 |
| `task.completed` | dispatcher | 任务完成 |
| `task.failed` | dispatcher | 任务失败 |
| `task.cancelled` | dispatcher | 任务被取消 |
| `grpc.connected` | grpcclient | gRPC 连接建立 |
| `grpc.disconnected` | grpcclient | gRPC 连接断开 |
| `grpc.reconnecting` | grpcclient | gRPC 重连中 |
| `plugin.started` | pluginruntime | 插件进程启动 |
| `plugin.stopped` | pluginruntime | 插件进程停止 |
| `plugin.crashed` | pluginruntime | 插件进程崩溃 |
| `sandbox.executed` | sandbox | 沙箱命令执行 |
| `sandbox.blocked` | sandbox | 沙箱策略拒绝 |
| `agent.started` | agent | Agent 启动 |
| `agent.shutting_down` | agent | Agent 开始关闭 |
| `agent.stopped` | agent | Agent 已停止 |

**输出格式：** JSON-lines，每行一个事件，写入独立的审计日志文件。

**配置：**
```yaml
agent:
  audit_log:
    enabled: true
    path: "/var/log/opsagent/audit.jsonl"
    max_size_mb: 100
    max_backups: 5
```

### 3. 健康检查增强

修改 `internal/server/handlers.go` 的 `/healthz` 端点：

```json
{
    "status": "healthy",
    "version": "1.0.0",
    "uptime_seconds": 3600,
    "subsystems": {
        "grpc": {
            "status": "connected",
            "last_heartbeat": "2026-04-29T10:00:00Z"
        },
        "scheduler": {
            "status": "running",
            "inputs_active": 5,
            "last_collection": "2026-04-29T10:00:05Z"
        },
        "plugin_runtime": {
            "status": "running",
            "plugins_loaded": 2
        },
        "sandbox": {
            "status": "available",
            "active_tasks": 0
        }
    }
}
```

**状态判定：**
- `healthy` — 所有子系统正常
- `degraded` — 部分子系统异常但核心功能可用
- `unhealthy` — 核心子系统不可用

### 4. CLI 工具

扩展 `cmd/agent/main.go` 添加子命令：

#### `opsagent validate`
```bash
$ opsagent validate --config /etc/opsagent/config.yaml
✓ Config loaded successfully
✓ Validation passed

Resolved config:
  agent.id: "my-host"
  server.listen_addr: ":18080"
  grpc.endpoint: "ops-pilot.example.com:9090"
  ...
```

#### `opsagent run --dry-run`
```bash
$ opsagent run --config /etc/opsagent/config.yaml --dry-run
Loading config... OK
Initializing plugins... OK
Running one collection cycle...

Collected metrics:
  cpu: 4 fields, 2 tags
  memory: 6 fields, 0 tags
  disk: 4 fields, 3 tags
  net: 5 fields, 2 tags
  load: 3 fields, 0 tags

Total: 22 metrics from 5 inputs
Exiting (dry-run mode).
```

#### `opsagent plugins`
```bash
$ opsagent plugins
Built-in plugins:
  INPUTS:      cpu, memory, disk, net, process, load, diskio, temp, gpu, connections
  PROCESSORS:  tagger, regex, delta
  AGGREGATORS: avg, sum, minmax, percentile
  OUTPUTS:     http, prometheus, promrw

Custom plugins:
  my-audit-plugin v1.0.0 [running] tasks: custom_audit, custom_report
  my-monitor v2.1.0 [running] tasks: custom_monitor

Plugin runtime: Rust (UDS) — connected, 6 handlers
```

#### 版本兼容检查

在 gRPC 注册消息中包含版本信息：

```go
registration := &proto.AgentRegistration{
    AgentId:   a.cfg.Agent.ID,
    Hostname:  hostname,
    Version:   version.Version,  // 从 ldflags 注入
    GitCommit: version.GitCommit,
    BuildTime: version.BuildTime,
}
```

平台端可以比较版本并警告不兼容。

### 5. CI/CD 增强

#### 覆盖率门禁

修改 `.github/workflows/ci.yml`：

```yaml
- name: Test with coverage
  run: |
    go test -race -coverprofile=coverage.out -covermode=atomic ./...
    COVERAGE=$(go tool cover -func=coverage.out | tail -1 | awk '{print $3}' | sed 's/%//')
    echo "Coverage: ${COVERAGE}%"
    if (( $(echo "$COVERAGE < 80" | bc -l) )); then
      echo "::error::Coverage ${COVERAGE}% is below 80% minimum"
      exit 1
    fi
```

#### Rust CI

```yaml
- name: Rust tests
  run: |
    cd rust-runtime
    cargo test
    cargo clippy -- -D warnings
    cargo audit
```

#### E2E 测试套件

新增 `internal/integration/e2e_test.go`：

```go
//go:build e2e

func TestAgentFullLifecycle(t *testing.T) {
    // 1. Start mock gRPC server
    // 2. Start agent with test config
    // 3. Verify agent registers with server
    // 4. Send task: exec_command → verify response
    // 5. Send task: sandbox_exec → verify response
    // 6. Send ConfigUpdate → verify hot-reload
    // 7. Verify metrics collection running
    // 8. Send shutdown signal → verify graceful shutdown
    // 9. Verify all subsystems stopped
}
```

#### 基准测试

新增 `internal/collector/benchmark_test.go`：

```go
func BenchmarkMetricCollection(b *testing.B) {
    // Benchmark: 10 inputs, 2 processors, 1 aggregator, 1 output
    // Measure: metrics/second, latency p50/p99
}

func BenchmarkPipelineProcessing(b *testing.B) {
    // Benchmark: processor chain throughput
}

func BenchmarkGRPCSerialization(b *testing.B) {
    // Benchmark: Metric.ToProto() + proto.Marshal()
}
```

## 测试要求

- Agent 自指标正确递增和暴露
- 审计日志覆盖所有事件类型
- CLI 命令在各种输入下正确工作
- E2E 测试覆盖主要用户流程
- 基准测试有基线数值

## 验证方式

```bash
# 自指标验证
go test -race ./internal/app/ -run TestMetrics
curl http://localhost:18080/metrics | grep opsagent_

# 审计日志验证
go test -race ./internal/app/ -run TestAudit
cat /var/log/opsagent/audit.jsonl | jq .

# CLI 验证
go run ./cmd/agent validate --config configs/config.yaml
go run ./cmd/agent plugins
go run ./cmd/agent run --config configs/config.yaml --dry-run

# E2E 测试
go test -race -tags=e2e ./internal/integration/ -run TestAgentFullLifecycle

# 基准测试
go test -bench=. ./internal/collector/ -benchmem
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/app/metrics.go` | 新建 — Prometheus 自指标 |
| `internal/app/metrics_test.go` | 新建 |
| `internal/app/audit.go` | 新建 — 审计日志 |
| `internal/app/audit_test.go` | 新建 |
| `internal/server/handlers.go` | 修改 — 健康检查增强 |
| `cmd/agent/main.go` | 修改 — 添加子命令 |
| `.github/workflows/ci.yml` | 修改 — 覆盖率门禁、Rust CI |
| `internal/integration/e2e_test.go` | 新建 — E2E 测试 |
| `internal/collector/benchmark_test.go` | 新建 — 基准测试 |
