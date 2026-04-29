package diskio

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("diskio", func() collector.Input {
		return &DiskIOInput{}
	})
}

// DiskIOInput gathers disk IO counters.
type DiskIOInput struct {
	devices []string
}

func (d *DiskIOInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["devices"]; ok {
		switch devs := v.(type) {
		case []interface{}:
			for _, item := range devs {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("diskio: devices items must be strings, got %T", item)
				}
				d.devices = append(d.devices, s)
			}
		case []string:
			d.devices = devs
		default:
			return fmt.Errorf("diskio: devices must be a list, got %T", v)
		}
	}
	return nil
}

func (d *DiskIOInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	counters, err := disk.IOCountersWithContext(ctx, d.devices...)
	if err != nil {
		return fmt.Errorf("diskio: failed to get IO counters: %w", err)
	}

	for name, counter := range counters {
		tags := map[string]string{
			"device": name,
		}
		fields := map[string]interface{}{
			"read_bytes":    int64(counter.ReadBytes),
			"write_bytes":   int64(counter.WriteBytes),
			"read_count":    int64(counter.ReadCount),
			"write_count":   int64(counter.WriteCount),
			"read_time_ms":  int64(counter.ReadTime),
			"write_time_ms": int64(counter.WriteTime),
		}
		acc.AddCounter("diskio", tags, fields)
	}

	return nil
}

func (d *DiskIOInput) SampleConfig() string {
	return `
  ## List of devices to filter. Empty means all devices.
  # devices = ["sda", "nvme0n1"]
`
}
