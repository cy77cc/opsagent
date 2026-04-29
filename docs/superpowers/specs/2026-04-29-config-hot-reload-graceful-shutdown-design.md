# Spec: 配置热更新与优雅关闭

## Context

OpsAgent 的 proto 定义了 `ConfigUpdate` 消息（含 `config_yaml` bytes + `version` int64），gRPC receiver 已接收到该消息，但 handler（`agent.go:618`）仅做日志记录和 ack，没有实际重载配置。同时，当前关闭流程存在缺陷：不排空 pipeline、不刷新 gRPC 缓存、不协调进行中的任务。

本 spec 实现安全的部分配置热加载和有序的优雅关闭。

## 设计决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Diff 比较策略 | 分组手写比较 | 精确报告变更字段名，嵌套 map/slice 用 reflect.DeepEqual |
| Reload 原子性 | 全有或全无 | 任何子系统失败则回滚全部变更，保持一致性 |
| 热更新触发方式 | gRPC + SIGHUP | SIGHUP 是经典守护进程热加载模式 |
| 进行中任务处理 | 拒绝新任务 + 等待完成 | 先标记不接受新任务，再等待现有任务完成，超时强制取消 |
| Scheduler 接口 | 扩展接口加 Reload | 最干净，所有实现必须满足 |
| Flush 失败行为 | 持久化到磁盘 | 数据不丢失，启动时重放 |
| SIGHUP 配置来源 | 原始启动路径 | 简单可靠，重新读取 --config 指定的文件 |
| 架构模式 | ConfigReloader + Reloader 接口 | 集中式原子性保证 + 分布式责任分离 |

## 目标

1. 实现配置热更新，支持 collector/reporter/auth/prometheus 部分重载
2. 不可重载配置变更被拒绝并给出清晰错误
3. 关闭时按序排空所有子系统、刷新 gRPC 缓存
4. 关闭超时可配置
5. 支持 SIGHUP 信号触发本地配置文件重载
6. Flush 失败时持久化缓存到磁盘，启动时重放

## 设计

### 1. 配置字段分类

将配置字段分为两类：

**可热重载（运行时安全变更）：**
- `collector.inputs` — 输入插件配置
- `collector.processors` — 处理器配置
- `collector.aggregators` — 聚合器配置
- `collector.outputs` — 输出插件配置
- `reporter.*` — 上报策略
- `auth.*` — 认证令牌
- `prometheus.*` — Prometheus 导出配置

**不可热重载（需重启）：**
- `agent.*` — Agent 基础配置（含 shutdown_timeout_seconds）
- `server.listen_addr` — HTTP 监听地址
- `grpc.*` — gRPC 连接配置
- `sandbox.*` — 沙箱配置
- `plugin.*` — 插件运行时配置
- `executor.*` — 本地执行器配置

### 2. Diff 引擎

新增 `internal/config/diff.go`：

```go
// ChangeSet 记录哪些字段组发生了变更
type ChangeSet struct {
    CollectorChanged  bool
    ReporterChanged   bool
    AuthChanged       bool
    PrometheusChanged bool
}

// NonReloadableChange 记录不可热重载的变更
type NonReloadableChange struct {
    Field  string      // e.g. "server.listen_addr"
    OldVal interface{}
    NewVal interface{}
}

// Diff 对比新旧配置，返回变更集和不可重载字段列表
// 新配置必须通过 Validate()
func Diff(old, new *Config) (*ChangeSet, []NonReloadableChange, error)
```

**比较逻辑：**
1. 先对 `new` 调用 `Validate()`，无效则直接返回错误
2. 逐组比较可重载字段，设置 `ChangeSet` 标志：
   - `collector.*` — 使用 `reflect.DeepEqual` 比较 `CollectorConfig`（含嵌套 map/slice）
   - `reporter.*` — 逐字段比较
   - `auth.*` — 逐字段比较
   - `prometheus.*` — 逐字段比较
3. 逐组比较不可重载字段，收集变更到 `NonReloadableChange`：
   - `agent.*` — 逐字段比较
   - `server.*` — 逐字段比较
   - `grpc.*` — 逐字段比较（含嵌套 MTLSConfig）
   - `sandbox.*` — 使用 `reflect.DeepEqual`（含嵌套 PolicyConfig）
   - `plugin.*` — 逐字段比较
   - `executor.*` — 逐字段比较
4. 返回结果

### 3. Reloader 接口 + ConfigReloader

新增 `internal/config/reload.go`：

```go
// Reloader 是每个可热重载子系统必须实现的接口
type Reloader interface {
    // CanReload 返回该子系统在给定 ChangeSet 中是否有变更
    CanReload(cs *ChangeSet) bool
    // Apply 应用新配置，失败返回错误（触发回滚）
    Apply(newCfg *Config) error
    // Rollback 回滚到旧配置（Apply 成功后才调用）
    Rollback(oldCfg *Config) error
}

// ConfigReloader 管理配置热重载，保证原子性
type ConfigReloader struct {
    current   *Config
    mu        sync.RWMutex
    reloaders []Reloader
    logger    zerolog.Logger
}

func NewConfigReloader(current *Config, logger zerolog.Logger, reloaders ...Reloader) *ConfigReloader

// Current 返回当前配置（只读快照）
func (r *ConfigReloader) Current() *Config

// Apply 解析 YAML，Diff，原子性应用可重载变更
func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error
```

**Apply 流程：**
1. `r.mu.Lock()` 保证并发互斥
2. 解析 `newYAML` 为 `Config` 结构
3. 调用 `Diff(r.current, newConfig)`
4. 若有 `NonReloadableChange` → 返回错误（列出字段），不应用任何变更
5. 遍历 `r.reloaders`，对 `CanReload(cs)` 为 true 的调用 `Apply(newCfg)`
6. 收集已成功应用的 reloaders 到 `applied` 列表
7. 任何 `Apply` 失败 → 对 `applied` 中的 reloader 依次调用 `Rollback(r.current)`，返回错误
8. 全部成功 → `r.current = newConfig`，释放锁

### 4. 四个 Reloader 实现

| 实现 | 位置 | CanReload | Apply 行为 | Rollback 行为 |
|------|------|-----------|------------|---------------|
| `CollectorReloader` | `internal/collector/reload.go` | `cs.CollectorChanged` | 将 `config.CollectorConfig` 转换为 `collector.ReloadConfig`，调用 `Scheduler.Reload()` | 同理用旧配置调用 `Scheduler.Reload()` |
| `AuthReloader` | `internal/server/reload.go` | `cs.AuthChanged` | 调用 `server.UpdateAuth(cfg)` 更新 auth 配置 | 用旧配置调用 `server.UpdateAuth()` |
| `PrometheusReloader` | `internal/server/reload.go` | `cs.PrometheusChanged` | 调用 `server.UpdatePrometheus(cfg)` 更新 exporter 配置 | 用旧配置调用 `server.UpdatePrometheus()` |
| `ReporterReloader` | `internal/reporter/reload.go` | `cs.ReporterChanged` | 调用 `reporter.UpdateConfig(cfg)` 更新配置 | 用旧配置调用 `reporter.UpdateConfig()` |

**Server 需新增方法：**
- `server.Server.UpdateAuth(cfg AuthConfig)` — 通过 `sync/atomic` 或 mutex 保护 auth 配置字段
- `server.Server.UpdatePrometheus(cfg PrometheusConfig)` — 同理

**Reporter 需新增方法：**
- `reporter.UpdateConfig(cfg ReporterConfig)` — 通过 atomic/mutex 保护配置引用

每个实现内部保存旧配置快照用于 Rollback。

### 5. Scheduler 热切换

#### 接口扩展 — `internal/app/interfaces.go`

```go
type Scheduler interface {
    Start(ctx context.Context) <-chan []*collector.Metric
    Reload(ctx context.Context, cfg collector.ReloadConfig) error
    Stop()
}
```

#### Scheduler.Reload 实现 — `internal/collector/scheduler.go`

`collector` 包不导入 `config` 包（避免循环依赖），定义自己的配置类型：

```go
// ReloadConfig 是 collector 管道的配置快照，由 CollectorReloader 从 config.CollectorConfig 转换而来
type ReloadConfig struct {
    Inputs      []PluginConfig
    Processors  []PluginConfig
    Aggregators []PluginConfig
    Outputs     []PluginConfig
}

// PluginConfig 是单个插件的配置（与 config.PluginInstanceConfig 对应，避免循环依赖）
type PluginConfig struct {
    Type   string
    Config map[string]interface{}
}

func (s *Scheduler) Reload(ctx context.Context, cfg ReloadConfig) error
```

**流程：**
1. 取消所有当前 input goroutines（调用 `s.cancel()`）
2. `s.wg.Wait()` 等待所有 goroutine 退出（gather 完成）
3. Push 当前 aggregator 结果到 outputs（避免丢失聚合数据）
4. 重新从 cfg 创建 inputs/processors/aggregators/outputs 实例
   - 使用 `DefaultRegistry` 的工厂方法
   - 每个插件调用 `Init(cfg)` 初始化
5. 替换 `s.inputs/s.processors/s.aggregators/s.outputs`
6. 创建新的 `context.WithCancel`，启动新的 input goroutines

**重构注意：** `startOnce sync.Once` 需要替换为 `running bool` + `sync.Mutex`，支持多次 Start/Reload。

### 6. 接入 gRPC handler

修改 `internal/app/agent.go` 中的 `ConfigUpdate` handler：

```go
recv.SetConfigUpdateHandler(func(ctx context.Context, update *pb.ConfigUpdate) error {
    if err := a.configReloader.Apply(ctx, update.ConfigYaml); err != nil {
        a.log.Error().Err(err).Int64("version", update.GetVersion()).Msg("config reload failed")
        // ack with error info
        a.grpcClient.SendExecResult(&grpcclient.ExecResult{
            TaskID:   fmt.Sprintf("config-update-%d", update.GetVersion()),
            ExitCode: -1,
        })
        return nil
    }
    a.log.Info().Int64("version", update.GetVersion()).Msg("config reloaded")
    a.grpcClient.SendExecResult(&grpcclient.ExecResult{
        TaskID: fmt.Sprintf("config-update-%d", update.GetVersion()),
    })
    return nil
})
```

### 7. SIGHUP 支持

修改 `cmd/agent/main.go`，在 run 命令中监听 SIGHUP：

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGHUP)

go func() {
    for {
        select {
        case <-ctx.Done():
            return
        case sig := <-sigCh:
            if sig == syscall.SIGHUP {
                yaml, err := os.ReadFile(configPath)
                if err != nil {
                    log.Error().Err(err).Msg("failed to read config file for reload")
                    continue
                }
                if err := agent.ConfigReloader().Apply(ctx, yaml); err != nil {
                    log.Error().Err(err).Msg("SIGHUP config reload failed")
                } else {
                    log.Info().Msg("config reloaded via SIGHUP")
                }
            }
        }
    }
}()
```

Agent 需要暴露 `ConfigReloader()` 方法供 main 调用。

### 8. 优雅关闭

#### 关闭顺序

重构 `Agent.shutdown(ctx)` 方法：

```
1. 标记不接受新任务（atomic flag: shuttingDown = true）
2. 等待进行中任务完成（waitForActiveTasks，超时强制取消）
3. 停止 Scheduler（排空 pipeline，等待 gather 完成）
4. Flush gRPC 缓存（FlushAndStop）
5. 停止插件运行时（SIGTERM → 等待 → SIGKILL）
6. 关闭 HTTP server（server.Shutdown）
7. 清理沙箱执行器（cgroup、网络）
```

#### 拒绝新任务

Agent 新增 `shuttingDown atomic.Bool`，在各 handler 入口检查：

```go
if a.shuttingDown.Load() {
    return nil, fmt.Errorf("agent is shutting down")
}
```

应用于：`registerTaskHandlers` 中所有 handler、`registerGRPCHandlers` 中 CommandHandler/ScriptHandler。

#### 等待进行中任务

```go
func (a *Agent) waitForActiveTasks(ctx context.Context) {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            // 超时，强制取消所有
            a.activeTasks.Range(func(key, value any) bool {
                value.(context.CancelFunc)()
                return true
            })
            return
        case <-ticker.C:
            remaining := 0
            a.activeTasks.Range(func(_, _ any) bool { remaining++; return true })
            if remaining == 0 {
                return
            }
        }
    }
}
```

#### FlushAndStop — `internal/grpcclient/client.go`

```go
func (c *Client) FlushAndStop(ctx context.Context, persistPath string) error
```

`persistPath` 从配置读取，新增配置字段：

```yaml
grpc:
  cache_persist_path: "/var/lib/opsagent/metric_cache.json"  # 默认 ""
```

为空时 Flush 失败直接丢弃（不持久化）。Agent 传递 `cfg.GRPC.CachePersistPath` 给 `FlushAndStop`。

**流程：**
1. 取消连接循环（`c.cancel()`）
2. `c.cache.Drain()` 获取所有缓存指标
3. 如果有缓存指标：
   - 尝试通过 stream 分批发送（每批 100 条）
   - 发送成功 → 继续下一批
   - 发送失败或 stream 不可用 → 持久化剩余到 `persistPath`（JSON 格式）
4. 关闭连接（`c.conn.Close()`）
5. `c.wg.Wait()` 等待 goroutine 退出

#### 启动时重放持久化缓存

在 `Client.connect()` 中 `replayCache()` 之前，检查持久化文件：

```go
func (c *Client) loadPersistedCache(path string) {
    data, err := os.ReadFile(path)
    if err != nil {
        return // 文件不存在或读取失败，忽略
    }
    var metrics []*collector.Metric
    if err := json.Unmarshal(data, &metrics); err != nil {
        c.logger.Warn().Err(err).Msg("failed to parse persisted cache, discarding")
        os.Remove(path)
        return
    }
    for _, m := range metrics {
        c.cache.Add(m)
    }
    os.Remove(path)
    c.logger.Info().Int("count", len(metrics)).Msg("loaded persisted cache")
}
```

#### 可配置超时

`internal/config/config.go` AgentConfig 新增：

```go
type AgentConfig struct {
    // ... existing fields
    ShutdownTimeoutSeconds int `mapstructure:"shutdown_timeout_seconds"`
}
```

默认 30s，添加 `v.SetDefault("agent.shutdown_timeout_seconds", 30)`。

GRPCConfig 新增：

```go
type GRPCConfig struct {
    // ... existing fields
    CachePersistPath string `mapstructure:"cache_persist_path"`
}
```

默认 ""（不持久化），添加 `v.SetDefault("grpc.cache_persist_path", "")`。

#### 关闭入口

```go
func (a *Agent) Run(ctx context.Context) error {
    pipelineCh, errCh, err := a.startSubsystems(ctx)
    if err != nil {
        return err
    }
    a.eventLoop(ctx, pipelineCh, errCh)

    shutdownCtx, cancel := context.WithTimeout(
        context.Background(),
        time.Duration(a.cfg.Agent.ShutdownTimeoutSeconds)*time.Second,
    )
    defer cancel()
    a.shutdown(shutdownCtx)
    return nil
}
```

## 测试要求

| 测试目标 | 测试用例 |
|----------|----------|
| **Diff 引擎** | 相同配置 → 无变更；仅 collector 变更；仅 auth 变更；不可重载字段变更 → 拒绝；混合变更 + 不可重载 → 拒绝；无效新配置 → 验证失败；嵌套 map/slice 深比较 |
| **ConfigReloader** | 成功应用全重载；拒绝不可重载变更并回滚；YAML 解析失败；验证失败；子系统 Apply 失败 → 自动 Rollback；并发 Apply 互斥 |
| **Scheduler.Reload** | 停止旧 input goroutine；启动新 input；aggregator 结果在 reload 前 push；channel 不中断；空配置 → scheduler 停止 |
| **CollectorReloader** | CanReload 正确判断；Apply 调用 scheduler.Reload；Rollback 恢复旧配置 |
| **AuthReloader** | 更新 server auth 配置；Rollback 恢复 |
| **PrometheusReloader** | 更新 prometheus 配置；Rollback 恢复 |
| **ReporterReloader** | 更新 reporter 配置；Rollback 恢复 |
| **FlushAndStop** | 正常 flush 全部发送；stream 不可用 → 持久化；部分发送失败 → 持久化剩余；启动时加载持久化缓存 |
| **优雅关闭** | 关闭顺序正确（通过 mock 验证调用序）；shuttingDown flag 拒绝新任务；activeTasks 超时强制取消；shutdown timeout 生效 |
| **SIGHUP** | 收到信号 → 重新读取配置文件 → 触发 Apply |

## 验证方式

```bash
# 全量单元测试
go test -race ./internal/config/ ./internal/collector/ ./internal/grpcclient/ ./internal/server/ ./internal/reporter/ ./internal/app/

# 集成测试
go test -race -tags=integration ./internal/integration/ -run TestConfigHotReload
go test -race -tags=integration ./internal/integration/ -run TestGracefulShutdown
```

## 关键文件清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/config/diff.go` | 新建 | ChangeSet、NonReloadableChange、Diff 函数 |
| `internal/config/reload.go` | 新建 | Reloader 接口、ConfigReloader |
| `internal/config/diff_test.go` | 新建 | Diff 引擎测试 |
| `internal/config/reload_test.go` | 新建 | ConfigReloader 测试 |
| `internal/config/config.go` | 修改 | 添加 ShutdownTimeoutSeconds、CachePersistPath |
| `internal/collector/reload.go` | 新建 | CollectorReloader |
| `internal/collector/scheduler.go` | 修改 | 添加 Reload()，重构 startOnce 为 running+mutex |
| `internal/collector/scheduler_test.go` | 修改 | Reload 测试 |
| `internal/collector/reload_test.go` | 新建 | CollectorReloader 测试 |
| `internal/grpcclient/client.go` | 修改 | 添加 FlushAndStop()、loadPersistedCache() |
| `internal/grpcclient/client_test.go` | 修改 | Flush 测试 |
| `internal/server/reload.go` | 新建 | AuthReloader、PrometheusReloader |
| `internal/server/reload_test.go` | 新建 | Auth/Prometheus reloader 测试 |
| `internal/server/server.go` | 修改 | 添加 UpdateAuth()、UpdatePrometheus() 方法 |
| `internal/reporter/reload.go` | 新建 | ReporterReloader |
| `internal/reporter/reload_test.go` | 新建 | ReporterReloader 测试 |
| `internal/reporter/reporter.go` | 修改 | 添加 UpdateConfig() 方法 |
| `internal/app/agent.go` | 修改 | configReloader 字段、shutdown 重构、shuttingDown flag、ConfigReloader() 方法 |
| `internal/app/agent_test.go` | 修改 | 关闭顺序、热重载集成测试 |
| `internal/app/interfaces.go` | 修改 | Scheduler 接口加 Reload |
| `cmd/agent/main.go` | 修改 | SIGHUP 监听 |
| `configs/config.yaml` | 修改 | 添加 shutdown_timeout_seconds、cache_persist_path 示例 |
