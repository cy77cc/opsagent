# Spec 3: 新 Input 插件

> 日期: 2026-04-29
> 状态: Draft
> 版本: v2（完善版）

## Context

OpsAgent 当前有 5 个 input 插件（cpu、memory、disk、net、process），覆盖了基础系统指标。但对于 AI 运维诊断场景，还缺少几个关键指标：
- **Load Average** — 系统负载是判断整体压力的第一指标
- **Disk IO** — IO 瓶颈是故障诊断的常见根因
- **Temperature** — 过热导致降频/关机是硬件故障的信号
- **GPU** — AI 训练/推理场景的 GPU 监控是刚需
- **Network Connections** — 连接数异常是网络问题的关键信号

所有新插件遵循已有的 `internal/collector/inputs/cpu/cpu.go` 模式。

**依赖：** Spec 1（测试模式已建立）

## 设计决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| GPU 实现 | nvidia-smi 命令行 | 简单可靠，无 CGO 依赖 |
| GPU 扩展性 | YAGNI，不预留 | 等有 AMD 需求时再加 |
| 数据源不可用 | 静默跳过 + Info 日志 | 环境差异是正常的 |
| 权限不足 | 运行时检测 + 静默跳过 | 不阻塞其他插件 |
| 测试隔离 | Build tag（硬件名） | 如 `//go:build nvidia` |
| 测试范围 | 仅单元测试 | 集成测试由统一框架覆盖 |
| Metric 命名 | 小写英文 | 与现有 `cpu/memory/disk` 一致 |
| Tag 风格 | 保持现有风格 | 小写无下划线 |
| Disk IO 依赖 | 独立发布 | 输出累计值，用户可选配 Delta Processor |
| 配置格式 | 保持 `type + config` | 与现有 config.yaml 一致 |

## 目标

1. 新增 5 个 input 插件，通过 YAML 配置即可使用
2. 所有新插件测试覆盖率 ≥80%
3. GPU 插件在无 NVIDIA GPU 时优雅跳过
4. 示例配置更新

## 设计

### 通用插件模式

```go
func init() {
    collector.RegisterInput("plugin_name", func() collector.Input {
        return &PluginName{}
    })
}

type PluginName struct {
    // config fields
    available bool
}

func (p *PluginName) Init(cfg map[string]interface{}) error {
    // 解析配置
    // 检测可用性
    if !p.available {
        log.Info().Str("plugin", "plugin_name").Msg("data source unavailable, skipping")
    }
    return nil
}

func (p *PluginName) Gather(ctx context.Context, acc collector.Accumulator) error {
    if !p.available {
        return nil  // 静默跳过
    }
    // 正常采集
    return nil
}

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
collector:
  inputs:
    - type: load
      config: {}
```

**降级策略：** `load.AvgWithContext()` 在所有 Linux 系统上可用，无需特殊降级处理。如果调用失败，返回 error。

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
collector:
  inputs:
    - type: diskio
      config:
        devices: ["sda", "nvme0n1"]  # 可选，空则采集所有
```

**配置项：**
```go
type DiskIOInput struct {
    devices []string  // 设备过滤，空则采集所有
}
```

**降级策略：** `IOCountersWithContext()` 在所有 Linux 系统上可用。如果指定设备不存在，返回空结果（不报错）。

**与 Delta Processor 的关系：**
- Disk IO 输出累计 Counter，实际速率计算需要 Spec 4 的 Delta Processor
- 两者独立发布，不互相阻塞
- 文档说明：配合 Delta Processor（Spec 4）使用可获得速率指标

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
collector:
  inputs:
    - type: temp
      config: {}
```

**配置项：**
```go
type TempInput struct {
    available bool
}
```

**降级策略：**
- Init 时调用 `SensorsTemperatures()` 检测可用性
- 返回空切片或 error → 标记 `available = false`，日志 Info
- Gather 时 `available == false` → 返回 nil

**工作量：** M

### 4. GPU/NVIDIA 插件

**文件：** `internal/collector/inputs/gpu/gpu.go`

**数据源：** `nvidia-smi --query-gpu=... --format=csv,noheader,nounits`

**实现策略：** nvidia-smi 命令行（简单、无需 CGO、所有 NVIDIA 系统都有）

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
collector:
  inputs:
    - type: gpu
      config:
        bin_path: "/usr/bin/nvidia-smi"  # 可选，默认从 PATH 查找
```

**配置项：**
```go
type GPUInput struct {
    binPath   string
    available bool
    timeout   time.Duration  // 默认 5s
}
```

**降级策略：**
1. Init 时检查 `nvidia-smi` 是否存在且可执行（`exec.LookPath` 或检查 `bin_path`）
2. 不存在 → 标记 `available = false`，日志 Info "nvidia-smi not found, skipping GPU metrics"
3. Gather 时 `available == false` → 返回 nil

**错误处理：**
- nvidia-smi 执行超时（默认 5s）→ 返回 error
- 输出格式异常（某行字段数不匹配）→ 跳过该行，日志 Warn
- 部分 GPU 查询失败 → 跳过该 GPU，不中断其他

**输出解析：**
```go
func parseGPUOutput(line string) (tags map[string]string, fields map[string]interface{}, err error) {
    parts := strings.Split(line, ", ")
    if len(parts) < 9 {
        return nil, nil, fmt.Errorf("unexpected field count: %d", len(parts))
    }
    // parts[0]=index, parts[1]=name, parts[2]=util_gpu, ...
    // 解析并返回 tags 和 fields
}
```

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
collector:
  inputs:
    - type: connections
      config:
        states: ["ESTABLISHED", "LISTEN", "TIME_WAIT"]  # 可选，空则采集所有
```

**配置项：**
```go
type ConnectionsInput struct {
    states []string  // 状态过滤，空则采集所有
}
```

**降级策略：**
- Gather 时调用 `net.Connections()`
- 如果返回 permission error → 日志 Info "connections: permission denied, skipping"，返回 nil
- 其他 error → 返回 error

**权限处理代码模式：**
```go
func (c *ConnectionsInput) Gather(ctx context.Context, acc collector.Accumulator) error {
    conns, err := net.ConnectionsWithContext(ctx, "all")
    if err != nil {
        if os.IsPermission(err) {
            log.Info().Str("plugin", "connections").Msg("permission denied, skipping")
            return nil
        }
        return fmt.Errorf("connections: failed to get connections: %w", err)
    }
    // ... 聚合统计
}
```

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

更新 `configs/config.yaml`，为每个新插件添加注释示例：

```yaml
collector:
  inputs:
    # ... 现有插件 ...
    - type: load
      config: {}
    - type: diskio
      config:
        # devices: ["sda", "nvme0n1"]
    - type: temp
      config: {}
    - type: gpu
      config:
        # bin_path: "/usr/bin/nvidia-smi"
    - type: connections
      config:
        # states: ["ESTABLISHED", "LISTEN"]
```

## 测试策略

### 测试隔离方案

使用 build tag 隔离硬件依赖测试：

**文件命名约定：**
- `gpu_test.go` — 普通单元测试（mock 输出解析）
- `gpu_integration_test.go` — `//go:build nvidia` 标记，需要真实硬件

**Build Tag 命名：**

| 插件 | Build Tag | 说明 |
|------|-----------|------|
| GPU | `nvidia` | 需要 nvidia-smi |
| Temperature | `sensors` | 需要温度传感器 |

**CI 配置：**
```yaml
# .github/workflows/ci.yml
- name: Test
  run: go test -race ./...
  # 不加 build tag，跳过硬件依赖测试

# 可选：在有 GPU 的 runner 上运行
- name: Test GPU
  if: runner.has_gpu
  run: go test -race -tags nvidia ./internal/collector/inputs/gpu/
```

### 各插件测试清单

**通用测试模式（所有插件）：**
- `TestXxxInputInit` — 空配置、有效配置、无效配置
- `TestXxxInputGather` — 正常采集、空结果
- `TestXxxInputSampleConfig` — 返回非空字符串

**GPU 特殊测试：**
- `TestGPUInputParseOutput` — 正常 CSV 行解析
- `TestGPUInputParseOutputMalformed` — 字段数不匹配
- `TestGPUInputNotAvailable` — nvidia-smi 不存在
- `TestGPUInputTimeout` — 命令执行超时
- `TestGPUInputPartialFailure` — 部分 GPU 失败

**Temperature 特殊测试：**
- `TestTempInputNotAvailable` — 传感器不可用
- `TestTempInputEmpty` — 返回空切片

**Network Connections 特殊测试：**
- `TestConnectionsInputPermissionDenied` — 权限不足

**Disk IO 特殊测试：**
- `TestDiskIOInputDeviceFilter` — 设备过滤
- `TestDiskIOInputNonexistentDevice` — 不存在的设备

## 验证方式

```bash
# 单元测试（不含硬件依赖）
go test -race ./internal/collector/inputs/load/
go test -race ./internal/collector/inputs/diskio/
go test -race ./internal/collector/inputs/temp/
go test -race ./internal/collector/inputs/gpu/
go test -race ./internal/collector/inputs/connections/

# 含 GPU 硬件的集成测试
go test -race -tags nvidia ./internal/collector/inputs/gpu/

# 全量测试
go test -race ./...
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
| `internal/collector/inputs/gpu/gpu_integration_test.go` | 新建（`//go:build nvidia`） |
| `internal/collector/inputs/connections/connections.go` | 新建 |
| `internal/collector/inputs/connections/connections_test.go` | 新建 |
| `internal/app/agent.go` | 修改 — 添加 blank imports |
| `configs/config.yaml` | 修改 — 添加新插件示例 |
