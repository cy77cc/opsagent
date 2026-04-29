# Spec 3: 新 Input 插件

## Context

OpsAgent 当前有 5 个 input 插件（cpu、memory、disk、net、process），覆盖了基础系统指标。但对于 AI 运维诊断场景，还缺少几个关键指标：
- **Load Average** — 系统负载是判断整体压力的第一指标
- **Disk IO** — IO 瓶颈是故障诊断的常见根因
- **Temperature** — 过热导致降频/关机是硬件故障的信号
- **GPU** — AI 训练/推理场景的 GPU 监控是刚需
- **Network Connections** — 连接数异常是网络问题的关键信号

所有新插件遵循已有的 `internal/collector/inputs/cpu/cpu.go` 模式。

**依赖：** Spec 1（测试模式已建立）

## 目标

1. 新增 5 个 input 插件，通过 YAML 配置即可使用
2. 所有新插件测试覆盖率 ≥80%
3. GPU 插件在无 NVIDIA GPU 时优雅跳过
4. 示例配置更新

## 设计

### 插件模式（已有，所有新插件遵循）

```go
func init() {
    collector.RegisterInput("plugin_name", func() collector.Input {
        return &PluginName{}
    })
}

type PluginName struct {
    // config fields
}

func (p *PluginName) Init(cfg map[string]interface{}) error { /* parse config */ }
func (p *PluginName) Gather(ctx context.Context, acc collector.Accumulator) error { /* collect */ }
func (p *PluginName) SampleConfig() string { /* return YAML example */ }
```

### 1. Load Average 插件

**文件：** `internal/collector/inputs/load/load.go`

**数据源：** `gopsutil/v4/load.AvgWithContext()`

**指标：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `load1` | Gauge | 1 分钟平均负载 |
| `load5` | Gauge | 5 分钟平均负载 |
| `load15` | Gauge | 15 分钟平均负载 |

**标签：** 无（主机级别指标）

**配置：**
```yaml
[[inputs.load]]
  # 无额外配置
```

**工作量：** S

### 2. Disk IO 插件

**文件：** `internal/collector/inputs/diskio/diskio.go`

**数据源：** `gopsutil/v4/disk.IOCountersWithContext()`

**指标：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `read_bytes` | Counter | 读取字节数（累计） |
| `write_bytes` | Counter | 写入字节数（累计） |
| `read_count` | Counter | 读取次数 |
| `write_count` | Counter | 写入次数 |
| `read_time_ms` | Counter | 读取耗时（毫秒） |
| `write_time_ms` | Counter | 写入耗时（毫秒） |

**标签：** `device`（设备名，如 sda、nvme0n1）

**配置：**
```yaml
[[inputs.diskio]]
  # 可选：指定设备过滤
  # devices = ["sda", "nvme0n1"]
```

**注意：** 这些是累计 Counter，实际速率需要配合 Spec 4 的 Delta Processor 使用。

**工作量：** M

### 3. Temperature 插件

**文件：** `internal/collector/inputs/temp/temp.go`

**数据源：** `gopsutil/v4/host.SensorsTemperaturesWithContext()`

**指标：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `temperature` | Gauge | 温度（摄氏度） |

**标签：** `sensor`（传感器名称）、`label`（标签，如 "Core 0"）

**配置：**
```yaml
[[inputs.temp]]
  # 可选：温度单位（默认摄氏）
  # unit = "celsius"
```

**可用性检查：** `SensorsTemperatures` 在某些系统上返回空或报错，插件需：
1. Init 时调用一次检查可用性
2. 不可用时 Gather 返回 nil（不报错），日志 warning

**工作量：** M

### 4. GPU/NVIDIA 插件

**文件：** `internal/collector/inputs/gpu/gpu.go`

**数据源：** `nvidia-smi --query-gpu=... --format=csv,noheader,nounits`

**实现策略：** 先用 nvidia-smi 命令行（简单、无需 CGO、所有 NVIDIA 系统都有），后续可优化为 go-nvml 绑定。

**命令：**
```bash
nvidia-smi --query-gpu=index,name,utilization.gpu,utilization.memory,memory.total,memory.used,temperature.gpu,power.draw,fan.speed --format=csv,noheader,nounits
```

**指标：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `utilization_gpu` | Gauge | GPU 利用率 (%) |
| `utilization_memory` | Gauge | 显存利用率 (%) |
| `memory_total` | Gauge | 显存总量 (MiB) |
| `memory_used` | Gauge | 已用显存 (MiB) |
| `temperature` | Gauge | GPU 温度 (°C) |
| `power_draw` | Gauge | 功耗 (W) |
| `fan_speed` | Gauge | 风扇转速 (%) |

**标签：** `gpu_index`（GPU 编号）、`gpu_name`（GPU 型号）

**配置：**
```yaml
[[inputs.gpu]]
  # nvidia-smi 路径（默认从 PATH 查找）
  # bin_path = "/usr/bin/nvidia-smi"
```

**可用性检查：**
1. Init 时检查 `nvidia-smi` 是否存在且可执行
2. 不存在时 Gather 返回 nil，日志 info "nvidia-smi not found, skipping GPU metrics"

**错误处理：**
- nvidia-smi 执行超时（默认 5s）
- 输出格式异常（某行字段数不匹配）
- 部分 GPU 查询失败（跳过该 GPU，不中断其他）

**工作量：** L

### 5. Network Connections 插件

**文件：** `internal/collector/inputs/connections/connections.go`

**数据源：** `gopsutil/v4/net.ConnectionsWithContext()`

**指标：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `count_by_state` | Gauge | 按状态分组的连接数 |

**标签：** `state`（ESTABLISHED、LISTEN、TIME_WAIT、CLOSE_WAIT 等）、`protocol`（tcp、udp）

**配置：**
```yaml
[[inputs.connections]]
  # 可选：指定状态过滤
  # states = ["ESTABLISHED", "LISTEN", "TIME_WAIT"]
```

**注意：** `net.Connections()` 在某些系统上需要 root 权限，需处理权限错误。

**工作量：** M

## 注册与集成

### agent.go 注册

在 `internal/app/agent.go` 的 blank import 区域添加：

```go
_ "github.com/cy77cc/opsagent/internal/collector/inputs/load"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/diskio"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/temp"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/gpu"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/connections"
```

### 示例配置

更新 `configs/config.yaml`，为每个新插件添加注释示例。

## 测试要求

每个插件的测试：
- **Init 测试：** 有效配置、无效配置、空配置、额外字段忽略
- **Gather 测试：** 正常采集、空结果、数据源不可用（优雅跳过）
- **SampleConfig 测试：** 返回非空字符串
- **GPU 特殊测试：** nvidia-smi 不存在时的行为、输出解析错误处理

使用 mock 或 build tag 隔离硬件依赖测试。

## 验证方式

```bash
# 单元测试
go test -race ./internal/collector/inputs/load/
go test -race ./internal/collector/inputs/diskio/
go test -race ./internal/collector/inputs/temp/
go test -race ./internal/collector/inputs/gpu/
go test -race ./internal/collector/inputs/connections/

# 集成验证：加载配置，运行一次采集
go run ./cmd/agent run --config configs/test-all-inputs.yaml --dry-run
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/collector/inputs/load/load.go` | 新建 |
| `internal/collector/inputs/load/load_test.go` | 新建 |
| `internal/collector/inputs/diskio/diskio.go` | 新建 |
| `internal/collector/inputs/diskio/diskio_test.go` | 新建 |
| `internal/collector/inputs/temp/temp.go` | 新建 |
| `internal/collector/inputs/temp/temp_test.go` | 新建 |
| `internal/collector/inputs/gpu/gpu.go` | 新建 |
| `internal/collector/inputs/gpu/gpu_test.go` | 新建 |
| `internal/collector/inputs/connections/connections.go` | 新建 |
| `internal/collector/inputs/connections/connections_test.go` | 新建 |
| `internal/app/agent.go` | 修改 — 添加 blank imports |
| `configs/config.yaml` | 修改 — 添加新插件示例 |
