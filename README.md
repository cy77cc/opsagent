# OpsAgent

OpsAgent 是面向 OpsPilot 控制面的主机侧执行与指标采集 Agent，包含两大核心子系统：

- **Telegraf 风格指标采集管线** — Input → Processor → Aggregator → Output 插件架构
- **nsjail 沙箱执行引擎** — 命名空间隔离 + cgroup v2 资源限制 + 安全策略

## 核心能力

| 能力 | 说明 |
|------|------|
| 指标采集管线 | 20 个内置插件（10 Input + 3 Processor + 4 Aggregator + 3 Output），标签注入，聚合，多路输出 |
| 远程命令执行 | 白名单策略、超时控制、输出截断 |
| 沙箱执行 | nsjail PID/NET/MNT 命名空间隔离，cgroup v2 内存/CPU/PID 限制 |
| gRPC 双向流 | Agent 主动连接平台，支持注册、心跳、指标上报、命令/脚本下发 |
| 脚本执行 | 沙箱内运行 bash/python3 脚本，实时流式输出 |
| Rust 插件 runtime | UDS JSON-RPC，支持日志解析、文本处理、eBPF 采集等 |
| 安全策略 | 命令白名单/黑名单、脚本关键字拦截、shell 注入检测 |
| 离线缓冲 | 环形缓存断线期间指标，重连后自动回放 |
| Prometheus 导出 | 内置 `/metrics` 端点 |
| 配置热重载 | SIGHUP + gRPC ConfigUpdate 触发，原子回滚，支持 collector/reporter/auth/prometheus 热更新 |
| 审计日志 | JSON-lines 格式，lumberjack 轮转，覆盖 config/plugin/task/grpc/sandbox 事件 |
| 自定义插件网关 | plugin.yaml 清单发现，健康检查，自动重启（指数退避），fsnotify 文件监听 |
| 健康检查 | /healthz 子系统聚合（healthy/degraded/unhealthy），/readyz 就绪探针 |
| CLI 子命令 | run, version, validate, plugins |

## 架构

```
┌─────────────────────────────────────────────────────┐
│                    Platform (OpsPilot)                │
│         gRPC Server ← 双向流 → Agent Client          │
└───────────────────────┬───────────────────────────────┘
                        │
┌───────────────────────┴───────────────────────────────┐
│                   OpsAgent Agent                     │
│                                                       │
│  ┌─────────────┐  ┌─────────────┐  ┌──────────────┐  │
│  │  Collector   │  │  Sandbox    │  │  Executor    │  │
│  │  Pipeline    │  │  (nsjail)   │  │  (local)     │  │
│  │             │  │             │  │              │  │
│  │ Input ──►   │  │ 命令/脚本    │  │ 直接执行      │  │
│  │ Processor ──►│  │ 隔离执行    │  │              │  │
│  │ Aggregator ──►│  └─────────────┘  └──────────────┘  │
│  │ Output ──►  │                                       │
│  └─────────────┘  ┌─────────────┐  ┌──────────────┐  │
│                   │ gRPC Client │  │ Plugin Runtime│  │
│                   │ 心跳/重连/缓存│  │ (Rust UDS)   │  │
│                   └─────────────┘  └──────────────┘  │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ Audit Logger │  │Config Reloader│  │Plugin Gateway │  │
│  │ (JSON-lines) │  │(SIGHUP/gRPC) │  │(plugin.yaml) │  │
│  └─────────────┘  └──────────────┘  └──────────────┘  │
│  ┌─────────────────────────────────────────────────┐  │
│  │              HTTP Server (:18080)                │  │
│  │  /healthz /readyz /api/v1/exec /api/v1/tasks    │  │
│  │  /api/v1/metrics/latest /metrics                 │  │
│  └─────────────────────────────────────────────────┘  │
└───────────────────────────────────────────────────────┘
```

## 内置插件

### Input 插件（采集）

| 插件 | type | 说明 | 可选 config |
|------|------|------|------------|
| CPU | `cpu` | CPU 使用率 | `per_cpu`, `total_cpu` |
| 内存 | `memory` | 虚拟内存 + swap | — |
| 磁盘 | `disk` | 磁盘用量 | `mount_points` |
| 网络 | `net` | 网络 I/O 计数器 | — |
| 进程 | `process` | Top-N 进程 | `top_n` |
| 磁盘 I/O | `diskio` | 磁盘读写计数器 | `devices` |
| 网络连接 | `connections` | 网络连接状态统计 | `states` |
| 负载 | `load` | 系统负载均值 (1/5/15 min) | — |
| GPU | `gpu` | NVIDIA GPU 指标 (nvidia-smi) | `bin_path` |
| 温度 | `temp` | 温度传感器 | — |

### Processor 插件（处理）

| 插件 | type | 说明 | config |
|------|------|------|--------|
| 标签器 | `tagger` | 静态/条件标签注入 | `tags`, `rules` |
| 正则替换 | `regex` | 标签值正则变换 | `tags[].key`, `pattern`, `replacement` |
| 差值/速率 | `delta` | 累计计数器差值或速率计算 | `fields`, `output` (delta/rate), `max_stale_seconds` |

### Aggregator 插件（聚合）

| 插件 | type | 说明 | config |
|------|------|------|--------|
| 平均值 | `avg` | 指标平均值 | `fields`, `period` |
| 求和 | `sum` | 指标累加 | `fields`, `period` |
| 最小/最大 | `minmax` | 指标最小值与最大值 | `fields` |
| 百分位 | `percentile` | P50/P95/P99 百分位 | `fields`, `percentiles` |

### Output 插件（输出）

| 插件 | type | 说明 | config |
|------|------|------|--------|
| HTTP | `http` | JSON POST + 重试 | `url`, `timeout`, `retry_count` |
| Prometheus | `prometheus` | 文本格式暴露 | `path`, `addr` |
| Prometheus Remote Write | `prometheus_remote_write` | 远程写入 | `url`, `timeout` |

## 快速启动

```bash
# 安装依赖
make tidy

# 运行测试（含 race detector）
make test-race

# 编译
make build

# 运行
./bin/opsagent run --config ./configs/config.yaml

# Smoke test（build + test + vet + security + sandbox check + integration）
./scripts/smoke-test.sh
```

## 配置

完整配置示例见 `configs/config.yaml`。关键配置项：

```yaml
# Agent 基本信息
agent:
  id: "agent-001"
  name: "web-server-01"
  interval_seconds: 10        # 指标采集间隔
  audit_log:
    enabled: false
    path: "/var/log/opsagent/audit.jsonl"
    max_size_mb: 100
    max_backups: 5

# 指标采集管线
collector:
  inputs:
    - type: cpu
      config: { per_cpu: false }
    - type: memory
      config: {}
    - type: disk
      config: {}
    - type: net
      config: {}
  processors:
    - type: tagger
      config: { tags: { env: "production" } }
  outputs:
    - type: http
      config: { url: "https://metrics.example.com/push" }

# gRPC 连接平台
grpc:
  server_addr: "platform.example.com:443"
  enroll_token: "your-token"
  mtls:
    cert_file: "/etc/opsagent/certs/client.crt"
    key_file: "/etc/opsagent/certs/client.key"
    ca_file: "/etc/opsagent/certs/ca.crt"
  heartbeat_interval_seconds: 15

# 沙箱执行
sandbox:
  enabled: true
  nsjail_path: "/usr/bin/nsjail"
  policy:
    allowed_commands: [echo, ls, cat, grep, df, free]
    blocked_commands: [rm, mkfs, dd]
    allowed_interpreters: [bash, python3]
    script_max_bytes: 65536
    shell_injection_check: true

# 命令执行器
executor:
  timeout_seconds: 10
  allowed_commands: [uptime, df, free, hostname]

# Reporter
reporter:
  mode: "stdout"  # stdout | http

# API 鉴权
auth:
  enabled: false
  bearer_token: ""

# Prometheus 导出
prometheus:
  enabled: true
  path: "/metrics"

# Rust 插件
plugin:
  enabled: false
  runtime_path: "./rust-runtime/target/release/opsagent-rust-runtime"
  socket_path: "/tmp/opsagent/plugin.sock"

# 自定义插件网关
plugin_gateway:
  enabled: false
  plugins_dir: "/etc/opsagent/plugins"
  startup_timeout_seconds: 10
  health_check_interval_seconds: 30
  max_restarts: 3
  restart_backoff_seconds: 5
  file_watch_debounce_seconds: 2
```

## API

```bash
# 健康检查
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/readyz

# 执行命令
curl -X POST http://127.0.0.1:18080/api/v1/exec \
  -H 'Content-Type: application/json' \
  -d '{"command":"df","args":["-h"],"timeout_seconds":10}'

# 沙箱执行命令
curl -X POST http://127.0.0.1:18080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"t1","type":"sandbox_exec","payload":{"command":"echo","args":["hello"]}}'

# 沙箱执行脚本
curl -X POST http://127.0.0.1:18080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"t2","type":"sandbox_exec","payload":{"interpreter":"bash","script":"df -h && free -h"}}'

# Rust 插件任务
curl -X POST http://127.0.0.1:18080/api/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"t3","type":"plugin_text_process","payload":{"text":"hello","operation":"uppercase"}}'

# Prometheus 指标
curl http://127.0.0.1:18080/metrics

# 最新指标快照
curl http://127.0.0.1:18080/api/v1/metrics/latest
```

## Makefile 目标

| 目标 | 说明 |
|------|------|
| `make build` | 编译 Agent 到 `bin/opsagent` |
| `make build-all` | 交叉编译 amd64 + arm64 |
| `make package` | 打包两个架构的安装包 (`dist/*.tar.gz`) |
| `make package-amd64` | 仅打包 amd64 |
| `make package-arm64` | 仅打包 arm64 |
| `make clean` | 清理 bin/ dist/ coverage.out |
| `make test` | 运行所有测试 |
| `make test-race` | 运行测试（含 race detector） |
| `make test-cover` | 测试覆盖率报告 |
| `make lint` | golangci-lint |
| `make vet` | go vet 静态分析 |
| `make proto` | 从 proto 生成 Go 代码 |
| `make rust-build` | 编译 Rust runtime |
| `make sandbox-check` | 检查 nsjail/cgroup/namespace 前置条件 |
| `make integration` | 运行集成测试 |
| `make integration-sandbox` | 运行沙箱集成测试（需 root） |
| `make security` | gosec 安全扫描 |
| `make ci` | CI 流水线（tidy + vet + test-race + security） |
| `make bench` | 采集管线基准测试 (benchmem, count=3) |
| `make e2e` | 端到端集成测试 (e2e build tag, 120s timeout) |

## 打包与安装

```bash
# 打包（开发机）
make package
# dist/opsagent-<version>-linux-amd64.tar.gz
# dist/opsagent-<version>-linux-arm64.tar.gz

# 安装（目标机器）
tar xzf opsagent-<version>-linux-amd64.tar.gz
cd opsagent-<version>-linux-amd64
sudo ./install.sh

# 管理服务
sudo systemctl start opsagent
sudo systemctl enable opsagent
sudo journalctl -u opsagent -f

# 卸载
sudo ./uninstall.sh
```

## 安全边界

1. 命令白名单 + `exec.CommandContext`，禁止 `sh -c` 拼接
2. 沙箱执行：nsjail PID/NET/MNT 命名空间隔离
3. cgroup v2 资源限制：内存上限、CPU 配额、PID 数量
4. 脚本安全：关键字黑名单 + shell 注入检测
5. stdout/stderr 输出字节上限
6. Rust 插件走独立子进程 + 本地 socket，便于隔离与熔断
7. 可选 mTLS + Bearer Token 鉴权
8. 沙箱 seccomp 系统调用白名单，仅允许基本 I/O/进程管理 syscall
9. 审计日志记录所有安全相关事件（config/plugin/task/grpc/sandbox）
10. 插件网关健康检查失败自动重启（指数退避），超限标记为 error

> 完整安全加固文档见 [安全加固手册](docs/security-hardening.md)。

## CI/CD

项目使用 GitHub Actions 自动化：

**CI** (`.github/workflows/ci.yml`) — 每次 push/PR 自动运行：
- Go: `go mod tidy` + `go vet` + `golangci-lint` + `go build` + `go test -race`（含 80% 覆盖率门槛）
- Rust: `cargo build` + `cargo test` + `cargo clippy` + `cargo audit`
- Integration: 集成测试（依赖 Go + Rust 通过）

**Release** (`.github/workflows/release.yml`) — 推送 `v*` tag 自动发布：
- 交叉编译 amd64 + arm64
- 打包 tar.gz（含二进制、配置、systemd 服务、安装/卸载脚本）
- 创建 GitHub Release 并上传产物

```bash
# 触发自动发布
git tag v1.0.0
git push origin v1.0.0
```

**本地测试命令：**
```bash
make test-race      # 测试 + race detector
make test-cover     # 覆盖率报告
make bench          # 基准测试
make e2e            # 端到端测试
make integration    # 集成测试
make ci             # 完整 CI 流水线
```

## 平台集成

详见 [platform-integration-guide.md](docs/platform-integration-guide.md)，包含：

- gRPC proto 定义与消息类型
- 平台端 Go 服务端完整实现示例
- 消息交互流程（注册、心跳、指标、命令执行、脚本执行）
- 配置参考与故障排查

## 项目结构

```
OpsAgent/
├── cmd/agent/                    # 入口
├── internal/
│   ├── app/                      # Agent 生命周期编排、审计日志、CLI 子命令
│   ├── collector/                # 采集管线
│   │   ├── inputs/               #   cpu, memory, disk, diskio, net, process, connections, load, gpu, temp
│   │   ├── processors/           #   tagger, regex, delta
│   │   ├── aggregators/          #   avg, sum, minmax, percentile
│   │   └── outputs/              #   http, prometheus, promrw
│   ├── config/                   # 配置加载与验证
│   ├── executor/                 # 本地命令执行
│   ├── grpcclient/               # gRPC 客户端（连接/发送/接收/缓存）
│   │   └── proto/                #   生成的 protobuf 代码
│   ├── health/                   # 健康检查接口与状态定义
│   ├── integration/              # 集成测试
│   ├── logger/                   # zerolog 封装
│   ├── pluginruntime/            # Rust 插件 runtime 客户端
│   ├── reporter/                 # 上报策略 (stdout/http)
│   ├── sandbox/                  # nsjail 沙箱执行引擎
│   ├── server/                   # HTTP API + Prometheus 导出
│   └── task/                     # 任务模型与分发
├── proto/                        # gRPC proto 定义
├── rust-runtime/                 # Rust 插件 runtime
├── configs/config.yaml           # 默认配置
├── scripts/
│   ├── package.sh                # 交叉编译打包脚本 (amd64/arm64)
│   ├── ci-package.sh             # CI 打包脚本（被 package.sh 和 Actions 调用）
│   ├── uninstall.sh              # 卸载脚本
│   ├── smoke-test.sh             # Smoke test 脚本
│   └── dev.sh                    # 开发运行脚本
├── .github/workflows/
│   ├── ci.yml                    # CI: build + test + vet
│   └── release.yml               # Release: 交叉编译 + 打包 + 发布
├── docs/                         # 文档
│   ├── README.md                     # 文档索引
│   ├── platform-integration-guide.md # 平台集成指南
│   ├── plugin-contract.md            # 插件协议规范
│   ├── sdk-development-guide.md      # SDK 开发指南
│   ├── security-hardening.md         # 安全加固手册
│   ├── operations-guide.md           # 运维部署指南
│   ├── changelog.md                  # 变更记录
│   └── archive/                      # 归档文档（开发计划与设计规格）
└── Makefile
```

## 文档

| 文档 | 说明 |
|------|------|
| [平台集成指南](docs/platform-integration-guide.md) | 平台侧开发者指南：gRPC 服务实现、消息交互流程 |
| [插件协议规范](docs/plugin-contract.md) | UDS JSON-RPC 2.0 协议定义 |
| [SDK 开发指南](docs/sdk-development-guide.md) | Go/Rust 插件 SDK 使用指南 |
| [安全加固手册](docs/security-hardening.md) | 安全架构、加固措施、审计配置 |
| [运维部署指南](docs/operations-guide.md) | 部署、监控、故障排查 |
| [CHANGELOG](docs/changelog.md) | 版本变更历史 |
