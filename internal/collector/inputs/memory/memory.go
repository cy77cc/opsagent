package memory

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/mem"

	"nodeagentx/internal/collector"
)

func init() {
	collector.RegisterInput("memory", func() collector.Input {
		return &MemoryInput{}
	})
}

// MemoryInput gathers memory and swap usage statistics.
type MemoryInput struct{}

func (m *MemoryInput) Init(_ map[string]interface{}) error {
	return nil
}

func (m *MemoryInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	vmStat, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return fmt.Errorf("memory: failed to get virtual memory: %w", err)
	}

	fields := map[string]interface{}{
		"total_bytes":     int64(vmStat.Total),
		"available_bytes": int64(vmStat.Available),
		"used_bytes":      int64(vmStat.Used),
		"used_percent":    vmStat.UsedPercent,
		"free_bytes":      int64(vmStat.Free),
	}
	acc.AddGauge("memory", nil, fields)

	swapStat, err := mem.SwapMemoryWithContext(ctx)
	if err == nil && swapStat != nil {
		swapFields := map[string]interface{}{
			"total_bytes":  int64(swapStat.Total),
			"used_bytes":   int64(swapStat.Used),
			"free_bytes":   int64(swapStat.Free),
			"used_percent": swapStat.UsedPercent,
		}
		acc.AddGauge("swap", nil, swapFields)
	}

	return nil
}

func (m *MemoryInput) SampleConfig() string {
	return `
  ## No configuration required for memory input.
`
}
