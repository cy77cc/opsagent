package collector

// Aggregator is a plugin that aggregates metrics over a window.
type Aggregator interface {
	Add(in *Metric)
	Push(acc Accumulator)
	Reset()
	SampleConfig() string
}
