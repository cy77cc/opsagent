package collector

import "context"

// Accumulator is the interface inputs use to emit metrics.
type Accumulator interface {
	AddFields(name string, tags map[string]string, fields map[string]interface{})
	AddGauge(name string, tags map[string]string, fields map[string]interface{})
	AddCounter(name string, tags map[string]string, fields map[string]interface{})
}

// Input is a plugin that gathers metrics on a schedule.
type Input interface {
	Init(cfg map[string]interface{}) error
	Gather(ctx context.Context, acc Accumulator) error
	SampleConfig() string
}
