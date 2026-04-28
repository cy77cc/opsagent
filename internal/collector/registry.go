package collector

import "slices"

// Factory types for each plugin kind.
type InputFactory func() Input
type OutputFactory func() Output
type ProcessorFactory func() Processor
type AggregatorFactory func() Aggregator

// Registry holds factories for all plugin types.
type Registry struct {
	inputs      map[string]InputFactory
	outputs     map[string]OutputFactory
	processors  map[string]ProcessorFactory
	aggregators map[string]AggregatorFactory
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		inputs:      make(map[string]InputFactory),
		outputs:     make(map[string]OutputFactory),
		processors:  make(map[string]ProcessorFactory),
		aggregators: make(map[string]AggregatorFactory),
	}
}

// RegisterInput registers an Input factory by name.
func (r *Registry) RegisterInput(name string, f InputFactory) {
	r.inputs[name] = f
}

// GetInput returns the Input factory for the given name.
func (r *Registry) GetInput(name string) (InputFactory, bool) {
	f, ok := r.inputs[name]
	return f, ok
}

// ListInputs returns sorted registered Input names.
func (r *Registry) ListInputs() []string {
	return sortedKeys(r.inputs)
}

// RegisterOutput registers an Output factory by name.
func (r *Registry) RegisterOutput(name string, f OutputFactory) {
	r.outputs[name] = f
}

// GetOutput returns the Output factory for the given name.
func (r *Registry) GetOutput(name string) (OutputFactory, bool) {
	f, ok := r.outputs[name]
	return f, ok
}

// ListOutputs returns sorted registered Output names.
func (r *Registry) ListOutputs() []string {
	return sortedKeys(r.outputs)
}

// RegisterProcessor registers a Processor factory by name.
func (r *Registry) RegisterProcessor(name string, f ProcessorFactory) {
	r.processors[name] = f
}

// GetProcessor returns the Processor factory for the given name.
func (r *Registry) GetProcessor(name string) (ProcessorFactory, bool) {
	f, ok := r.processors[name]
	return f, ok
}

// ListProcessors returns sorted registered Processor names.
func (r *Registry) ListProcessors() []string {
	return sortedKeys(r.processors)
}

// RegisterAggregator registers an Aggregator factory by name.
func (r *Registry) RegisterAggregator(name string, f AggregatorFactory) {
	r.aggregators[name] = f
}

// GetAggregator returns the Aggregator factory for the given name.
func (r *Registry) GetAggregator(name string) (AggregatorFactory, bool) {
	f, ok := r.aggregators[name]
	return f, ok
}

// ListAggregators returns sorted registered Aggregator names.
func (r *Registry) ListAggregators() []string {
	return sortedKeys(r.aggregators)
}

// DefaultRegistry is the global plugin registry.
var DefaultRegistry = NewRegistry()

// RegisterInput registers an Input factory in the default registry.
func RegisterInput(name string, f InputFactory) {
	DefaultRegistry.RegisterInput(name, f)
}

// RegisterOutput registers an Output factory in the default registry.
func RegisterOutput(name string, f OutputFactory) {
	DefaultRegistry.RegisterOutput(name, f)
}

// sortedKeys returns the sorted keys of a map.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
