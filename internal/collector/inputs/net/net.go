package net

import (
	"context"
	"fmt"

	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("net", func() collector.Input {
		return &NetInput{}
	})
}

// NetInput gathers network I/O statistics per interface.
type NetInput struct{}

func (n *NetInput) Init(_ map[string]interface{}) error {
	return nil
}

func (n *NetInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	counters, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return fmt.Errorf("net: failed to get io counters: %w", err)
	}

	for _, c := range counters {
		tags := map[string]string{
			"interface": c.Name,
		}
		fields := map[string]interface{}{
			"bytes_sent":   int64(c.BytesSent),
			"bytes_recv":   int64(c.BytesRecv),
			"packets_sent": int64(c.PacketsSent),
			"packets_recv": int64(c.PacketsRecv),
			"err_in":       int64(c.Errin),
			"err_out":      int64(c.Errout),
			"drop_in":      int64(c.Dropin),
			"drop_out":     int64(c.Dropout),
		}
		acc.AddCounter("net", tags, fields)
	}

	return nil
}

func (n *NetInput) SampleConfig() string {
	return `
  ## No configuration required for net input.
  ## Gathers per-interface network I/O counters.
`
}
