package collector

import "context"

// Input is a plugin that gathers metrics on a schedule.
type Input interface {
	Init(cfg map[string]interface{}) error
	Gather(ctx context.Context, acc Accumulator) error
	SampleConfig() string
}
