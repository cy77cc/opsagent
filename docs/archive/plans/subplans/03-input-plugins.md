# Sub-Plan 3: Input Plugins

> **Parent:** [OpsAgent Full Implementation Plan](../2026-04-28-opsagent-full-implementation.md)
> **Depends on:** [Sub-Plan 2: Collector Pipeline Core](02-collector-pipeline.md)

**Goal:** Implement built-in Input plugins for host metrics collection using gopsutil.

**Files:**
- Create: `internal/collector/inputs/cpu/cpu.go`, `cpu_test.go`
- Create: `internal/collector/inputs/memory/memory.go`, `memory_test.go`
- Create: `internal/collector/inputs/disk/disk.go`, `disk_test.go`
- Create: `internal/collector/inputs/net/net.go`, `net_test.go`
- Create: `internal/collector/inputs/process/process.go`, `process_test.go`

---

## Task 3.1: CPU Input Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/inputs/cpu/cpu_test.go`:

```go
package cpu

import (
	"context"
	"testing"

	"opsagent/internal/collector"
)

func TestCPUGather(t *testing.T) {
	input := &CPUInput{}
	if err := input.Init(map[string]interface{}{"totalcpu": true}); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}

	found := false
	for _, m := range metrics {
		if m.Name() == "cpu" {
			found = true
			if _, ok := m.Fields()["usage_percent"]; !ok {
				t.Fatal("expected usage_percent field")
			}
		}
	}
	if !found {
		t.Fatal("expected cpu metric")
	}
}

func TestCPUGatherPerCPU(t *testing.T) {
	input := &CPUInput{}
	if err := input.Init(map[string]interface{}{"percpu": true, "totalcpu": false}); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric for per-cpu")
	}
}

func TestCPUSampleConfig(t *testing.T) {
	input := &CPUInput{}
	cfg := input.SampleConfig()
	if cfg == "" {
		t.Fatal("expected non-empty sample config")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/inputs/cpu/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement CPU input**

Create `internal/collector/inputs/cpu/cpu.go`:

```go
package cpu

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"

	"opsagent/internal/collector"
)

// CPUInput collects CPU usage metrics via gopsutil.
type CPUInput struct {
	perCPU   bool
	totalCPU bool
}

func (c *CPUInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["percpu"].(bool); ok {
		c.perCPU = v
	}
	if v, ok := cfg["totalcpu"].(bool); ok {
		c.totalCPU = v
	}
	if !c.perCPU && !c.totalCPU {
		c.totalCPU = true
	}
	return nil
}

func (c *CPUInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	percentages, err := cpu.PercentWithContext(ctx, time.Second, c.perCPU)
	if err != nil {
		return fmt.Errorf("cpu gather: %w", err)
	}

	if c.perCPU {
		for i, pct := range percentages {
			acc.AddGauge("cpu",
				map[string]string{"cpu": fmt.Sprintf("cpu%d", i)},
				map[string]interface{}{"usage_percent": pct},
			)
		}
	}

	if c.totalCPU && len(percentages) > 0 {
		total := 0.0
		for _, pct := range percentages {
			total += pct
		}
		avg := total / float64(len(percentages))
		acc.AddGauge("cpu",
			map[string]string{"cpu": "cpu-total"},
			map[string]interface{}{"usage_percent": avg},
		)
	}

	return nil
}

func (c *CPUInput) SampleConfig() string {
	return `# percpu = false
# totalcpu = true`
}

func init() {
	collector.RegisterInput("cpu", func() collector.Input { return &CPUInput{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/inputs/cpu/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/inputs/cpu/
git commit -m "feat(collector): add CPU input plugin"
```

---

## Task 3.2: Memory Input Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/inputs/memory/memory_test.go`:

```go
package memory

import (
	"context"
	"testing"

	"opsagent/internal/collector"
)

func TestMemoryGather(t *testing.T) {
	input := &MemoryInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}

	found := false
	for _, m := range metrics {
		if m.Name() == "memory" {
			found = true
			fields := m.Fields()
			if _, ok := fields["total_bytes"]; !ok {
				t.Fatal("expected total_bytes field")
			}
			if _, ok := fields["used_percent"]; !ok {
				t.Fatal("expected used_percent field")
			}
		}
	}
	if !found {
		t.Fatal("expected memory metric")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/inputs/memory/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Memory input**

Create `internal/collector/inputs/memory/memory.go`:

```go
package memory

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/mem"

	"opsagent/internal/collector"
)

// MemoryInput collects memory usage metrics via gopsutil.
type MemoryInput struct{}

func (m *MemoryInput) Init(_ map[string]interface{}) error { return nil }

func (m *MemoryInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return fmt.Errorf("memory gather: %w", err)
	}

	acc.AddGauge("memory", nil, map[string]interface{}{
		"total_bytes":     int64(vm.Total),
		"available_bytes": int64(vm.Available),
		"used_bytes":      int64(vm.Used),
		"used_percent":    vm.UsedPercent,
		"free_bytes":      int64(vm.Free),
	})

	swap, err := mem.SwapMemoryWithContext(ctx)
	if err == nil {
		acc.AddGauge("swap", nil, map[string]interface{}{
			"total_bytes":  int64(swap.Total),
			"used_bytes":   int64(swap.Used),
			"free_bytes":   int64(swap.Free),
			"used_percent": swap.UsedPercent,
		})
	}

	return nil
}

func (m *MemoryInput) SampleConfig() string { return "# no configuration required" }

func init() {
	collector.RegisterInput("memory", func() collector.Input { return &MemoryInput{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/inputs/memory/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/inputs/memory/
git commit -m "feat(collector): add Memory input plugin"
```

---

## Task 3.3: Disk Input Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/inputs/disk/disk_test.go`:

```go
package disk

import (
	"context"
	"testing"

	"opsagent/internal/collector"
)

func TestDiskGather(t *testing.T) {
	input := &DiskInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}

	found := false
	for _, m := range metrics {
		if m.Name() == "disk" {
			found = true
			if _, ok := m.Fields()["total_bytes"]; !ok {
				t.Fatal("expected total_bytes field")
			}
			if _, ok := m.Tags()["mountpoint"]; !ok {
				t.Fatal("expected mountpoint tag")
			}
		}
	}
	if !found {
		t.Fatal("expected disk metric")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/inputs/disk/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Disk input**

Create `internal/collector/inputs/disk/disk.go`:

```go
package disk

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/disk"

	"opsagent/internal/collector"
)

// DiskInput collects disk usage metrics for specified mount points.
type DiskInput struct {
	mountPoints []string
}

func (d *DiskInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["mount_points"].([]interface{}); ok {
		for _, mp := range v {
			if s, ok := mp.(string); ok {
				d.mountPoints = append(d.mountPoints, s)
			}
		}
	}
	return nil
}

func (d *DiskInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return fmt.Errorf("disk gather partitions: %w", err)
	}

	for _, p := range partitions {
		if len(d.mountPoints) > 0 && !contains(d.mountPoints, p.Mountpoint) {
			continue
		}

		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}

		acc.AddGauge("disk",
			map[string]string{
				"mountpoint": p.Mountpoint,
				"device":     p.Device,
				"fstype":     p.Fstype,
			},
			map[string]interface{}{
				"total_bytes":  int64(usage.Total),
				"used_bytes":   int64(usage.Used),
				"free_bytes":   int64(usage.Free),
				"used_percent": usage.UsedPercent,
			},
		)
	}

	return nil
}

func (d *DiskInput) SampleConfig() string {
	return `# mount_points = ["/", "/data"]`
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func init() {
	collector.RegisterInput("disk", func() collector.Input { return &DiskInput{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/inputs/disk/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/inputs/disk/
git commit -m "feat(collector): add Disk input plugin"
```

---

## Task 3.4: Network Input Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/inputs/net/net_test.go`:

```go
package net

import (
	"context"
	"testing"

	"opsagent/internal/collector"
)

func TestNetGather(t *testing.T) {
	input := &NetInput{}
	if err := input.Init(nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}

	found := false
	for _, m := range metrics {
		if m.Name() == "net" {
			found = true
			if _, ok := m.Fields()["bytes_sent"]; !ok {
				t.Fatal("expected bytes_sent field")
			}
		}
	}
	if !found {
		t.Fatal("expected net metric")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/inputs/net/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Network input**

Create `internal/collector/inputs/net/net.go`:

```go
package net

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/net"

	"opsagent/internal/collector"
)

// NetInput collects network I/O metrics.
type NetInput struct{}

func (n *NetInput) Init(_ map[string]interface{}) error { return nil }

func (n *NetInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	counters, err := net.IOCountersWithContext(ctx, false)
	if err != nil {
		return fmt.Errorf("net gather: %w", err)
	}

	for _, c := range counters {
		acc.AddCounter("net",
			map[string]string{"interface": c.Name},
			map[string]interface{}{
				"bytes_sent":   int64(c.BytesSent),
				"bytes_recv":   int64(c.BytesRecv),
				"packets_sent": int64(c.PacketsSent),
				"packets_recv": int64(c.PacketsRecv),
				"err_in":       int64(c.Errin),
				"err_out":      int64(c.Errout),
				"drop_in":      int64(c.Dropin),
				"drop_out":     int64(c.Dropout),
			},
		)
	}

	return nil
}

func (n *NetInput) SampleConfig() string { return "# no configuration required" }

func init() {
	collector.RegisterInput("net", func() collector.Input { return &NetInput{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/inputs/net/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/inputs/net/
git commit -m "feat(collector): add Network input plugin"
```

---

## Task 3.5: Process Input Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/inputs/process/process_test.go`:

```go
package process

import (
	"context"
	"testing"

	"opsagent/internal/collector"
)

func TestProcessGather(t *testing.T) {
	input := &ProcessInput{}
	if err := input.Init(map[string]interface{}{"top_n": 5.0}); err != nil {
		t.Fatalf("init: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("gather: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}

	// Should have a process_count summary metric
	found := false
	for _, m := range metrics {
		if m.Name() == "process_summary" {
			found = true
			if _, ok := m.Fields()["total_count"]; !ok {
				t.Fatal("expected total_count field")
			}
		}
	}
	if !found {
		t.Fatal("expected process_summary metric")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/inputs/process/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Process input**

Create `internal/collector/inputs/process/process.go`:

```go
package process

import (
	"context"
	"fmt"
	"sort"

	"github.com/shirou/gopsutil/v4/process"

	"opsagent/internal/collector"
)

// ProcessInput collects process-level metrics.
type ProcessInput struct {
	topN int
}

func (p *ProcessInput) Init(cfg map[string]interface{}) error {
	p.topN = 10
	if v, ok := cfg["top_n"]; ok {
		switch n := v.(type) {
		case float64:
			p.topN = int(n)
		case int:
			p.topN = n
		}
	}
	return nil
}

type procInfo struct {
	pid        int32
	name       string
	cpuPercent float64
	memPercent float32
	memRSS     uint64
}

func (p *ProcessInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return fmt.Errorf("process gather pids: %w", err)
	}

	acc.AddGauge("process_summary", nil, map[string]interface{}{
		"total_count": int64(len(pids)),
	})

	infos := make([]procInfo, 0, len(pids))
	for _, pid := range pids {
		proc, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			continue
		}

		name, _ := proc.NameWithContext(ctx)
		cpuPct, _ := proc.CPUPercentWithContext(ctx)
		memPct, _ := proc.MemoryPercentWithContext(ctx)
		memInfo, _ := proc.MemoryInfoWithContext(ctx)

		var rss uint64
		if memInfo != nil {
			rss = memInfo.RSS
		}

		infos = append(infos, procInfo{
			pid:        pid,
			name:       name,
			cpuPercent: cpuPct,
			memPercent: memPct,
			memRSS:     rss,
		})
	}

	// Sort by CPU percent descending, take top N
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].cpuPercent > infos[j].cpuPercent
	})

	n := p.topN
	if n > len(infos) {
		n = len(infos)
	}

	for _, info := range infos[:n] {
		acc.AddGauge("process",
			map[string]string{
				"pid":  fmt.Sprintf("%d", info.pid),
				"name": info.name,
			},
			map[string]interface{}{
				"cpu_percent":  info.cpuPercent,
				"mem_percent":  float64(info.memPercent),
				"mem_rss_bytes": int64(info.memRSS),
			},
		)
	}

	return nil
}

func (p *ProcessInput) SampleConfig() string {
	return `# top_n = 10`
}

func init() {
	collector.RegisterInput("process", func() collector.Input { return &ProcessInput{} })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/inputs/process/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/inputs/process/
git commit -m "feat(collector): add Process input plugin"
```
