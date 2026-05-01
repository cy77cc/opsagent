# OpsAgent 全量设计文档

> 日期: 2026-04-28
> 状态: Draft
> 作者: AI Assistant + User

## 1. 愿景与背景

OpsAgent 定位为 OpsPilot 控制面的主机侧 Agent，对标 categraf/telegraf 的数据采集能力，同时提供安全沙箱环境供平台侧 AI Agent 远程执行命令和脚本。

### 核心问题

AI Agent 在故障排查和运维方面能力强大，但直接通过 SSH 在主机上执行 shell 命令和脚本存在严重安全风险。OpsAgent 通过主机侧沙箱执行引擎，让平台 AI Agent 安全地委托执行任务。

### 两大子系统

1. **指标采集** — Telegraf 风格的插件化采集管线 (Input/Processor/Aggregator/Output)
2. **沙箱执行** — nsjail 隔离的命令/脚本安全执行环境

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────────┐
│                    OpsPilot 控制面 (平台侧)                          │
│                                                                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐  ┌──────────────┐   │
│  │ AI Agent │  │ 任务调度 │  │ 指标存储     │  │ 告警/可视化  │   │
│  │ (故障排查)│→│ (Dispatch)│  │ (Prometheus/ │  │ (Grafana/    │   │
│  └──────────┘  └─────┬────┘  │  VictoriaM)  │  │  Nightingale)│   │
│                      │       └──────▲───────┘  └──────────────┘   │
│                      │              │                              │
│               ┌──────▼──────────────┴───────┐                     │
│               │     gRPC Server (双向流)      │                     │
│               │     mTLS + Enrollment Token  │                     │
│               └──────────────┬──────────────┘                     │
└──────────────────────────────┼─────────────────────────────────────┘
                               │ gRPC 双向流 (Agent 主动连接)
                               │
┌──────────────────────────────┼─────────────────────────────────────┐
│  Host (主机侧)               │                                     │
│  ┌───────────────────────────▼──────────────────────────────────┐  │
│  │                    OpsAgent (Go)                            │  │
│  │                                                               │  │
│  │  ┌─────────────┐  ┌──────────────┐  ┌────────────────────┐  │  │
│  │  │ gRPC Client  │  │  Collector   │  │   Sandbox Exec     │  │  │
│  │  │ (连接管理)    │  │  Pipeline    │  │   Engine           │  │  │
│  │  │              │  │              │  │                    │  │  │
│  │  │ · 心跳上报   │  │ · Input 调度 │  │ · 任务接收        │  │  │
│  │  │ · 任务拉取   │  │ · Processor  │  │ · nsjail 封装     │  │  │
│  │  │ · 输出流式   │  │ · Aggregator │  │ · 输出流式回传    │  │  │
│  │  │ · 结果上报   │  │ · Output     │  │ · 资源限制        │  │  │
│  │  └──────┬──────┘  └──────┬───────┘  └────────┬───────────┘  │  │
│  │         │                │                    │              │  │
│  │  ┌──────▼────────────────▼────────────────────▼───────────┐  │  │
│  │  │                    核心基础设施                          │  │  │
│  │  │  · Config (Viper)  · Logger (zerolog)  · Health/Ready  │  │  │
│  │  │  · Executor (白名单) · Reporter (stdout/http)          │  │  │
│  │  └────────────────────────────────────────────────────────┘  │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐  │
│  │  Rust Runtime (可选) — UDS JSON-RPC 插件                      │  │
│  └──────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

### 设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 实现语言 | Go (单二进制) | 与现有代码一致，低内存占用 |
| 沙箱技术 | nsjail | Google 出品，namespace+cgroup+seccomp 综合隔离，功能完整 |
| 通信协议 | gRPC 双向流 | 支持流式输出、mTLS、多路复用 |
| 连接方向 | Agent 主动连接 | 无需开放主机入站端口，Shoreline/SSM 成熟模式 |
| 采集架构 | Telegraf 风格管线 | 最成熟的插件化采集模型，Input/Output 接口清晰 |

### 向后兼容

未配置 gRPC 平台时，Agent 仍可独立运行（本地模式），现有功能不受影响。

## 3. 指标采集子系统

### 3.1 管线架构

```
Input (goroutine, 定时 Gather)
  │
  ▼
Accumulator (channel-based, 线程安全)
  │
  ▼
Processor Chain (串行, 可链式)
  │
  ▼
Aggregator (时间窗口聚合, 可选)
  │
  ▼
Output (batch flush, 重试+缓冲)
```

### 3.2 核心接口

```go
// Input 插件 — 负责采集指标
type Input interface {
    Init(cfg map[string]interface{}) error
    Gather(acc Accumulator) error
    SampleConfig() string
}

// Accumulator — 指标收集器
type Accumulator interface {
    AddFields(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
    AddGauge(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
    AddCounter(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
}

// Processor 插件 — 指标变换/过滤
type Processor interface {
    Apply(in []Metric) []Metric
    SampleConfig() string
}

// Aggregator 插件 — 时间窗口聚合
type Aggregator interface {
    Add(in Metric)
    Push(acc Accumulator)
    Reset()
    SampleConfig() string
}

// Output 插件 — 指标上报
type Output interface {
    Write(metrics []Metric) error
    Close() error
    SampleConfig() string
}

// Metric — 统一指标模型
type Metric struct {
    Name      string
    Tags      map[string]string
    Fields    map[string]interface{}
    Timestamp time.Time
    Type      MetricType // Gauge, Counter, Histogram
}
```

### 3.3 调度与缓冲

```go
// 调度器 — 每个 Input 独立 goroutine
type Scheduler struct {
    inputs    []ScheduledInput
    interval  time.Duration
    jitter    time.Duration
}

type ScheduledInput struct {
    Input    Input
    Interval time.Duration // 每个 Input 可独立配置间隔
    Tags     map[string]string
}

// 缓冲区 — per-output 分片缓冲
type Buffer struct {
    metrics    []Metric
    maxSize    int        // metric_buffer_limit
    batchSize  int        // metric_batch_size
    dropPolicy DropPolicy // DropNewest | DropOldest
}
```

### 3.4 内置 Input 插件

| 插件 | 采集内容 | 默认间隔 |
|------|----------|----------|
| cpu | CPU 使用率、负载 | 10s |
| memory | 内存、swap 使用 | 10s |
| disk | 磁盘使用率、IO | 30s |
| net | 网络接口流量、连接数 | 10s |
| process | 进程列表、资源占用 | 30s |
| system | 主机名、OS、uptime | 60s |
| host | 内核版本、CPU 核数 | 60s |

### 3.5 内置 Output 插件

| 插件 | 用途 |
|------|------|
| stdout | 开发调试 |
| http | 推送到平台 (JSON/InfluxDB line protocol) |
| prometheus | Prometheus 文本导出端点 (已有) |
| prometheus_remote_write | Remote write 到 VictoriaMetrics/Thanos |

### 3.6 内置 Processor 插件

| 插件 | 用途 |
|------|------|
| regex | 正则匹配替换 tag/field |
| tagger | 条件添加/修改 tag |

### 3.7 内置 Aggregator 插件

| 插件 | 用途 |
|------|------|
| avg | 时间窗口内求平均 |
| sum | 时间窗口内求和 |
| min | 时间窗口内取最小 |
| max | 时间窗口内取最大 |

### 3.8 背压处理策略

- 每个 Output 独立缓冲区，`metric_buffer_limit` (默认 10000)
- 缓冲区满时丢弃最新指标 (`DropNewest` 策略，与 Telegraf 一致)
- 输出失败时指标重新入队，指数退避重试
- 连接断开时指标缓存到本地 ring buffer，重连后重放

### 3.9 配置格式 (TOML)

```toml
[agent]
  interval = "10s"
  flush_interval = "10s"
  metric_batch_size = 1000
  metric_buffer_limit = 10000

[[inputs.cpu]]
  percpu = true
  totalcpu = true

[[inputs.memory]]

[[inputs.disk]]
  mount_points = ["/", "/data"]
  interval = "30s"

[[processors.regex]]
  [[processors.regex.tags]]
    key = "host"
    pattern = "^ip-(\\d+)-(\\d+)-(\\d+)-(\\d+)$"
    replacement = "node-${1}${2}${3}${4}"

[[outputs.http]]
  url = "https://platform.example.com/api/v1/metrics"
  timeout = "5s"
  batch_size = 500
```

## 4. 沙箱执行子系统

### 4.1 执行流程

```
平台 AI Agent
    │
    │ gRPC ExecuteRequest
    ▼
┌───────────────────────────────────────────────────────┐
│  OpsAgent — Sandbox Executor Engine                  │
│                                                        │
│  1. 接收任务 (command/script)                           │
│  2. 安全策略校验 (白名单/黑名单/注入检测)               │
│  3. 准备沙箱环境                                       │
│     ├── 脚本 → 写入临时文件                             │
│     ├── 构建 nsjail 配置                                │
│     └── 设置 cgroup 资源限制                            │
│  4. 启动 nsjail 子进程                                  │
│  5. 流式捕获 stdout/stderr                              │
│  6. 实时回传输出到平台                                  │
│  7. 等待完成或超时                                      │
│  8. 收集退出码、资源使用统计                            │
│  9. 清理沙箱环境                                       │
│ 10. 上报最终结果                                       │
└───────────────────────────────────────────────────────┘
```

### 4.2 沙箱安全模型

```
┌────────────────────────────────────────────────────────┐
│  nsjail 沙箱                                            │
│                                                         │
│  文件系统：                                              │
│  ├── /usr        (bind mount, 只读)                     │
│  ├── /lib        (bind mount, 只读)                     │
│  ├── /bin        (bind mount, 只读)                     │
│  ├── /etc        (bind mount, 只读, 精简)               │
│  ├── /tmp        (tmpfs, 64MB, 可读写)                  │
│  ├── /work       (tmpfs, 脚本/输入/输出)                │
│  └── /proc       (只读, 隔离视图)                       │
│                                                         │
│  网络：                                                 │
│  ├── 默认：完全隔离 (net none)                          │
│  └── 可选：白名单模式 (仅允许特定 IP:Port)              │
│                                                         │
│  资源限制 (cgroup v2)：                                  │
│  ├── 内存：512MB (可配置)                               │
│  ├── CPU：50% 单核 (可配置)                             │
│  ├── PID：32 个进程 (可配置)                            │
│  └── 文件大小：64MB (可配置)                            │
│                                                         │
│  用户：                                                 │
│  ├── 容器内 root (UID 0) → 宿主 unprivileged (UID 65534)│
│  └── seccomp-bpf 白名单过滤 syscall                     │
│                                                         │
│  超时：                                                 │
│  ├── 命令：30s 默认，最大 300s                          │
│  └── 脚本：60s 默认，最大 600s                          │
│                                                         │
│  输出：                                                 │
│  ├── stdout: 最大 10MB                                  │
│  ├── stderr: 最大 5MB                                   │
│  └── 超限自动截断                                       │
└────────────────────────────────────────────────────────┘
```

### 4.3 任务模型

```go
// ExecTask — 沙箱执行任务
type ExecTask struct {
    ID          string
    Type        TaskType            // Command | Script
    Command     string              // 命令名 (如 "df", "systemctl")
    Args        []string
    Script      string              // 脚本内容 (bash/python)
    Interpreter string              // 脚本解释器 (bash/python3/perl)
    Env         map[string]string
    Timeout     time.Duration
    Sandbox     SandboxConfig
}

// SandboxConfig — 可覆盖的沙箱参数
type SandboxConfig struct {
    MemoryMB     int
    CPUQuotaPct  int
    MaxPIDs      int
    NetworkMode  string // "none" | "allowlist"
    AllowedIPs   []string
    MaxOutputKB  int
}

// ExecResult — 执行结果
type ExecResult struct {
    TaskID    string
    ExitCode  int
    Stdout    string
    Stderr    string
    Duration  time.Duration
    TimedOut  bool
    Truncated bool
    Killed    bool
    Stats     ExecStats
}

// ExecStats — 资源使用统计
type ExecStats struct {
    PeakMemoryBytes int64
    CPUTimeUser     time.Duration
    CPUTimeSystem   time.Duration
    ProcessCount    int
    BytesWritten    int64
    BytesRead       int64
}
```

### 4.4 安全策略引擎

```go
type SecurityPolicy struct {
    AllowedCommands     []string // 命令白名单 (为空则允许所有)
    BlockedCommands     []string // 命令黑名单 (优先级高于白名单)
    BlockedKeywords     []string // 脚本关键词黑名单
    AllowedInterpreters []string // 脚本解释器白名单
    MaxScriptBytes      int      // 最大脚本大小
    AllowSudo           bool     // 是否允许 sudo
    AllowNetwork        bool     // 是否允许网络访问
}
```

执行前校验流程：
1. 命令白名单/黑名单检查
2. 脚本关键词扫描 (`rm -rf /`, `dd if=`, `mkfs`, `> /dev/sd`)
3. 脚本大小检查
4. 解释器白名单检查
5. 参数注入检测 (shell metacharacters)
6. 沙箱配置合理性校验

### 4.5 nsjail 配置生成

```go
type NsjailConfig struct {
    Mode        string // ONCE
    TimeLimit   int    // 秒
    RlimitAs    int    // MB
    RlimitFsize int    // MB
    UidMap      UIDMap
    GidMap      GIDMap
    Mounts      []Mount
    CgroupMem   int    // bytes
    CgroupCPU   int    // ms per 1000ms
    CgroupPids  int
    Seccomp     string
}
```

生成 nsjail 命令行：
- 命令模式: `nsjail --config /tmp/sandbox-<taskid>.cfg -- <command> <args...>`
- 脚本模式: `nsjail --config /tmp/sandbox-<taskid>.cfg -- /bin/bash /work/script.sh`
- 内联脚本: `nsjail --config /tmp/sandbox-<taskid>.cfg -- /bin/bash -c "<script>"`

### 4.6 网络白名单模式

默认 `net none` 完全隔离。开启白名单时：
1. 创建 network namespace
2. 创建 veth pair 连接到 host bridge
3. 配置 iptables 仅允许目标 IP:Port
4. 沙箱结束后清理 veth 和 iptables 规则

### 4.7 cgroup 资源统计

从 `/sys/fs/cgroup/sandbox-<taskid>/` 读取：
- `memory.peak` — 峰值内存
- `cpu.stat` — CPU 时间 (user/system)
- `pids.current` — 当前进程数
- `io.stat` — IO 字节数

### 4.8 审计日志

每次执行记录结构化审计日志：
```json
{
  "task_id": "exec-001",
  "timestamp": "2026-04-28T10:30:00Z",
  "triggered_by": "ai-agent",
  "type": "command",
  "command": "df -h",
  "exit_code": 0,
  "duration_ms": 120,
  "sandbox_config": {"memory_mb": 512, "timeout_s": 30},
  "stats": {"peak_memory_bytes": 10485760, "cpu_time_user_ms": 50},
  "timed_out": false,
  "truncated": false
}
```

## 5. gRPC 通信层

### 5.1 服务定义

```protobuf
syntax = "proto3";
package opsagent;

service AgentService {
    rpc Connect(stream AgentMessage) returns (stream PlatformMessage);
}

message AgentMessage {
    oneof payload {
        Heartbeat          heartbeat = 1;
        MetricBatch        metrics = 2;
        ExecOutput         exec_output = 3;
        ExecResult         exec_result = 4;
        AgentRegistration  registration = 5;
        Ack                ack = 6;
    }
}

message PlatformMessage {
    oneof payload {
        ExecuteCommand     exec_command = 1;
        ExecuteScript      exec_script = 2;
        CancelJob          cancel_job = 3;
        ConfigUpdate       config_update = 4;
        Ack                ack = 5;
    }
}
```

### 5.2 消息类型

#### 心跳

```protobuf
message Heartbeat {
    string agent_id = 1;
    int64  timestamp_ms = 2;
    string status = 3;           // "ready" | "busy" | "degraded"
    AgentInfo agent_info = 4;
}

message AgentInfo {
    string hostname = 1;
    string os = 2;
    string arch = 3;
    string version = 4;
    int32  cpu_cores = 5;
    int64  memory_bytes = 6;
    int32  running_tasks = 7;
    int32  max_tasks = 8;
}
```

#### 注册

```protobuf
message AgentRegistration {
    string agent_id = 1;
    string token = 2;            // enrollment token
    AgentInfo agent_info = 3;
    repeated string capabilities = 4;  // ["metrics", "exec", "sandbox"]
}
```

#### 指标上报

```protobuf
message MetricBatch {
    repeated Metric metrics = 1;
}

message Metric {
    string name = 1;
    map<string, string> tags = 2;
    repeated Field fields = 3;
    int64 timestamp_ms = 4;
    MetricType type = 5;
}

message Field {
    string key = 1;
    oneof value {
        double double_value = 2;
        int64  int_value = 3;
        string string_value = 4;
        bool   bool_value = 5;
    }
}

enum MetricType {
    GAUGE = 0;
    COUNTER = 1;
    HISTOGRAM = 2;
}
```

#### 命令执行

```protobuf
message ExecuteCommand {
    string task_id = 1;
    string command = 2;
    repeated string args = 3;
    map<string, string> env = 4;
    int32  timeout_seconds = 5;
    SandboxConfig sandbox = 6;
}

message ExecuteScript {
    string task_id = 1;
    string interpreter = 2;    // "bash", "python3", "perl"
    string script = 3;
    repeated string args = 4;
    map<string, string> env = 5;
    int32  timeout_seconds = 6;
    SandboxConfig sandbox = 7;
}

message SandboxConfig {
    int32 memory_mb = 1;
    int32 cpu_quota_pct = 2;
    int32 max_pids = 3;
    string network_mode = 4;   // "none" | "allowlist"
    repeated string allowed_ips = 5;
    int32 max_output_kb = 6;
}
```

#### 执行输出 (流式)

```protobuf
message ExecOutput {
    string task_id = 1;
    string stream = 2;         // "stdout" | "stderr"
    bytes  data = 3;
    int64  timestamp_ms = 4;
}
```

#### 执行结果

```protobuf
message ExecResult {
    string task_id = 1;
    int32  exit_code = 2;
    int64  duration_ms = 3;
    bool   timed_out = 4;
    bool   truncated = 5;
    bool   killed = 6;
    ExecStats stats = 7;
}

message ExecStats {
    int64 peak_memory_bytes = 1;
    int64 cpu_time_user_ms = 2;
    int64 cpu_time_system_ms = 3;
    int32 process_count = 4;
    int64 bytes_written = 5;
    int64 bytes_read = 6;
}
```

#### 任务取消

```protobuf
message CancelJob {
    string task_id = 1;
    string reason = 2;
}
```

#### 配置更新

```protobuf
message ConfigUpdate {
    bytes config_yaml = 1;
    int64 version = 2;
}
```

#### 确认

```protobuf
message Ack {
    string ref_id = 1;
    bool   success = 2;
    string error = 3;
}
```

### 5.3 连接生命周期

```
Agent 启动
    │
    ├─ 1. 加载本地配置
    ├─ 2. 初始化日志、健康检查
    ├─ 3. 启动 gRPC Client
    │
    ▼
Agent ──Connect()──→ Platform
    │                 │
    │ Registration    │ Ack
    │ (agent_id,      │
    │  token,         │
    │  capabilities)  │
    │                 │
    ▼                 ▼
双向流建立
    │
    ├── 每 30s Agent → Heartbeat
    ├── 每 flush_interval Agent → MetricBatch
    ├── 平台 → ExecuteCommand/ExecuteScript
    ├── Agent → ExecOutput (流式)
    └── Agent → ExecResult (完成)
    │
    │ 连接断开？
    │
    ▼
指数退避重连 (1s → 2s → 4s → ... → 60s max)
    │
    ├── 重连期间：指标缓存到本地 ring buffer
    ├── 重连期间：新任务拒绝，返回错误
    └── 重连成功：重放缓存中的指标
```

### 5.4 安全认证

首次注册 (Agent → Platform)：
1. Agent 携带 `enrollment_token` (部署时配置)
2. 平台验证 token，颁发 client cert + agent_id
3. Agent 保存 cert 到本地 (加密存储)

后续连接 (双向 mTLS)：
1. Agent 使用 client cert 连接平台
2. 平台验证 client cert
3. Agent 验证平台 server cert (CA pinning)
4. 双向认证通过，建立 gRPC stream

### 5.5 输出流式传输策略

```go
type OutputStreamer struct {
    taskID        string
    stream        string // "stdout" | "stderr"
    buffer        []byte
    flushSize     int           // 4KB 触发刷新
    flushInterval time.Duration // 500ms 定时刷新
    mu            sync.Mutex
}
```

策略：
1. 收集到 `flushSize` (4KB) → 立即发送
2. 距离上次发送超过 `flushInterval` (500ms) → 立即发送
3. 进程结束 → flush 剩余缓冲

## 6. 配置扩展

在现有 `config.yaml` 基础上新增段落：

```yaml
# gRPC 平台连接 (新增)
grpc:
  enabled: false
  server_addr: "platform.example.com:443"
  enrollment_token: ""
  cert_path: ""
  key_path: ""
  ca_path: ""
  heartbeat_interval_seconds: 30
  reconnect_backoff_max_seconds: 60

# 沙箱配置 (新增)
sandbox:
  enabled: false
  nsjail_path: "/usr/bin/nsjail"
  default_memory_mb: 512
  default_cpu_quota_pct: 50
  default_max_pids: 32
  default_timeout_seconds: 30
  max_timeout_seconds: 600
  default_network_mode: "none"
  max_output_bytes: 10485760  # 10MB
  work_dir: "/tmp/opsagent-sandbox"
  bind_mounts:
    - {src: "/usr", dst: "/usr", readonly: true}
    - {src: "/lib", dst: "/lib", readonly: true}
    - {src: "/bin", dst: "/bin", readonly: true}
    - {src: "/etc", dst: "/etc", readonly: true}

# 安全策略 (新增)
sandbox.security:
  allowed_commands: []
  blocked_commands: ["reboot", "shutdown", "init", "halt"]
  blocked_keywords: ["rm -rf /", "dd if=", "mkfs", "> /dev/sd", ":(){ :|:& };:"]
  allowed_interpreters: ["bash", "python3", "perl", "sh"]
  max_script_bytes: 1048576  # 1MB
  allow_sudo: false
  allow_network: false

# 指标采集配置 (新增)
collector:
  enabled: true
  global_interval: "10s"
  flush_interval: "10s"
  metric_batch_size: 1000
  metric_buffer_limit: 10000

# 原有配置保持不变
agent:
  id: "agent-local-001"
  name: "local-dev-agent"
  interval_seconds: 10
# ...
```

## 7. 目录结构

```
OpsAgent/
├── cmd/agent/                  # 入口 (不变)
├── configs/config.yaml
├── internal/
│   ├── app/                    # 生命周期编排
│   ├── config/                 # 配置模型
│   ├── logger/                 # 日志
│   ├── collector/              # 指标采集管线
│   │   ├── collector.go        # Input/Output/Processor/Aggregator 接口
│   │   ├── accumulator.go      # Accumulator 实现
│   │   ├── scheduler.go        # 调度器
│   │   ├── buffer.go           # 缓冲区
│   │   ├── metric.go           # Metric 数据模型
│   │   ├── inputs/             # 内置 Input 插件
│   │   │   ├── cpu/
│   │   │   ├── memory/
│   │   │   ├── disk/
│   │   │   ├── net/
│   │   │   └── process/
│   │   ├── processors/         # Processor 插件
│   │   │   ├── regex/
│   │   │   └── tagger/
│   │   ├── aggregators/        # Aggregator 插件
│   │   │   ├── avg/
│   │   │   └── sum/
│   │   └── outputs/            # Output 插件
│   │       ├── http/
│   │       ├── prometheus/
│   │       └── prometheus_remote_write/
│   ├── sandbox/                # 沙箱执行引擎
│   │   ├── executor.go         # 执行引擎主逻辑
│   │   ├── nsjail.go           # nsjail 配置生成 & 启动
│   │   ├── policy.go           # 安全策略引擎
│   │   ├── output_streamer.go  # 输出流式捕获
│   │   ├── stats.go            # cgroup 资源统计
│   │   └── audit.go            # 审计日志
│   ├── grpcclient/             # gRPC 通信层
│   │   ├── client.go           # 连接管理、心跳、重连
│   │   ├── sender.go           # 消息发送 (metrics/output/result)
│   │   ├── receiver.go         # 消息接收 (commands/config)
│   │   ├── cache.go            # 指标本地缓存 & 重放
│   │   └── proto/              # protobuf 生成代码
│   ├── executor/               # 命令白名单 (已有，保留)
│   ├── reporter/               # stdout/http reporter (已有，保留)
│   ├── server/                 # HTTP API (已有，保留)
│   ├── task/                   # 任务模型 (已有，可复用)
│   └── pluginruntime/          # Rust 插件 (已有，保留)
├── rust-runtime/               # Rust 插件 runtime (已有)
├── proto/                      # .proto 文件
│   └── agent.proto
└── Makefile
```

## 8. 全量实施任务

按依赖关系排序：

```
T1:  protobuf 定义 & 代码生成
 │
 ├──→ T2:  gRPC Client (连接管理/心跳/重连/mTLS)
 │     │
 │     ├──→ T5:  指标采集管线 (Input/Processor/Agg/Output 接口+调度器+缓冲区)
 │     │     │
 │     │     ├──→ T6:  内置 Input 插件 (cpu/mem/disk/net/process/system/host)
 │     │     │
 │     │     ├──→ T7:  内置 Output 插件 (http/prometheus/prometheus_remote_write)
 │     │     │
 │     │     └──→ T8:  Processor & Aggregator 框架 (regex/tagger + avg/sum)
 │     │
 │     └──→ T9:  gRPC 指标上报 sender (MetricBatch 消息)
 │
 ├──→ T3:  沙箱执行引擎
 │     │
 │     ├──→ T10: nsjail 封装 (配置生成/进程启动/资源限制)
 │     │
 │     ├──→ T11: 安全策略引擎 (白名单/黑名单/关键词扫描/注入检测)
 │     │
 │     ├──→ T12: 输出流式捕获 (stdout/stderr 缓冲+批量发送)
 │     │
 │     ├──→ T13: 脚本执行 (上传/临时文件/解释器)
 │     │
 │     ├──→ T14: 网络白名单模式 (veth+iptables)
 │     │
 │     └──→ T15: cgroup 资源统计采集
 │
 └──→ T4:  gRPC 任务接收 & 结果回传

T16: 配置热更新 (ConfigUpdate 消息 → Viper reload)
T17: 指标本地缓存 & 重放 (连接断开时 ring buffer)
T18: 任务审计日志
T19: 自监控 Prometheus 指标
T20: 健康检查增强 (readyz/livez 包含子系统状态)
```

## 9. 全量验证标准

```bash
# 1. 编译通过
make build

# 2. 全量测试通过
make test

# 3. 指标采集端到端
#    Agent 采集 cpu/mem/disk/net/process → gRPC 上报 → 平台收到 MetricBatch
#    同时 Prometheus 端点 /metrics 可抓到

# 4. 沙箱执行端到端 — 命令
#    平台发 ExecuteCommand("df", ["-h"]) → Agent nsjail 执行 → 流式输出回传 → ExecResult

# 5. 沙箱执行端到端 — 脚本
#    平台发 ExecuteScript("bash", "echo hello && df -h") → 上传→执行→回传

# 6. 安全策略验证
#    命令不在白名单 → 拒绝
#    脚本含 "rm -rf /" → 拒绝
#    参数含 shell 注入 → 拒绝

# 7. 资源限制验证
#    脚本申请超过 memory_mb → OOM killed → 正确报告
#    脚本超过 timeout → timed_out=true

# 8. 网络隔离验证
#    沙箱内 curl 外网 → 失败 (net none)
#    开启 allowlist 后 curl 允许的 IP → 成功

# 9. 断线重连验证
#    杀掉平台 → Agent 指标缓存 → 重启平台 → Agent 重连 + 重放

# 10. 配置热更新验证
#     平台发 ConfigUpdate → Agent 采集间隔变更 → 验证生效

# 11. 安全基线
#     gosec ./...
#     无硬编码密钥
#     mTLS 双向认证
```

## 10. 调研参考

### 指标采集参考

- **Telegraf**: Input→Accumulator→Processor→Aggregator→Output 管线，编译时静态注册，50+ 输出插件
- **Categraf**: 目录式 per-plugin 配置，SampleList 收集器，聚焦 Nightingale 生态
- **OTel Collector**: Factory + Builder 模式，DAG 管线，vendor-neutral
- **Datadog Agent**: Check-based 收集，Go+Python 双运行时

### 沙箱执行参考

- **nsjail**: Google 出品，namespace+cgroup+seccomp 综合隔离，单命令输出捕获
- **Shoreline.io**: gRPC 双向流，Agent 主动连接，Op/Action 模型，最接近的架构参考
- **Rundeck Agent**: HTTPS 长轮询，Node Executor 模型
- **SSM Agent**: IAM 授权 + Worker 进程隔离

### 安全隔离技术

- **seccomp-bpf**: syscall 白名单过滤，防御层
- **Linux Namespaces**: PID/NET/MNT/USER/UTS/IPC 隔离
- **cgroup v2**: 内存/CPU/PID 资源硬限制
- **gVisor**: 用户态内核，安全性最高但兼容性差
- **WASM/WASI**: 字节码沙箱，无法执行 shell 脚本
