package collector

// Processor is a plugin that transforms metrics in-flight.
type Processor interface {
	Apply(in []*Metric) []*Metric
	SampleConfig() string
}
