package collector

import "context"

// Output is a plugin that writes metrics to an external destination.
type Output interface {
	Init(cfg map[string]interface{}) error
	Write(ctx context.Context, metrics []Metric) error
	Close() error
	SampleConfig() string
}
