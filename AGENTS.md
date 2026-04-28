# AGENTS.md

## 项目定位

NodeAgentX 是 OpsPilot 平台的主机侧执行 Agent，当前阶段目标：指标采集、受限命令执行、AI 任务分发、可选鉴权、Prometheus 导出、Rust 插件 runtime。

## 技术栈

- Go
- Cobra（CLI）
- Viper（配置管理）
- zerolog（结构化日志）
- net/http（服务与客户端）
- gopsutil（主机指标采集）
- Rust（高性能插件 runtime）

## 目录职责

- `cmd/agent`：程序入口，仅负责启动
- `internal/app`：生命周期编排与依赖装配
- `internal/config`：配置模型、默认值、校验
- `internal/collector`：指标采集接口与实现
- `internal/executor`：命令执行与白名单安全边界
- `internal/server`：HTTP API、鉴权中间件、Prometheus 导出
- `internal/task`：任务模型与分发器
- `internal/reporter`：stdout/http 上报策略
- `internal/pluginruntime`：Rust runtime 进程管理与 UDS RPC 客户端
- `rust-runtime`：Rust 插件 runtime 实现

## 编码规范

1. `main.go` 不写业务逻辑
2. 包职责单一，高内聚低耦合
3. API 响应保持统一 JSON（`/metrics` 除外）
4. 所有错误必须有日志并向上返回
5. 新能力优先通过接口扩展（Collector/Reporter/Plugin runtime）
6. 不引入非必要第三方依赖

## 测试命令

```bash
go test ./...
```

## 安全约束

1. 禁止执行 shell 字符串与 `sh -c`
2. 命令执行必须走白名单
3. 必须使用 `context` 超时
4. 必须限制 stdout/stderr 输出大小
5. 插件任务必须走本地 UDS RPC，不得直接在 handler 执行高危系统逻辑
6. 插件返回结果必须做大小上限校验

## 禁止事项

1. 不得在 `main` 写采集/执行逻辑
2. 不得绕过 `executor` 直接调用系统命令
3. 不得在 handler 中写跨模块业务编排
4. 不得引入与当前阶段无关的大型基础设施（如全量 gRPC/mTLS 重构）
