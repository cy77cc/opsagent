package process

import (
	"context"
	"fmt"
	"sort"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("process", func() collector.Input {
		return &ProcessInput{}
	})
}

type processInfo struct {
	pid        int32
	name       string
	cpuPercent float64
	memPercent float32
	memRSS     uint64
}

// ProcessInput gathers process-level metrics.
type ProcessInput struct {
	topN int
}

func (p *ProcessInput) Init(cfg map[string]interface{}) error {
	p.topN = 10 // default

	if v, ok := cfg["top_n"]; ok {
		switch n := v.(type) {
		case int:
			p.topN = n
		case int64:
			p.topN = int(n)
		case float64:
			p.topN = int(n)
		default:
			return fmt.Errorf("process: top_n must be an int, got %T", v)
		}
		if p.topN <= 0 {
			return fmt.Errorf("process: top_n must be positive, got %d", p.topN)
		}
	}
	return nil
}

func (p *ProcessInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return fmt.Errorf("process: failed to list pids: %w", err)
	}

	totalCount := len(pids)

	// Emit summary metric
	acc.AddGauge("process_summary", nil, map[string]interface{}{
		"total_count": int64(totalCount),
	})

	// Collect info for each process
	infos := make([]processInfo, 0, totalCount)
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return err
		}

		proc, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			continue // process may have exited
		}

		name, err := proc.NameWithContext(ctx)
		if err != nil {
			continue
		}

		cpuPct, err := proc.CPUPercentWithContext(ctx)
		if err != nil {
			continue
		}

		memPct, err := proc.MemoryPercentWithContext(ctx)
		if err != nil {
			continue
		}

		memInfo, err := proc.MemoryInfoWithContext(ctx)
		if err != nil {
			continue
		}

		var memRSS uint64
		if memInfo != nil {
			memRSS = memInfo.RSS
		}

		infos = append(infos, processInfo{
			pid:        pid,
			name:       name,
			cpuPercent: cpuPct,
			memPercent: memPct,
			memRSS:     memRSS,
		})
	}

	// Sort by CPU percent descending
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].cpuPercent > infos[j].cpuPercent
	})

	// Emit top N
	limit := p.topN
	if limit > len(infos) {
		limit = len(infos)
	}
	for i := 0; i < limit; i++ {
		info := infos[i]
		tags := map[string]string{
			"pid":  fmt.Sprintf("%d", info.pid),
			"name": info.name,
		}
		fields := map[string]interface{}{
			"cpu_percent":   info.cpuPercent,
			"mem_percent":   float64(info.memPercent),
			"mem_rss_bytes": int64(info.memRSS),
		}
		acc.AddGauge("process", tags, fields)
	}

	return nil
}

func (p *ProcessInput) SampleConfig() string {
	return `
  ## Number of top processes to report, sorted by CPU usage.
  # top_n = 10
`
}
