# OpsAgent 运维部署指南

本指南面向使用和维护 OpsAgent 的运维工程师，涵盖系统要求、安装部署、配置管理、日常运维及故障排查等完整流程。OpsAgent 是一款基于 Go 开发的主机侧指标采集与沙箱执行代理，支持 Rust 插件运行时扩展。

---

## 1. 系统要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux (amd64 / arm64) |
| Go | 1.21+（仅编译需要） |
| Rust | 1.75+（仅编译 Rust 运行时需要） |
| nsjail | 可选，用于沙箱执行 |
| cgroup v2 | 可选，用于沙箱资源限制 |

> **前置检查**：运行 `make sandbox-check` 验证沙箱环境是否就绪。

---

## 2. 安装

### 2.1 从 Release 安装

```bash
# 从 GitHub Releases 下载对应架构的压缩包
tar xzf opsagent-<version>-linux-amd64.tar.gz
cd opsagent-<version>-linux-amd64
sudo ./install.sh
```

`install.sh` 执行以下操作：

| 安装项 | 路径 |
|--------|------|
| 主程序二进制 | `/usr/local/bin/opsagent` |
| 配置文件 | `/etc/opsagent/config.yaml`（已存在则保留，新版本存为 `.new`） |
| systemd 服务文件 | `/etc/systemd/system/opsagent.service` |
| 日志目录 | `/var/log/opsagent/` |

### 2.2 从源码编译

```bash
git clone <repo-url> opsagent
cd opsagent

make build          # 编译当前架构
make build-all      # 交叉编译 amd64 + arm64
make rust-build     # 编译 Rust 运行时（可选）
```

编译产物位于 `bin/` 目录下。

### 2.3 systemd 服务管理

```bash
# 启动服务
sudo systemctl start opsagent

# 停止服务
sudo systemctl stop opsagent

# 重启服务
sudo systemctl restart opsagent

# 查看服务状态
sudo systemctl status opsagent

# 开机自启
sudo systemctl enable opsagent

# 取消开机自启
sudo systemctl disable opsagent

# 实时查看日志
sudo journalctl -u opsagent -f
```

**服务安全特性**：

- `Restart=always`：进程异常退出后自动重启
- `After=network-online.target`：等待网络就绪后启动
- `ProtectSystem=strict`：以只读方式挂载系统目录
- `ProtectHome=true`：禁止访问用户主目录
- `PrivateTmp=true`：使用独立的 /tmp 命名空间
- `NoNewPrivileges=true`：禁止进程提升权限

---

## 3. 配置管理

### 3.1 配置文件

配置文件路径：`/etc/opsagent/config.yaml`

以下是完整配置字段参考：

```yaml
agent:
  id: "agent-001"                    # 必填，代理唯一标识符
  name: "web-server-01"              # 必填，人类可读的名称
  interval_seconds: 10               # 指标采集间隔（秒）
  shutdown_timeout_seconds: 30       # 优雅关闭超时时间（秒）
  audit_log:
    enabled: false                   # 是否启用审计日志
    path: "/var/log/opsagent/audit.jsonl"
    max_size_mb: 100                 # 单个审计日志文件最大体积（MB）
    max_backups: 5                   # 保留的旧日志文件数量

server:
  listen_addr: "127.0.0.1:18080"     # HTTP API 监听地址

executor:
  timeout_seconds: 10                # 命令执行超时时间（秒）
  max_output_bytes: 65536            # 命令输出最大字节数
  allowed_commands: [uptime, df, free, hostname]  # 允许执行的命令白名单

reporter:
  mode: "stdout"                     # 上报模式：stdout | http
  endpoint: ""                       # HTTP 上报地址（mode=http 时必填）
  timeout_seconds: 5                 # 上报超时时间（秒）
  retry_count: 3                     # 上报重试次数
  retry_interval_ms: 500             # 上报重试间隔（毫秒）

auth:
  enabled: true                      # 是否启用认证
  bearer_token: ""                   # Bearer Token（生产环境须 32 字符以上）

prometheus:
  enabled: true                      # 是否暴露 Prometheus 指标
  path: "/metrics"                   # 指标端点路径
  protect_with_auth: false           # 指标端点是否需要认证

grpc:
  server_addr: "platform.example.com:443"  # 平台 gRPC 服务地址
  enroll_token: ""                         # 注册 Token
  mtls:
    cert_file: ""                    # 客户端证书路径
    key_file: ""                     # 客户端私钥路径
    ca_file: ""                      # CA 证书路径
  heartbeat_interval_seconds: 15     # 心跳间隔（秒）
  reconnect_initial_backoff_ms: 1000 # 重连初始退避时间（毫秒）
  reconnect_max_backoff_ms: 30000    # 重连最大退避时间（毫秒）

collector:
  inputs:                            # 采集器输入插件列表
    - type: cpu
      config: { per_cpu: false }
    - type: memory
      config: {}
    - type: disk
      config: {}
    - type: net
      config: {}
    - type: load
      config: {}
    - type: diskio
      config: {}
    - type: temp
      config: {}
    - type: gpu
      config: {}
    - type: connections
      config: {}
  processors: []                     # 数据处理器列表
  aggregators: []                    # 数据聚合器列表
  outputs: []                        # 数据输出列表

sandbox:
  enabled: false                     # 是否启用沙箱执行
  nsjail_path: "/usr/bin/nsjail"     # nsjail 二进制路径
  base_workdir: "/tmp/opsagent/sandbox"  # 沙箱工作目录
  default_timeout_seconds: 30        # 默认执行超时时间（秒）
  max_concurrent_tasks: 4            # 最大并发沙箱任务数
  cgroup_base_path: "/sys/fs/cgroup/opsagent"  # cgroup 基础路径
  audit_log_path: "/var/log/opsagent/audit.log"
  policy:
    allowed_commands: [echo, ls, cat, grep, wc]  # 沙箱内允许的命令
    blocked_commands: [rm, mkfs, dd]              # 沙箱内禁止的命令
    allowed_interpreters: [bash, python3]         # 允许的脚本解释器
    script_max_bytes: 65536                       # 脚本最大字节数
    shell_injection_check: true                   # 是否启用 Shell 注入检测

plugin:
  enabled: false                     # 是否启用 Rust 插件运行时
  runtime_path: "./rust-runtime/target/release/opsagent-rust-runtime"
  socket_path: "/tmp/opsagent/plugin.sock"  # 插件通信 Unix Socket 路径

plugin_gateway:
  enabled: false                     # 是否启用插件网关
  plugins_dir: "/etc/opsagent/plugins"     # 插件目录
  startup_timeout_seconds: 10        # 插件启动超时时间（秒）
  health_check_interval_seconds: 30  # 插件健康检查间隔（秒）
  max_restarts: 3                    # 插件最大重启次数
  restart_backoff_seconds: 5         # 重启退避时间（秒）
```

### 3.2 热重载

支持通过 SIGHUP 信号或 gRPC ConfigUpdate 指令触发配置热重载。

```bash
# 通过信号触发热重载
kill -HUP $(pidof opsagent)
```

**热重载范围**：

| 可热重载（无需重启） | 需要重启 |
|----------------------|----------|
| collector（inputs / processors / aggregators / outputs） | agent.id |
| reporter | agent.name |
| auth | server.listen_addr |
| prometheus | grpc 相关配置 |
| - | sandbox 相关配置 |
| - | plugin 相关配置 |

- 不可热重载的字段发生变更时，重载请求将被拒绝。
- 重载失败时自动回滚至上一有效配置（原子操作）。

---

## 4. 健康检查与监控

### 4.1 HTTP 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/healthz` | GET | 综合健康检查，返回各子系统状态：`healthy` / `degraded` / `unhealthy` |
| `/readyz` | GET | 就绪探针，可用于 Kubernetes readinessProbe |

```bash
# 检查健康状态
curl http://127.0.0.1:18080/healthz

# 检查就绪状态
curl http://127.0.0.1:18080/readyz
```

### 4.2 Prometheus 指标

启用 Prometheus 后，通过 `/metrics` 端点暴露标准格式的指标数据。

```bash
curl http://127.0.0.1:18080/metrics
```

可将该地址配置到 Prometheus 的 `scrape_configs` 中作为采集目标。

### 4.3 审计日志

审计日志以 JSON Lines（JSONL）格式写入，每行一条事件记录。

支持的事件类型：

| 事件类型 | 说明 |
|----------|------|
| `config` | 配置变更事件 |
| `plugin` | 插件生命周期事件 |
| `task` | 沙箱任务执行事件 |
| `grpc` | gRPC 通信事件 |
| `sandbox` | 沙箱隔离相关事件 |

日志轮转由 lumberjack 管理，通过 `audit_log.max_size_mb` 和 `audit_log.max_backups` 控制。

---

## 5. 日常运维

### 5.1 日志管理

OpsAgent 使用 zerolog 进行结构化日志输出，通过 lumberjack 实现日志轮转。

```bash
# 实时查看服务日志
sudo journalctl -u opsagent -f

# 查看最近 100 行日志
sudo journalctl -u opsagent -n 100

# 按时间段过滤日志
sudo journalctl -u opsagent --since "2024-01-01" --until "2024-01-02"
```

日志轮转参数：

- `max_size_mb`：单个日志文件最大体积（MB），达到后触发轮转
- `max_backups`：保留的旧日志文件数量

### 5.2 性能调优

| 参数 | 说明 | 调优建议 |
|------|------|----------|
| `agent.interval_seconds` | 指标采集频率 | 根据业务需求调整，过低会增加系统开销 |
| `sandbox.max_concurrent_tasks` | 并发沙箱任务数 | 根据 CPU 核心数和内存大小调整 |
| `collector.inputs` | 采集输入列表 | 仅启用实际需要的输入，减少不必要的系统调用 |

### 5.3 升级流程

```bash
# 1. 备份当前配置
cp /etc/opsagent/config.yaml /etc/opsagent/config.yaml.bak

# 2. 停止服务
sudo systemctl stop opsagent

# 3. 替换二进制文件
sudo cp opsagent /usr/local/bin/opsagent

# 4. 启动服务
sudo systemctl start opsagent

# 5. 验证服务状态
curl http://127.0.0.1:18080/healthz
```

> **注意**：如果新版本包含配置格式变更，请参考 Release Notes 处理配置迁移。配置文件中的不可热重载字段变更需要重启服务才能生效。

---

## 6. 故障排查

### 6.1 常见问题

| 问题 | 可能原因 | 排查方法 |
|------|----------|----------|
| gRPC 连接失败 | 网络不通、证书配置错误、服务端地址有误 | 检查网络连通性、mTLS 证书、`grpc.server_addr` 配置 |
| 沙箱启动失败 | nsjail 未安装、cgroup v2 未启用、权限不足 | 运行 `make sandbox-check`，检查 nsjail 路径和权限 |
| 插件无响应 | plugin.yaml 配置错误、二进制路径不正确、Socket 权限不足 | 检查插件配置文件、二进制路径、Socket 文件权限 |
| 指标数据为空 | 未配置 collector.inputs、采集间隔过大 | 检查 `collector.inputs` 配置和 `interval_seconds` 值 |

### 6.2 诊断命令

```bash
# 冒烟测试
./scripts/smoke-test.sh

# 检查沙箱环境前置条件
make sandbox-check

# 检查服务健康状态
curl http://127.0.0.1:18080/healthz

# 检查服务就绪状态
curl http://127.0.0.1:18080/readyz

# 查看 Prometheus 指标
curl http://127.0.0.1:18080/metrics

# 验证配置文件语法
./bin/opsagent validate --config /etc/opsagent/config.yaml

# 列出已加载的插件
./bin/opsagent plugins --config /etc/opsagent/config.yaml

# 安全扫描
make security

# 本地完整 CI 流程
make ci
```

---

## 7. 卸载

```bash
sudo ./uninstall.sh
```

卸载脚本将执行以下操作：

1. 停止并禁用 opsagent 服务
2. 移除 `/usr/local/bin/opsagent` 二进制文件
3. 移除 `/etc/systemd/system/opsagent.service` 服务文件
4. 交互式询问是否同时删除配置文件（`/etc/opsagent/`）和日志目录（`/var/log/opsagent/`）

---

## 8. CI/CD 集成

### 8.1 GitHub Actions CI 流水线

CI 流水线包含以下阶段：

**Go 阶段**：
- `go mod tidy` — 检查依赖一致性
- `go vet` — 静态分析
- `golangci-lint` — 代码风格与质量检查
- `go build` — 编译验证
- `go test -race` — 运行测试（覆盖率门槛 80%）

**Rust 阶段**：
- `cargo build` — 编译 Rust 运行时
- `cargo test` — 运行 Rust 测试
- `cargo clippy` — 代码质量检查
- `cargo audit` — 依赖安全审计

**集成测试**：
- 端到端功能验证

### 8.2 发布流程

发布通过 Git Tag 触发，推送 `v*` 格式的 Tag 后自动执行交叉编译、打包并发布到 GitHub Releases。

```bash
# 创建版本标签
git tag v1.0.0

# 推送标签触发发布流水线
git push origin v1.0.0
```

发布流水线自动完成：
1. 交叉编译 amd64 和 arm64 架构
2. 生成压缩包（包含二进制、install.sh、默认配置）
3. 创建 GitHub Release 并上传产物
