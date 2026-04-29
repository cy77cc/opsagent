package collector

// Processor is a plugin that transforms metrics in-flight.
type Processor interface {
	Init(cfg map[string]interface{}) error
	Apply(in []*Metric) []*Metric
	SampleConfig() string
}
