package collector

// Aggregator is a plugin that aggregates metrics over a window.
type Aggregator interface {
	Init(cfg map[string]interface{}) error
	Add(in *Metric)
	Push(acc Accumulator)
	Reset()
	SampleConfig() string
}
