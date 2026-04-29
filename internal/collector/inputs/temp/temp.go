package temp

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("temp", func() collector.Input {
		return &TempInput{}
	})
}

// TempInput gathers temperature sensor metrics.
type TempInput struct {
	available bool
}

func (t *TempInput) Init(cfg map[string]interface{}) error {
	// Check availability by trying to read sensors once
	temps, err := sensors.TemperaturesWithContext(context.Background())
	if err != nil || len(temps) == 0 {
		t.available = false
		log.Info().Str("plugin", "temp").Msg("temperature sensors unavailable, skipping")
		return nil
	}
	t.available = true
	return nil
}

func (t *TempInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	if !t.available {
		return nil
	}

	temps, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		return fmt.Errorf("temp: failed to get temperatures: %w", err)
	}

	for _, sensor := range temps {
		tags := map[string]string{
			"sensor": sensor.SensorKey,
		}
		fields := map[string]interface{}{
			"temperature": sensor.Temperature,
		}
		if sensor.High != 0 {
			fields["high"] = sensor.High
		}
		if sensor.Critical != 0 {
			fields["critical"] = sensor.Critical
		}
		acc.AddGauge("temp", tags, fields)
	}

	return nil
}

func (t *TempInput) SampleConfig() string {
	return `
  ## No configuration required for temperature input.
  ## Sensors are auto-detected. If unavailable, plugin is silently skipped.
`
}
