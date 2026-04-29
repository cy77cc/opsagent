# Spec 4: Pipeline 增强

## Context

OpsAgent 的 Telegraf-style pipeline（Input → Processor → Aggregator → Output）已有 2 个 processor（tagger、regex）和 2 个 aggregator（avg、sum）。但缺少几个关键的 pipeline 组件：

- **Delta/Rate Processor** — 累计计数器（如 disk IO、网络字节数）需要转换为速率才有意义
- **Min/Max Aggregator** — 监控峰值需要知道窗口内的极值
- **Percentile Aggregator** — 延迟监控的 p50/p95/p99 是核心指标

这些组件直接插入现有 pipeline 架构，遵循已有的 processor 和 aggregator 接口。

**依赖：** Spec 1（测试模式已建立）

## 目标

1. 新增 Delta Processor，支持从累计计数器计算速率
2. 新增 Min/Max Aggregator，跟踪窗口内极值
3. 新增 Percentile Aggregator，支持 p50/p95/p99
4. 所有新组件测试覆盖率 ≥80%

## 设计

### 1. Delta/Rate Processor

**文件：** `internal/collector/processors/delta/delta.go`

**接口：** 实现 `collector.Processor`

**原理：** 存储每个 metric（按 name+tags 唯一标识）的上一次字段值，输出 `current - previous`。如果 previous 不存在（首次采集），输出 0 并存储当前值。

```go
type DeltaProcessor struct {
    fields           []string      // 需要计算 delta 的字段名
    maxStaleSeconds  int64         // 超过此时间未更新的条目自动过期
    previous         map[string]*metricSnapshot  // key: metric name+tags hash
    mu               sync.Mutex
}

type metricSnapshot struct {
    fields    map[string]interface{}
    timestamp time.Time
}
```

**配置：**
```yaml
[[processors.delta]]
  fields = ["read_bytes", "write_bytes", "read_count", "write_count"]
  max_stale_seconds = 300  # 5 分钟未更新则过期
```

**处理逻辑：**
1. 对每个 metric，检查 name 是否在 `fields` 中
2. 计算 metric 的唯一 key（name + sorted tags）
3. 查找 previous snapshot
4. 如果找到：输出 delta = current - previous（处理 int64/float64 类型转换）
5. 如果未找到：输出 0
6. 更新 previous snapshot
7. 后台 goroutine 定期清理过期条目

**类型处理：**
- int64 - int64 → int64
- float64 - float64 → float64
- int64 - float64 → float64（自动提升）
- 非数值字段 → 跳过，保留原值

**工作量：** M

### 2. Min/Max Aggregator

**文件：** `internal/collector/aggregators/minmax/minmax.go`

**接口：** 实现 `collector.Aggregator`

**原理：** 在聚合窗口内跟踪每个字段的最小值和最大值，Push 时输出。

```go
type MinMaxAggregator struct {
    fields []string
    min    map[string]map[string]interface{}  // field -> metric_key -> min_value
    max    map[string]map[string]interface{}  // field -> metric_key -> max_value
    mu     sync.Mutex
}
```

**配置：**
```yaml
[[aggregators.minmax]]
  fields = ["cpu_usage_percent", "memory_used_percent"]
  # push_interval 跟随全局 aggregator 周期（默认 60s）
```

**输出指标：**
- `{name}_min` — 窗口内最小值（Gauge）
- `{name}_max` — 窗口内最大值（Gauge）

**实现模式：** 完全遵循 `internal/collector/aggregators/avg/avg.go` 的模式：
- `Init(cfg)` — 解析配置
- `Add(metric)` — 更新 min/max
- `Push(acc)` — 输出 `{name}_min` 和 `{name}_max`
- `Reset()` — 清空状态

**工作量：** S

### 3. Percentile Aggregator

**文件：** `internal/collector/aggregators/percentile/percentile.go`

**接口：** 实现 `collector.Aggregator`

**原理：** 收集聚合窗口内的所有值，Push 时计算指定的百分位数。

```go
type PercentileAggregator struct {
    fields       []string
    percentiles  []float64     // e.g., [50, 95, 99]
    values       map[string][]float64  // field -> collected values
    mu           sync.Mutex
}
```

**配置：**
```yaml
[[aggregators.percentile]]
  fields = ["response_time_ms", "latency_ms"]
  percentiles = [50, 95, 99]
```

**输出指标：**
- `{name}_p50` — 第 50 百分位（Gauge）
- `{name}_p95` — 第 95 百分位（Gauge）
- `{name}_p99` — 第 99 百分位（Gauge）

**算法选择：**

| 方案 | 优点 | 缺点 |
|------|------|------|
| 排序切片 | 简单精确 | 内存随窗口大小线性增长 |
| T-Digest | 近似但内存固定 | 实现复杂，需引入依赖 |

**推荐：** 排序切片方案。理由：
1. 聚合窗口通常 60s，数据量有限（每秒一次采集 = 60 个值）
2. 精确性更重要（p99 的误差会被放大）
3. 不引入额外依赖

**实现：**
1. `Add(metric)` — 将指定字段的值 append 到 `values[field]`
2. `Push(acc)` — 对每个字段排序，计算百分位，输出
3. `Reset()` — 清空 `values`

**百分位计算：**
```go
func percentile(sorted []float64, p float64) float64 {
    if len(sorted) == 0 {
        return 0
    }
    index := p / 100 * float64(len(sorted)-1)
    lower := int(math.Floor(index))
    upper := int(math.Ceil(index))
    if lower == upper {
        return sorted[lower]
    }
    return sorted[lower] + (sorted[upper]-sorted[lower])*(index-float64(lower))
}
```

**工作量：** M

## 注册与集成

在 `internal/app/agent.go` 的 blank import 区域添加：

```go
_ "github.com/cy77cc/opsagent/internal/collector/processors/delta"
_ "github.com/cy77cc/opsagent/internal/collector/aggregators/minmax"
_ "github.com/cy77cc/opsagent/internal/collector/aggregators/percentile"
```

更新 `configs/config.yaml` 添加示例配置。

## 测试要求

### Delta Processor 测试
- 首次采集：输出 0，存储当前值
- 连续采集：输出正确的 delta
- 类型混合：int64 和 float64 混合处理
- 过期清理：超过 max_stale_seconds 的条目被清除
- 缺失字段：metric 缺少指定字段时跳过
- 并发安全：多个 input goroutine 同时写入

### Min/Max Aggregator 测试
- 单值窗口：min == max
- 多值窗口：正确识别极值
- Reset 后状态清空
- 非数值字段跳过

### Percentile Aggregator 测试
- 已知分布：验证 p50/p95/p99 计算正确
- 空窗口：Push 输出 0
- 单值：所有百分位等于该值
- 大数据量：1000+ 值的性能和正确性

## 验证方式

```bash
# 单元测试
go test -race ./internal/collector/processors/delta/
go test -race ./internal/collector/aggregators/minmax/
go test -race ./internal/collector/aggregators/percentile/

# 端到端验证：配合 disk IO input 使用 delta processor
go run ./cmd/agent run --config configs/test-pipeline.yaml --dry-run
```

## 关键文件

| 文件 | 操作 |
|------|------|
| `internal/collector/processors/delta/delta.go` | 新建 |
| `internal/collector/processors/delta/delta_test.go` | 新建 |
| `internal/collector/aggregators/minmax/minmax.go` | 新建 |
| `internal/collector/aggregators/minmax/minmax_test.go` | 新建 |
| `internal/collector/aggregators/percentile/percentile.go` | 新建 |
| `internal/collector/aggregators/percentile/percentile_test.go` | 新建 |
| `internal/app/agent.go` | 修改 — 添加 blank imports |
| `configs/config.yaml` | 修改 — 添加示例配置 |
