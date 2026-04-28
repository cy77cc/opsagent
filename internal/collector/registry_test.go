package collector

import (
	"context"
	"sort"
	"testing"
)

// mockInput implements Input for testing.
type mockInput struct {
	name string
}

func (m *mockInput) Init(_ map[string]interface{}) error { return nil }
func (m *mockInput) Gather(_ context.Context, _ Accumulator) error { return nil }
func (m *mockInput) SampleConfig() string { return "" }

// mockOutput implements Output for testing.
type mockOutput struct {
	name string
}

func (m *mockOutput) Write(_ []Metric) error { return nil }
func (m *mockOutput) Close() error            { return nil }
func (m *mockOutput) SampleConfig() string    { return "" }

func TestRegistryInput(t *testing.T) {
	r := NewRegistry()
	r.RegisterInput("mock", func() Input { return &mockInput{name: "mock"} })

	f, ok := r.GetInput("mock")
	if !ok {
		t.Fatal("GetInput('mock') returned false, want true")
	}
	input := f()
	if input.SampleConfig() != "" {
		t.Errorf("SampleConfig() = %q, want empty", input.SampleConfig())
	}
}

func TestRegistryInputNotFound(t *testing.T) {
	r := NewRegistry()

	_, ok := r.GetInput("nonexistent")
	if ok {
		t.Fatal("GetInput('nonexistent') returned true, want false")
	}
}

func TestRegistryListInputs(t *testing.T) {
	r := NewRegistry()
	r.RegisterInput("alpha", func() Input { return &mockInput{name: "alpha"} })
	r.RegisterInput("beta", func() Input { return &mockInput{name: "beta"} })
	r.RegisterInput("gamma", func() Input { return &mockInput{name: "gamma"} })

	names := r.ListInputs()
	sort.Strings(names)
	expected := []string{"alpha", "beta", "gamma"}

	if len(names) != len(expected) {
		t.Fatalf("ListInputs() len = %d, want %d", len(names), len(expected))
	}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("ListInputs()[%d] = %q, want %q", i, n, expected[i])
		}
	}
}

func TestRegistryOutput(t *testing.T) {
	r := NewRegistry()
	r.RegisterOutput("mock", func() Output { return &mockOutput{name: "mock"} })

	f, ok := r.GetOutput("mock")
	if !ok {
		t.Fatal("GetOutput('mock') returned false, want true")
	}
	output := f()
	if output.SampleConfig() != "" {
		t.Errorf("SampleConfig() = %q, want empty", output.SampleConfig())
	}
}

func TestRegistryProcessor(t *testing.T) {
	r := NewRegistry()
	r.RegisterProcessor("nop", func() Processor { return &nopProcessor{} })

	_, ok := r.GetProcessor("nop")
	if !ok {
		t.Fatal("GetProcessor('nop') returned false, want true")
	}
}

func TestRegistryAggregator(t *testing.T) {
	r := NewRegistry()
	r.RegisterAggregator("sum", func() Aggregator { return &sumAggregator{} })

	_, ok := r.GetAggregator("sum")
	if !ok {
		t.Fatal("GetAggregator('sum') returned false, want true")
	}
}

// nopProcessor is a test helper.
type nopProcessor struct{}

func (n *nopProcessor) Apply(in []*Metric) []*Metric { return in }
func (n *nopProcessor) SampleConfig() string          { return "" }

// sumAggregator is a test helper.
type sumAggregator struct{}

func (s *sumAggregator) Add(_ *Metric)       {}
func (s *sumAggregator) Push(_ Accumulator)   {}
func (s *sumAggregator) Reset()               {}
func (s *sumAggregator) SampleConfig() string { return "" }
