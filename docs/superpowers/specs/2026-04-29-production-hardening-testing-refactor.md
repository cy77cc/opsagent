# Spec 1: 生产加固 — 测试与重构

## Context

OpsAgent 核心架构已完成，但整体测试覆盖率仅 43.4%，关键模块存在严重覆盖缺口：
- `internal/app/` — 0%（Agent 生命周期完全未测试）
- `internal/grpcclient/` — 28%（重连、消息分发未测试）
- `internal/collector/` — 53.9%（pipeline 核心）
- `internal/collector/processors/` — 43-48%（tagger、regex）

此外，`app/agent.go:Run()` 方法是一个 ~70 行的混合职责单体，耦合了子系统启动、事件循环、关闭逻辑，无法独立测试。`dist/` 目录中还残留重命名前的 `nodeagentx` 产物。

本 spec 的目标是将测试覆盖率提升到 70%+，重构核心方法使其可测试，清理历史遗留。

## 目标

1. 整体测试覆盖率从 43.4% 提升到 ≥70%
2. `internal/app/` 覆盖率从 0% 提升到 ≥60%
3. `internal/grpcclient/` 覆盖率从 28% 提升到 ≥70%
4. Agent `Run()` 拆分为 3+ 个可独立测试的方法
5. 清理所有重命名遗留

## 设计

### 1. Agent 生命周期重构

**当前问题：** `internal/app/agent.go` 的 `Run()` 方法混合了：
- 子系统启动（plugin runtime、scheduler、gRPC client、HTTP server）
- 事件循环（select on ctx.Done()、pipelineCh）
- 关闭逻辑（server.Shutdown、pluginRuntime.Stop）

**重构方案：** 拆分为三个方法：

```go
// startSubsystems 按序启动所有子系统
func (a *Agent) startSubsystems(ctx context.Context) error {
    // 1. Start plugin runtime
    // 2. Start scheduler
    // 3. Start gRPC client
    // 4. Start HTTP server
}

// eventLoop 运行主事件循环
func (a *Agent) eventLoop(ctx context.Context) {
    // select on ctx.Done(), pipelineCh, etc.
}

// shutdown 有序关闭所有子系统
func (a *Agent) shutdown(ctx context.Context) {
    // Reverse order of startSubsystems
}
```

**依赖注入改造：** `Agent` 结构体已通过 `NewAgent(cfg)` 构造，但 `Run()` 内部直接构造子系统。改造为：
- `NewAgent()` 接受可选的接口参数（用于测试注入 mock）
- 或使用 functional options 模式：`WithGRPCClient(c)`, `WithServer(s)`

**推荐方案：** functional options，保持向后兼容，测试时注入 mock。

### 2. gRPC 客户端测试

**当前覆盖：** 28%，主要缺口在 `connectLoop` 退避逻辑、`messageLoop` 分发、`replayCache`。

**测试策略：**
- **Mock stream：** 实现 `AgentService_ConnectClient` 接口的 mock，可控的 `Send()`/`Recv()` 行为
- **connectLoop 测试：** 验证指数退避（1s→2s→4s...→max）、成功重连后重置、context 取消退出
- **messageLoop 测试：** 验证每种 `PlatformMessage` payload 类型的分发、nil handler 跳过、未知类型处理
- **replayCache 测试：** 验证 ring buffer drain 顺序、空缓存、满缓存

**关键文件：**
- `internal/grpcclient/client.go` — connectLoop, messageLoop, replayCache
- `internal/grpcclient/receiver.go` — handler 分发
- `internal/grpcclient/sender.go` — proto 转换

### 3. Scheduler 测试

**当前状态：** Scheduler 是 pipeline 的核心，驱动 Input→Processor→Aggregator→Output 链。

**测试点：**
- `Start()`/`Stop()` 生命周期 — 验证 goroutine 启动和优雅退出
- `gatherOnce()` — 验证 processor 链顺序应用、aggregator feeding
- 聚合器推送间隔 — 验证 60s 周期 push
- 输出写入失败 — 验证错误处理不中断 pipeline

### 4. 配置验证边界测试

**当前覆盖：** 74.6%，需要补充：
- 缺失必填字段的错误消息
- 边界值（端口 0/65535、空字符串、超长字符串）
- 嵌套验证（sandbox 策略仅在 sandbox 启用时验证）
- 交叉字段依赖（reporter.mode=http 时 reporter.endpoint 必填）

### 5. 清理工作

| 项目 | 说明 |
|------|------|
| `dist/` 清理 | 删除 `nodeagentx-*` 压缩包，检查 `scripts/package.sh` 命名模板 |
| 错误包装统一 | 审计所有 `fmt.Errorf` 调用，确保使用 `%w` 包装 |
| blank import 注册 | 验证 `agent.go` 中所有 input 插件的 blank import 正确 |

## 测试要求

- 所有新测试使用表驱动模式
- Mock 使用接口注入，不使用 monkey patching
- 集成测试使用 `//go:build integration` tag 隔离
- `go test -race ./...` 全部通过
- `make ci` 通过（tidy → vet → test-race → security）

## 验证方式

```bash
# 运行测试并生成覆盖率报告
go test -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | tail -1
# 期望: total: (statements) >= 70.0%

# 检查关键包覆盖率
go test -race -coverprofile=app.out ./internal/app/
go tool cover -func=app.out | tail -1
# 期望: >= 60.0%

go test -race -coverprofile=grpc.out ./internal/grpcclient/
go tool cover -func=grpc.out | tail -1
# 期望: >= 70.0%

# CI 完整检查
make ci
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/app/agent.go` | 重构 Run() 为 3 个方法，添加 functional options |
| `internal/app/agent_test.go` | 新建，生命周期测试 |
| `internal/grpcclient/client_test.go` | 扩展，connectLoop/messageLoop 测试 |
| `internal/grpcclient/receiver_test.go` | 新建或扩展，handler 分发测试 |
| `internal/grpcclient/sender_test.go` | 新建或扩展，proto 转换测试 |
| `internal/collector/scheduler_test.go` | 新建或扩展，生命周期和 pipeline 测试 |
| `internal/config/config_test.go` | 扩展，边界值测试 |
| `dist/` | 删除 nodeagentx 产物 |
