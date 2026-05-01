# Spec 7: 平台成熟度

## Context

前 6 个 spec 完成了核心功能和扩展能力。本 spec 聚焦于生产级可观测性、运维工具和长期可维护性 — 这些是将 OpsAgent 从"功能完整"提升到"生产就绪"的关键。

**依赖：** Spec 1-6 全部

## 目标

1. Agent 自指标通过 Prometheus 端点暴露（用 `prometheus/client_golang` 替换现有手动拼接方案）
2. JSON-lines 审计日志覆盖所有重要事件
3. CLI 工具支持配置验证、dry-run、插件列表
4. CI 强制 80% 覆盖率
5. E2E 测试覆盖完整 Agent 生命周期（9 步）
6. 基准测试建立性能基线

## 设计

### 1. Agent 自指标

用 `prometheus/client_golang` 替换现有 `internal/server/prometheus.go` 的手动拼接方案。所有指标统一注册到自定义 `prometheus.Registry`，由 server 暴露。

新增 `internal/app/metrics.go`：

```go
package app

import "github.com/prometheus/client_golang/prometheus"

var (
    // Agent 自指标
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

    tasksFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "opsagent_tasks_failed_total",
        Help: "Total failed tasks",
    }, []string{"task_type", "error_code"})

    metricsCollected = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_metrics_collected_total",
        Help: "Total metrics collected by pipeline",
    })

    pipelineErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "opsagent_pipeline_errors_total",
        Help: "Total pipeline processing errors",
    }, []string{"stage", "plugin"})

    pluginRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "opsagent_plugin_requests_total",
        Help: "Total plugin runtime requests",
    }, []string{"plugin", "task_type", "status"})

    gRPCReconnects = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_grpc_reconnects_total",
        Help: "Total gRPC reconnection attempts",
    })

    // 系统指标（替换现有 prometheus.go 中手动渲染的部分）
    cpuUsage = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_cpu_usage_percent",
        Help: "CPU usage percent",
    })

    memoryUsage = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_memory_usage_percent",
        Help: "Memory usage percent",
    })

    diskUsage = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_disk_usage_percent",
        Help: "Disk usage percent",
    })

    load1 = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_load1",
        Help: "Host load average over 1 minute",
    })

    load5 = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_load5",
        Help: "Host load average over 5 minutes",
    })

    load15 = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "opsagent_load15",
        Help: "Host load average over 15 minutes",
    })

    networkBytesSent = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_network_bytes_sent_total",
        Help: "Total bytes sent",
    })

    networkBytesRecv = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "opsagent_network_bytes_recv_total",
        Help: "Total bytes received",
    })
)
```

**关键设计点：**

- **Registry 隔离** — 创建自定义 `prometheus.Registry` 而非使用默认全局注册表，避免与第三方库指标冲突
- **系统指标迁移** — 现有 `prometheus.go` 中的 CPU/内存/磁盘等指标改为 Gauge/Counter 类型注册到同一 Registry，由 Scheduler 每次 gather 后更新。注意：网络指标从 `opsagent_network_bytes_sent` 改为 `opsagent_network_bytes_sent_total`（Counter 的 Prometheus 命名规范），这是有意的 breaking change
- **Server 注入** — `server.Server` 新增 `promRegistry *prometheus.Registry` 字段，在 `server.New()` 的 Options 中传入，`handlePrometheusMetrics` 使用该字段调用 `Gather()`
- **集成点：**
  - `Agent.Run()` 启动时注册所有指标到 Registry
  - Scheduler 每次 gather 后递增 `metricsCollected` 并更新系统指标 Gauge
  - Task dispatcher 递增 `tasksCompleted`/`tasksFailed`
  - gRPC client 连接/断开时更新 `grpcConnected`
  - Plugin runtime 请求后递增 `pluginRequests`

**Server 端改造 `internal/server/prometheus.go`：**

```go
func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
    gathered, err := s.promRegistry.Gather()
    if err != nil {
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", string(expfmt.FmtText))
    expfmt.MetricFamilyToText(w, gathered)
}
```

删除现有的 `renderPrometheus()` 函数和手动拼接逻辑。

### 2. 结构化审计日志

新增 `internal/app/audit.go`：

```go
package app

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

func NewAuditLogger(path string, maxSizeMB, maxBackups int) (*AuditLogger, error)
func (a *AuditLogger) Log(event AuditEvent)
func (a *AuditLogger) Close() error
```

**文件轮转：** 使用 `gopkg.in/natefinish/lumberjack.v2` 实现，配置项包含 `max_size_mb` 和 `max_backups`。

**注入方式：** Agent 持有 `*AuditLogger`，在 `NewAgent()` 中根据配置创建，通过闭包注入到各子系统：

```go
// Agent 结构体新增
auditLog *AuditLogger

// NewAgent 中创建
if cfg.Agent.AuditLog.Enabled {
    a.auditLog, err = NewAuditLogger(cfg.Agent.AuditLog.Path,
        cfg.Agent.AuditLog.MaxSizeMB, cfg.Agent.AuditLog.MaxBackups)
}

// 注入到 dispatcher 任务处理器
dispatcher.Register(task.TypeExecCommand, func(ctx context.Context, t task.AgentTask) (any, error) {
    // ... 执行逻辑 ...
    a.auditLog.Log(AuditEvent{
        EventType: "task.completed",
        Component: "dispatcher",
        Action:    "exec_command",
        Status:    "success",
        Details:   map[string]interface{}{"task_id": t.TaskID},
    })
})
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

**接口扩展 `internal/app/interfaces.go`：**

```go
// SubsystemStatus 描述单个子系统的健康状态。
type SubsystemStatus struct {
    Status  string         `json:"status"` // running, connected, stopped, error
    Details map[string]any `json:"details,omitempty"`
}

// HealthStatuser 由需要报告健康状态的子系统实现。
type HealthStatuser interface {
    HealthStatus() SubsystemStatus
}

// 扩展现有接口
type GRPCClient interface {
    // ... 现有方法 ...
    HealthStatus() SubsystemStatus
}

type Scheduler interface {
    // ... 现有方法 ...
    HealthStatus() SubsystemStatus
}

type PluginRuntime interface {
    // ... 现有方法 ...
    HealthStatus() SubsystemStatus
}

type PluginGateway interface {
    // ... 现有方法 ...
    HealthStatus() SubsystemStatus
}
```

**各子系统实现要点：**

- `grpcclient.Client.HealthStatus()` — 返回 connected/disconnected，附带 `last_heartbeat` 时间
- `collector.Scheduler.HealthStatus()` — 返回 running/stopped，附带 `inputs_active` 数量和 `last_collection` 时间
- `pluginruntime.Runtime.HealthStatus()` — 返回 running/stopped，附带 `plugins_loaded` 数量
- `pluginruntime.Gateway.HealthStatus()` — 返回 running/stopped，附带已加载插件列表

修改 `internal/server/handlers.go` 的 `/healthz` 端点，Agent 传入各子系统的 `HealthStatuser` 引用：

```json
{
    "status": "healthy",
    "version": "1.0.0",
    "git_commit": "abc1234",
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

- `healthy` — 所有子系统正常（status == running/connected/available）
- `degraded` — 非核心子系统（plugin_runtime/sandbox）异常但核心功能可用
- `unhealthy` — 核心子系统（grpc/scheduler）不可用

**版本信息注入** — 扩展 `var Version` 为：

```go
var (
    Version   = "dev"
    GitCommit = "unknown"
    BuildTime = "unknown"
)
```

Makefile 的 build 目标更新 ldflags：

```makefile
build:
	go build -ldflags="-s -w \
		-X github.com/cy77cc/opsagent/internal/app.Version=$(VERSION) \
		-X github.com/cy77cc/opsagent/internal/app.GitCommit=$(shell git rev-parse --short HEAD) \
		-X github.com/cy77cc/opsagent/internal/app.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bin/$(APP_NAME) ./cmd/agent
```

### 4. CLI 工具

扩展 `cmd/agent/main.go`，用 cobra 添加子命令。

#### `opsagent validate`

```bash
$ opsagent validate --config /etc/opsagent/config.yaml
✓ Config loaded successfully
✓ All inputs initialized (cpu, memory, disk, net, load)
✓ All processors initialized (tagger, regex)
✓ All aggregators initialized (avg)
✓ All outputs initialized (prometheus)
✓ Validation passed

Resolved config:
  agent.id: "my-host"
  agent.interval_seconds: 10
  server.listen_addr: ":18080"
  grpc.endpoint: "ops-pilot.example.com:9090"
  plugin.enabled: true
  sandbox.enabled: false
```

**实现方式：** 复用 `config.Load()` + `buildScheduler()` 的初始化逻辑，但不启动任何服务。输出所有解析后的配置项。验证失败时 exit code = 1。

#### `opsagent run --dry-run`

```bash
$ opsagent run --config /etc/opsagent/config.yaml --dry-run
Loading config... OK
Initializing plugins... OK
Starting collector pipeline...
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

**实现方式：** 在 `runCmd` 中添加 `--dry-run` flag。当设置时：
1. 创建 Agent 但不启动 gRPC 和 HTTP server
2. 调用 scheduler.Start() 获取 pipeline channel
3. 从 channel 读取一次指标输出
4. 格式化输出后调用 scheduler.Stop() 并退出

需要在 Agent 上新增 `RunOnce()` 方法处理 dry-run 路径。

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

**实现方式：**
- 内置插件：从 `collector.DefaultRegistry` 枚举已注册的 input/processor/aggregator/output
- 自定义插件：如果 PluginGateway 启用，调用 `gateway.ListPlugins()` 获取名称、版本、状态、task types
- 运行时状态：调用 `pluginRuntime.HealthStatus()` 获取连接信息

#### 版本命令增强

```bash
$ opsagent version
opsagent v1.2.3 (commit: abc1234, built: 2026-04-29T10:00:00Z)
```

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

新增独立 job：

```yaml
  rust:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@stable
        with:
          components: clippy
      - name: Build
        run: cd rust-runtime && cargo build --release
      - name: Test
        run: cd rust-runtime && cargo test
      - name: Clippy
        run: cd rust-runtime && cargo clippy -- -D warnings
      - name: Audit
        run: cd rust-runtime && cargo audit
```

#### 完整 CI 流程

```yaml
jobs:
  go:
    # ... 现有步骤 + 覆盖率门禁 ...

  rust:
    # ... Rust job ...

  integration:
    needs: [go, rust]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.1'
      - name: Integration tests
        run: go test ./internal/integration/... -v -race -count=1 -timeout 120s
```

### 6. E2E 测试

新增 `internal/integration/e2e_test.go`，带 `//go:build e2e` 标签：

```go
//go:build e2e

func TestAgentFullLifecycle(t *testing.T) {
    // 1. 启动 mock gRPC server
    mockServer := startMockGRPCServer(t)
    defer mockServer.Stop()

    // 2. 用测试配置启动 Agent
    cfg := loadTestConfig(t, mockServer.Address())
    agent, err := app.NewAgent(cfg, testLogger)
    require.NoError(t, err)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go func() { _ = agent.Run(ctx) }()
    waitForReady(t, agent)

    // 3. 验证 Agent 注册到 server
    assert.Eventually(t, func() bool {
        return mockServer.HasRegisteredAgent("test-agent")
    }, 5*time.Second, 100*time.Millisecond)

    // 4. 发送 exec_command 任务 → 验证响应
    resp := sendTask(t, agent, task.AgentTask{
        Type: task.TypeExecCommand,
        Payload: map[string]any{"command": "echo", "args": []any{"hello"}},
    })
    assert.Equal(t, 0, resp.ExitCode)

    // 5. 发送 sandbox_exec 任务 → 验证响应（需 nsjail 可用）
    if sandboxAvailable() {
        resp = sendTask(t, agent, task.AgentTask{
            Type: task.TypeSandboxExec,
            Payload: map[string]any{"command": "echo", "args": []any{"sandbox"}},
        })
        assert.Equal(t, 0, resp.ExitCode)
    }

    // 6. 发送 ConfigUpdate → 验证热更新
    mockServer.SendConfigUpdate(&pb.ConfigUpdate{
        Version:    2,
        ConfigYaml: loadUpdatedConfigYAML(t),
    })
    assert.Eventually(t, func() bool {
        return agent.ConfigReloader().Version() == 2
    }, 5*time.Second, 100*time.Millisecond)

    // 7. 验证指标收集正常运行
    assert.Eventually(t, func() bool {
        return mockServer.MetricsReceived() > 0
    }, 15*time.Second, 500*time.Millisecond)

    // 8. 发送关闭信号 → 验证优雅关闭
    cancel()

    // 9. 验证所有子系统已停止
    assert.Eventually(t, func() bool {
        return agent.IsShutdownComplete()
    }, 10*time.Second, 500*time.Millisecond)
}
```

**辅助设施：**

- `startMockGRPCServer` — 实现 proto.AgentServiceServer 的最小 mock，支持 Register、SendMetrics、SendConfigUpdate
- `loadTestConfig` — 返回指向 mock server 的测试配置
- `waitForReady` — 轮询 /healthz 直到返回 healthy
- `sendTask` — 通过 HTTP API (`POST /api/v1/tasks`) 派发任务，测试完整请求路径
- `sandboxAvailable()` — 检测 nsjail 是否可用，不可用则跳过步骤 5

**Agent 需要暴露的测试辅助方法：**

```go
func (a *Agent) IsShutdownComplete() bool  // shutdown 流程完成后返回 true
```

### 7. 基准测试

新增 `internal/collector/benchmark_test.go`：

```go
func BenchmarkMetricCollection(b *testing.B) {
    // 10 inputs, 2 processors, 1 aggregator, 1 output
    // 测量: metrics/second, latency p50/p99
    scheduler := buildBenchmarkScheduler(b)
    ch := scheduler.Start(context.Background())
    defer scheduler.Stop()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        <-ch
    }
}

func BenchmarkPipelineProcessing(b *testing.B) {
    // 单独测量 processor chain 吞吐量
    processors := buildBenchmarkProcessors(b)
    metrics := generateBenchmarkMetrics(100)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        for _, p := range processors {
            metrics = p.Process(metrics)
        }
    }
}

func BenchmarkGRPCSerialization(b *testing.B) {
    // 测量 Metric.ToProto() + proto.Marshal() 的性能
    m := generateSampleMetric()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pb := m.ToProto()
        _, _ = proto.Marshal(pb)
    }
}
```

**Makefile 新增目标：**

```makefile
bench:
	go test -bench=. -benchmem -count=3 ./internal/collector/

e2e:
	go test -tags=e2e -v -race -count=1 -timeout 120s ./internal/integration/
```

## 测试要求

- Agent 自指标正确递增和暴露（通过 client_golang Registry）
- 审计日志覆盖所有 18 种事件类型
- CLI 命令（validate、dry-run、plugins）在各种输入下正确工作
- E2E 测试覆盖全部 9 步生命周期
- 基准测试有基线数值
- 健康检查返回正确的子系统状态和 overall 判定

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
go test -bench=. -benchmem ./internal/collector/
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/app/metrics.go` | 新建 — Prometheus 自指标（client_golang） |
| `internal/app/metrics_test.go` | 新建 |
| `internal/app/audit.go` | 新建 — 审计日志 |
| `internal/app/audit_test.go` | 新建 |
| `internal/server/prometheus.go` | 重写 — 从手动拼接改为 client_golang Registry |
| `internal/server/handlers.go` | 修改 — 健康检查增强（子系统状态） |
| `internal/app/interfaces.go` | 修改 — 添加 HealthStatus() 到各接口 |
| `cmd/agent/main.go` | 修改 — 添加 validate、plugins 子命令，dry-run 标志 |
| `Makefile` | 修改 — ldflags 注入 GitCommit/BuildTime，新增 bench/e2e 目标 |
| `.github/workflows/ci.yml` | 修改 — 覆盖率门禁、Rust CI、集成测试 job |
| `internal/integration/e2e_test.go` | 新建 — 9 步 E2E 测试 |
| `internal/collector/benchmark_test.go` | 新建 — 性能基线基准测试 |
