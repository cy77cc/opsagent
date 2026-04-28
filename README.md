# NodeAgentX

NodeAgentX 是面向 OpsPilot 控制面的主机侧执行与指标采集 Agent。

## 项目介绍

当前核心能力：

1. 主机指标采集（gopsutil）
2. 受限远程命令执行（白名单、超时、输出截断）
3. AI 任务分发 API
4. Reporter 支持 `stdout` 与 `http`（含重试）
5. 可选 Bearer Token 鉴权
6. Prometheus 文本导出端点
7. Rust 插件 runtime（UDS JSON-RPC）

## 架构说明

`run` 启动链路：

1. 加载配置
2. 初始化日志
3. （可选）拉起 rust-runtime 子进程
4. 启动 HTTP Server
5. 周期采集指标
6. 上报采集结果（stdout/http）

核心模块：

- `collector`：指标采集接口与实现
- `executor`：命令执行安全边界
- `task`：任务模型与分发
- `pluginruntime`：Rust runtime 本地 RPC 客户端与生命周期管理
- `server`：API、鉴权中间件、Prometheus 导出
- `reporter`：上报策略（stdout/http）
- `app`：生命周期编排

## 快速启动

```bash
make tidy
make test
go run ./cmd/agent run --config ./configs/config.yaml
```

## Rust Runtime 构建

```bash
make rust-build
```

默认 runtime 路径：`./rust-runtime/target/release/nodeagentx-rust-runtime`

## 配置说明

关键配置：

- `executor.allowed_commands`：命令白名单
- `reporter.mode`：`stdout` 或 `http`
- `auth.enabled`：开启 API Bearer 鉴权
- `prometheus.path`：Prometheus 导出路径
- `plugin.enabled`：开启 Rust 插件任务
- `plugin.socket_path`：本地 UDS 套接字
- `plugin.max_result_bytes` / `plugin.chunk_size_bytes`：大结果分块与上限

## API 示例

健康检查：

```bash
curl -s http://127.0.0.1:18080/healthz
curl -s http://127.0.0.1:18080/readyz
```

执行命令：

```bash
curl -s -X POST http://127.0.0.1:18080/api/v1/exec \
  -H 'Content-Type: application/json' \
  -d '{"command":"df","args":["-h"],"timeout_seconds":10}'
```

Rust 插件任务（文本处理）：

```bash
curl -s -X POST http://127.0.0.1:18080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "task_id":"tp-1",
    "type":"plugin_text_process",
    "payload":{"text":"hello world","operation":"uppercase"}
  }'
```

Rust 插件任务（日志解析）：

```bash
curl -s -X POST http://127.0.0.1:18080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "task_id":"lp-1",
    "type":"plugin_log_parse",
    "payload":{"text":"INFO ok\nWARN high cpu\nERROR timeout"}
  }'
```

## 安全边界

1. 禁止 `sh -c` 与 shell 字符串拼接
2. 必须使用白名单 + `exec.CommandContext`
3. 必须设置执行超时
4. stdout/stderr 限制最大输出字节
5. Rust 任务走独立子进程与本地 socket，便于隔离与熔断
6. eBPF 任务在 runtime 内默认可降级返回（能力不可用时）

## Roadmap

1. Rust runtime seccomp/namespace 强化落地
2. 插件任务熔断与重启策略细化
3. 文件扫描与安全探测规则系统
4. eBPF 多内核版本兼容矩阵
