package cpu

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

func init() {
	collector.RegisterInput("cpu", func() collector.Input {
		return &CPUInput{}
	})
}

// CPUInput gathers CPU usage percentages.
type CPUInput struct {
	perCPU   bool
	totalCPU bool
}

func (c *CPUInput) Init(cfg map[string]interface{}) error {
	c.totalCPU = true // default

	if v, ok := cfg["percpu"]; ok {
		b, ok := v.(bool)
		if !ok {
			return fmt.Errorf("cpu: percpu must be a bool, got %T", v)
		}
		c.perCPU = b
	}
	if v, ok := cfg["totalcpu"]; ok {
		b, ok := v.(bool)
		if !ok {
			return fmt.Errorf("cpu: totalcpu must be a bool, got %T", v)
		}
		c.totalCPU = b
	}
	return nil
}

func (c *CPUInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	if c.perCPU {
		percentages, err := cpu.PercentWithContext(ctx, 0, true)
		if err != nil {
			return fmt.Errorf("cpu: failed to get per-cpu usage: %w", err)
		}
		for i, pct := range percentages {
			tags := map[string]string{
				"cpu": fmt.Sprintf("cpu%d", i),
			}
			fields := map[string]interface{}{
				"usage_percent": pct,
			}
			acc.AddGauge("cpu", tags, fields)
		}
	}

	if c.totalCPU {
		percentages, err := cpu.PercentWithContext(ctx, 0, false)
		if err != nil {
			return fmt.Errorf("cpu: failed to get total cpu usage: %w", err)
		}
		total := 0.0
		if len(percentages) > 0 {
			total = percentages[0]
		}
		tags := map[string]string{
			"cpu": "cpu-total",
		}
		fields := map[string]interface{}{
			"usage_percent": total,
		}
		acc.AddGauge("cpu", tags, fields)
	}

	return nil
}

func (c *CPUInput) SampleConfig() string {
	return `
  ## Collect per-CPU stats
  # percpu = false
  ## Collect total CPU stats (default true)
  # totalcpu = true
`
}
