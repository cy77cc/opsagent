package collector

// Output is a plugin that writes metrics to an external destination.
type Output interface {
	Init(cfg map[string]interface{}) error
	Write(metrics []Metric) error
	Close() error
	SampleConfig() string
}
