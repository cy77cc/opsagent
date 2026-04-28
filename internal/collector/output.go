package collector

// Output is a plugin that writes metrics to an external destination.
type Output interface {
	Write(metrics []Metric) error
	Close() error
	SampleConfig() string
}
