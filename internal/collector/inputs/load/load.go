package load

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/load"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("load", func() collector.Input {
		return &LoadInput{}
	})
}

// LoadInput gathers system load average metrics.
type LoadInput struct{}

func (l *LoadInput) Init(cfg map[string]interface{}) error {
	return nil
}

func (l *LoadInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	avg, err := load.AvgWithContext(ctx)
	if err != nil {
		return fmt.Errorf("load: failed to get load average: %w", err)
	}

	fields := map[string]interface{}{
		"load1":  avg.Load1,
		"load5":  avg.Load5,
		"load15": avg.Load15,
	}
	acc.AddGauge("load", nil, fields)
	return nil
}

func (l *LoadInput) SampleConfig() string {
	return `
  ## No configuration required for load input.
`
}
