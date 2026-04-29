# Spec 2: 配置热更新与优雅关闭

## Context

OpsAgent 的 proto 定义了 `ConfigUpdate` 消息（含 `config_yaml` bytes + `version` int64），gRPC receiver 已接收到该消息，但 handler（`agent.go:657`）仅做日志记录和 ack，没有实际重载配置。同时，当前关闭流程存在缺陷：不排空 pipeline、不刷新 gRPC 缓存、不协调进行中的任务。

本 spec 实现安全的部分配置热加载和有序的优雅关闭。

**依赖：** Spec 1（测试覆盖使重构安全）

## 目标

1. 实现配置热更新，支持 collector/reporter/auth/prometheus 部分重载
2. 不可重载配置变更被拒绝并给出清晰错误
3. 关闭时按序排空所有子系统、刷新 gRPC 缓存
4. 关闭超时可配置

## 设计

### 1. 配置字段分类

将 80+ 配置字段分为两类：

**可重载（运行时安全变更）：**
- `collector.inputs` — 输入插件配置
- `collector.processors` — 处理器配置
- `collector.aggregators` — 聚合器配置
- `collector.outputs` — 输出插件配置
- `reporter.*` — 上报策略
- `auth.*` — 认证令牌
- `prometheus.*` — Prometheus 导出配置

**不可重载（需重启）：**
- `agent.*` — Agent 基础配置
- `server.listen_addr` — HTTP 监听地址
- `grpc.*` — gRPC 连接配置
- `sandbox.*` — 沙箱配置
- `plugin.*` — 插件运行时配置
- `executor.*` — 本地执行器配置

### 2. 配置差异引擎

新增 `internal/config/reload.go`：

```go
// ReloadPlan 描述可应用的配置变更
type ReloadPlan struct {
    CollectorChanged  bool
    ReporterChanged   bool
    AuthChanged       bool
    PrometheusChanged bool
    NewConfig         *Config
}

// Diff 对比新旧配置，返回可重载的变更计划和不可重载的字段列表
func Diff(old, new *Config) (plan *ReloadPlan, nonReloadable []string, err error)
```

**实现逻辑：**
1. 逐字段对比（使用 reflect 或手写比较）
2. 变更字段分类到可重载/不可重载
3. 可重载变更打包为 `ReloadPlan`
4. 不可重载变更收集到 `nonReloadable` 列表
5. 新配置必须通过 `Validate()` 检查

### 3. ConfigReloader

新增 `internal/config/reload.go`（同文件）：

```go
type ConfigReloader struct {
    current   *Config
    mu        sync.RWMutex
    onReload  func(plan *ReloadPlan) error  // 回调：应用变更
}

// Apply 解析 YAML，差异对比，应用可重载变更
func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error
```

**流程：**
1. 解析 newYAML 为 Config 结构
2. 调用 `Diff(r.current, newConfig)`
3. 如果有不可重载变更 → 返回错误（包含字段列表），不应用任何变更
4. 调用 `onReload(plan)` 回调
5. 回调成功 → 更新 `r.current`

### 4. Scheduler 热切换

新增 `internal/collector/scheduler.go` 的 `Reload` 方法：

```go
func (s *Scheduler) Reload(inputs []InputConfig, processors []ProcessorConfig,
    aggregators []AggregatorConfig, outputs []OutputConfig) error
```

**流程：**
1. 停止所有当前 input goroutines（通过 cancel）
2. 等待进行中的 gather 完成（带超时）
3. 重新创建 inputs/processors/aggregators/outputs 实例
4. 启动新的 input goroutines
5. 保留 accumulator 中的未推送指标

### 5. 接入 gRPC handler

修改 `internal/app/agent.go` 中的 `ConfigUpdate` handler：

```go
case *proto.PlatformMessage_ConfigUpdate:
    update := msg.GetConfigUpdate()
    if err := a.configReloader.Apply(ctx, update.ConfigYaml); err != nil {
        // ack with error
        a.grpcClient.SendAck(update.Version, false, err.Error())
    } else {
        a.grpcClient.SendAck(update.Version, true, "")
    }
```

### 6. 优雅关闭

重构 `Agent.shutdown(ctx)` 方法，实现有序关闭：

```
1. 停止接受新的 gRPC 消息（设置 flag）
2. 取消 scheduler（排空 pipeline，等待进行中的 gather 完成）
3. 刷新 gRPC 缓存（drain ring buffer，发送所有缓存指标）
4. 停止插件运行时（发送 SIGTERM，等待 5s，SIGKILL）
5. 关闭 HTTP server（server.Shutdown 带超时）
6. 关闭沙箱执行器（清理 cgroup、网络）
```

新增 `grpcclient/client.go` 的 `FlushAndStop` 方法：

```go
func (c *Client) FlushAndStop(ctx context.Context) error {
    // 1. Drain cache
    metrics := c.cache.Drain()
    // 2. Send all cached metrics
    for _, batch := range chunk(metrics, 100) {
        c.sender.SendMetricBatch(batch)
    }
    // 3. Close connection
    return c.conn.Close()
}
```

新增配置字段：

```yaml
agent:
  shutdown_timeout_seconds: 30  # 默认 30s
```

## 测试要求

- **配置差异引擎：** 相同配置、仅可重载变更、不可重载变更、混合变更、无效新配置
- **ConfigReloader：** 成功应用、拒绝不可重载、YAML 解析失败、验证失败
- **Scheduler 热切换：** 停止旧 input、启动新 input、进行中指标保留
- **优雅关闭：** 关闭顺序验证、超时行为、部分子系统关闭失败

## 验证方式

```bash
# 单元测试
go test -race ./internal/config/ -run TestReload
go test -race ./internal/collector/ -run TestSchedulerReload
go test -race ./internal/app/ -run TestGracefulShutdown

# 集成测试：通过 gRPC 发送 ConfigUpdate 消息验证端到端热更新
go test -race -tags=integration ./internal/integration/ -run TestConfigHotReload
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/config/reload.go` | 新建 — Diff、ConfigReloader |
| `internal/config/reload_test.go` | 新建 — 差异引擎和 reloader 测试 |
| `internal/collector/scheduler.go` | 修改 — 添加 Reload() 方法 |
| `internal/collector/scheduler_test.go` | 修改 — 添加热切换测试 |
| `internal/grpcclient/client.go` | 修改 — 添加 FlushAndStop() |
| `internal/app/agent.go` | 修改 — ConfigUpdate handler、shutdown 重构 |
| `internal/app/agent_test.go` | 修改 — 关闭顺序测试 |
| `internal/config/config.go` | 修改 — 添加 shutdown_timeout_seconds |
| `configs/config.yaml` | 修改 — 添加 shutdown 配置示例 |
