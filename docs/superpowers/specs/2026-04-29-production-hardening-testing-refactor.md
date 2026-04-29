# Spec: 生产加固 — 测试与重构

## Context

OpsAgent 核心架构已完成，但整体测试覆盖率仅 43.3%，多个关键模块存在严重覆盖缺口：

| 包 | 当前覆盖率 | 问题 |
|---|---|---|
| `internal/app/` | 0% | Agent 生命周期完全未测试 |
| `internal/grpcclient/` | 28.1% | connectLoop、messageLoop、replayCache 未测试 |
| `internal/collector/` | 53.9% | pipeline 核心，Manager/HostCollector 未覆盖 |
| `internal/collector/processors/` | 43-48% | tagger、regex 测试不足 |
| `internal/pluginruntime/` | 40% | UDS 通信未测试 |
| `internal/sandbox/` | 60.9% | nsjail 调用未 mock |
| `internal/server/` | 72.8% | Start/Shutdown 为 0% |
| `internal/logger/` | 0% | 薄封装层未测试 |

此外，`app/agent.go:Run()` 是一个 ~70 行的混合职责单体，耦合了子系统启动、事件循环、关闭逻辑和 legacy collectAndReport 路径，无法独立测试。`dist/` 目录残留重命名前的 `nodeagentx` 产物。

本 spec 的目标：将测试覆盖率提升到 ≥70%，通过接口抽象和依赖注入重构核心模块使其可测试，移除 legacy 通信路径，清理历史遗留。

## 目标

1. 整体测试覆盖率从 43.3% 提升到 ≥70%（统一标准，排除 `cmd/agent`、`internal/grpcclient/proto` 生成代码、`internal/integration`）
2. 为 Agent 的所有外部依赖定义接口，实现依赖注入
3. `Run()` 拆分为 `startSubsystems` / `eventLoop` / `shutdown` 三个可测试方法
4. 移除 legacy `collectAndReport()` 通信路径
5. 统一错误包装为 `fmt.Errorf("context: %w", err)`
6. 清理所有重命名遗留

## 设计

### 1. 接口定义层

为 Agent 的所有外部依赖定义接口，解耦具体实现：

```go
// internal/app/interfaces.go

// GRPCClient 定义 gRPC 客户端行为
type GRPCClient interface {
    Start(ctx context.Context) error
    Stop()
    SendMetrics(ctx context.Context, metrics []*collector.Metric) error
    SendExecOutput(ctx context.Context, taskID string, data []byte) error
    SendExecResult(ctx context.Context, taskID string, exitCode int, errMsg string) error
    IsConnected() bool
}

// HTTPServer 定义本地 HTTP 服务器行为
type HTTPServer interface {
    Start() error
    Shutdown(ctx context.Context) error
}

// Scheduler 定义采集调度器行为
type Scheduler interface {
    Start(ctx context.Context) (<-chan []*collector.Metric, error)
    Stop()
}

// PluginRuntime 定义插件运行时行为
type PluginRuntime interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
}
```

现有 `grpcclient.Client`、`server.Server`、`collector.Scheduler`、`pluginruntime.Runtime` 自然满足这些接口（Go 隐式接口），无需修改它们的代码。

### 2. Agent 重构 + 依赖注入

**Agent 结构体改造：**

```go
type Agent struct {
    cfg           *config.Config
    logger        *zerolog.Logger
    grpcClient    GRPCClient
    server        HTTPServer
    scheduler     Scheduler
    pluginRuntime PluginRuntime
    dispatcher    *task.Dispatcher
    executor      *executor.Executor
    reporter      reporter.Reporter
}
```

**Functional Options 模式：**

```go
type Option func(*Agent)

func WithGRPCClient(c GRPCClient) Option      { return func(a *Agent) { a.grpcClient = c } }
func WithServer(s HTTPServer) Option           { return func(a *Agent) { a.server = s } }
func WithScheduler(s Scheduler) Option         { return func(a *Agent) { a.scheduler = s } }
func WithPluginRuntime(r PluginRuntime) Option { return func(a *Agent) { a.pluginRuntime = r } }

func NewAgent(cfg *config.Config, logger *zerolog.Logger, opts ...Option) (*Agent, error) {
    a := &Agent{cfg: cfg, logger: logger}
    // 应用 options
    for _, opt := range opts {
        opt(a)
    }
    // 未被注入的依赖使用默认实现
    if a.grpcClient == nil {
        // 创建默认 grpcclient.Client
    }
    // ... 其他依赖同理
}
```

**Run() 拆分：**

```go
func (a *Agent) Run(ctx context.Context) error {
    if err := a.startSubsystems(ctx); err != nil {
        return fmt.Errorf("starting subsystems: %w", err)
    }
    defer a.shutdown(ctx)
    a.eventLoop(ctx)
    return nil
}

// startSubsystems 按序启动所有子系统
func (a *Agent) startSubsystems(ctx context.Context) error {
    // 1. PluginRuntime.Start(ctx)
    // 2. Scheduler.Start(ctx) → pipelineCh
    // 3. GRPCClient.Start(ctx)
    // 4. HTTPServer.Start() in goroutine → errCh
}

// eventLoop 运行主事件循环（无 legacy ticker）
func (a *Agent) eventLoop(ctx context.Context) {
    // select on ctx.Done(), errCh, pipelineCh
    // pipelineCh → grpcClient.SendMetrics()
}

// shutdown 逆序关闭所有子系统
func (a *Agent) shutdown(ctx context.Context) {
    // 4. HTTPServer.Shutdown(ctx)
    // 3. GRPCClient.Stop()
    // 2. Scheduler.Stop()
    // 1. PluginRuntime.Stop(ctx)
}
```

**移除 legacy 路径：** 删除 `collectAndReport()`、`ticker`、`Manager` 依赖。Agent 仅通过 gRPC pipeline 通信。

### 3. gRPC 客户端测试

当前 28.1% 覆盖率，核心缺口在 `connectLoop`、`messageLoop`、`replayCache`。

**Mock Stream 设计：**

```go
// internal/grpcclient/client_test.go

type mockStream struct {
    agentpb.AgentService_ConnectClient
    sendFn    func(*agentpb.PlatformMessage) error
    recvFn    func() (*agentpb.AgentMessage, error)
    sendCount atomic.Int32
}

func (m *mockStream) Send(msg *agentpb.PlatformMessage) error {
    m.sendCount.Add(1)
    return m.sendFn(msg)
}
func (m *mockStream) Recv() (*agentpb.AgentMessage, error) { return m.recvFn() }
```

**测试矩阵（table-driven）：**

| 测试目标 | 场景 |
|---------|------|
| `connectLoop` | 首次连接成功、连接失败退避（1s→2s→4s→max）、context 取消退出、成功重连后重置退避 |
| `messageLoop` | 心跳发送、收到消息分发到 receiver、stream 错误触发重连、context 取消退出 |
| `replayCache` | 缓存为空跳过、缓存有数据发送 batch、发送失败重新缓存 |
| `connect` | TLS 配置正确、注册消息发送、replayCache 调用 |
| `Start/Stop` | goroutine 启动、优雅停止、重复调用安全 |

**关键文件：**
- `internal/grpcclient/client.go` — connectLoop, messageLoop, replayCache, connect, Start, Stop
- `internal/grpcclient/receiver.go` — handler 分发
- `internal/grpcclient/sender.go` — proto 转换

### 4. Server 测试

当前 72.8%，但 `Start()` 和 `Shutdown()` 为 0%。

- `Start()` — 用随机端口测试，验证监听和路由注册
- `Shutdown()` — 验证优雅关闭、活跃连接处理
- `handlePrometheusMetrics()` — 用 `httptest.NewRequest` + `httptest.NewRecorder` 测试指标格式
- `metricsSnapshot()` — 测试并发读写安全

### 5. PluginRuntime 测试

当前 40%。

- 定义 `UDSConnection` 接口，mock UDS socket 通信
- `Start()` — 测试进程启动、连接建立
- `Stop()` — 测试优雅关闭、超时处理
- 连接失败重试逻辑
- 集成测试用 `//go:build integration` tag 隔离

### 6. Sandbox 测试

当前 60.9%。采用单元测试 mock + 集成测试隔离策略。

- 单元测试 mock `exec.Command` 调用，不需要真实 nsjail 环境
- `WriteConfigFile()` — 测试配置生成
- `buildConfigContent()` — 测试 nsjail 参数构建
- `Policy` — 扩展现有 shell injection 测试，补充 allow/block list 边界
- `KillCgroupProcesses()` — mock /proc 文件系统
- 集成测试用 `//go:build integration` tag，需要 nsjail + sudo 环境

### 7. Logger 测试

当前 0%。薄封装层，测试简单。

- 测试各级别日志输出格式（Debug/Info/Warn/Error）
- 测试 context 字段注入
- 测试 WithContext 返回新 logger

### 8. Collector 核心补充测试

当前 53.9%。

- `Manager.CollectAll()` — mock input，验证聚合和错误处理
- `HostCollector` — mock gopsutil，验证指标收集
- `buffer.go` — ring buffer 边界测试（空、满、溢出、并发读写）

### 9. Processor 补充测试

当前 tagger 43.2%、regex 47.6%。

**Tagger：**
- tag 覆盖（已有 tag 被新 tag 覆盖）
- 空 tag 列表
- nil metric 处理

**Regex：**
- 无匹配场景
- 多匹配场景
- 捕获组提取
- 无效正则表达式

### 10. Config 补充测试

当前 74.6%，已达标，补充边界值测试：

- 端口边界值（0, 65535, 负数, 超范围）
- 超长字符串
- 嵌套验证（sandbox 策略仅在 sandbox 启用时验证）
- 交叉字段依赖（reporter.mode=http 时 reporter.endpoint 必填）

## 清理工作

| 项目 | 操作 |
|------|------|
| `dist/` | `make clean` 删除 nodeagentx 产物 |
| `scripts/package.sh` | 检查命名模板是否已更新为 opsagent |
| 错误包装 | 全局审计 `fmt.Errorf`，统一使用 `%w` 包装 |
| blank import | 验证 `agent.go` 中所有 input 插件的 blank import 正确 |
| legacy 代码 | 删除 `collectAndReport()`、`Manager` 引用、相关 ticker 逻辑 |

**错误包装规范：**

```go
// 正确：保留上下文和错误链
return fmt.Errorf("connecting to gRPC server: %w", err)

// 错误：丢失错误链
return fmt.Errorf("connect failed: %v", err)

// 错误：缺少上下文
return err
```

## 测试要求

- 所有新测试使用 table-driven 模式（`t.Run()` 子测试 + 结构体切片）
- Mock 使用接口注入，不使用 monkey patching
- 集成测试使用 `//go:build integration` tag 隔离
- `go test -race ./...` 全部通过
- `make ci` 通过（tidy → vet → test-race → security）

## 执行顺序

| 阶段 | 内容 | 验证点 |
|------|------|--------|
| Phase 1 | 定义接口 + 清理 dist/ 遗留 | `go vet ./...` 通过 |
| Phase 2 | 重构 Agent（DI + Run 拆分 + 移除 legacy） | `go build ./...` 通过，现有集成测试通过 |
| Phase 3 | grpcclient 测试 | 覆盖率 ≥70% |
| Phase 4 | server/pluginruntime/sandbox/logger 测试 | 各包 ≥70% |
| Phase 5 | collector/processor/config 测试补充 | 各包 ≥70% |
| Phase 6 | 错误包装统一 + 最终清理 | `make ci` 通过 |

## 验证方式

```bash
# 每阶段验证
go test -race ./...
go tool cover -func=coverage.out | tail -1
# 期望: total: (statements) >= 70.0%
# 注意: 排除 cmd/、internal/grpcclient/proto/、internal/integration/

# 最终验证
make ci
go test -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
# 期望: total: (statements) >= 70.0%
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/app/interfaces.go` | 新建，接口定义 |
| `internal/app/agent.go` | 重构 Run()，移除 legacy，DI 改造 |
| `internal/app/agent_test.go` | 新建，生命周期测试 |
| `internal/grpcclient/client_test.go` | 扩展，connectLoop/messageLoop 测试 |
| `internal/grpcclient/receiver_test.go` | 新建或扩展 |
| `internal/grpcclient/sender_test.go` | 扩展 |
| `internal/server/server_test.go` | 扩展，Start/Shutdown 测试 |
| `internal/pluginruntime/runtime_test.go` | 新建或扩展 |
| `internal/sandbox/*_test.go` | 扩展 |
| `internal/logger/logger_test.go` | 新建 |
| `internal/collector/scheduler_test.go` | 扩展 |
| `internal/collector/processors/*_test.go` | 扩展 |
| `internal/config/config_test.go` | 扩展 |
| `dist/` | 清理 |
