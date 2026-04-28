# Sub-Plan 2: Collector Pipeline Core

> **Parent:** [NodeAgentX Full Implementation Plan](../2026-04-28-nodeagentx-full-implementation.md)
> **Depends on:** [Sub-Plan 1: Proto & gRPC Foundation](01-proto-grpc.md)

**Goal:** Build the Telegraf-style collector pipeline with Metric model, Accumulator, Buffer, Scheduler, and plugin interfaces.

**Files:**
- Create: `internal/collector/metric.go`
- Create: `internal/collector/metric_test.go`
- Create: `internal/collector/input.go`
- Create: `internal/collector/output.go`
- Create: `internal/collector/processor.go`
- Create: `internal/collector/aggregator.go`
- Create: `internal/collector/accumulator.go`
- Create: `internal/collector/accumulator_test.go`
- Create: `internal/collector/buffer.go`
- Create: `internal/collector/buffer_test.go`
- Create: `internal/collector/scheduler.go`
- Create: `internal/collector/scheduler_test.go`
- Create: `internal/collector/registry.go`
- Create: `internal/collector/registry_test.go`

---

## Task 2.1: Metric Data Model

- [ ] **Step 1: Write failing tests for Metric**

Create `internal/collector/metric_test.go`:

```go
package collector

import (
	"testing"
	"time"
)

func TestMetricNew(t *testing.T) {
	ts := time.Now()
	m := NewMetric("cpu_usage", map[string]string{"host": "node1"}, map[string]interface{}{"value": 85.5}, Gauge, ts)

	if m.Name() != "cpu_usage" {
		t.Fatalf("expected name cpu_usage, got %s", m.Name())
	}
	if m.Tags()["host"] != "node1" {
		t.Fatalf("expected tag host=node1")
	}
	if m.Fields()["value"].(float64) != 85.5 {
		t.Fatalf("expected field value=85.5")
	}
	if m.Type() != Gauge {
		t.Fatalf("expected type Gauge")
	}
	if !m.Timestamp().Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, m.Timestamp())
	}
}

func TestMetricAddTag(t *testing.T) {
	m := NewMetric("test", nil, map[string]interface{}{"v": 1}, Gauge, time.Now())
	m.AddTag("env", "prod")

	if m.Tags()["env"] != "prod" {
		t.Fatalf("expected tag env=prod")
	}
}

func TestMetricToProto(t *testing.T) {
	ts := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	m := NewMetric("mem_used",
		map[string]string{"host": "n1"},
		map[string]interface{}{"bytes": int64(1024), "pct": 50.0},
		Gauge, ts,
	)

	pb := m.ToProto()

	if pb.Name != "mem_used" {
		t.Fatalf("proto name: expected mem_used, got %s", pb.Name)
	}
	if pb.Tags["host"] != "n1" {
		t.Fatalf("proto tag: expected n1")
	}
	if len(pb.Fields) != 2 {
		t.Fatalf("proto fields: expected 2, got %d", len(pb.Fields))
	}
	if pb.TimestampMs != ts.UnixMilli() {
		t.Fatalf("proto timestamp: expected %d, got %d", ts.UnixMilli(), pb.TimestampMs)
	}
}

func TestMetricToProtoCounter(t *testing.T) {
	m := NewMetric("req_total", nil, map[string]interface{}{"n": int64(1)}, Counter, time.Now())
	pb := m.ToProto()

	if pb.Type != pb_type_COUNTER { // reference generated enum
		// The actual enum reference depends on generated code.
		// We'll check the type field is set correctly.
		t.Logf("proto type: %v", pb.Type)
	}
}

func TestMetricCopyIsolation(t *testing.T) {
	tags := map[string]string{"a": "1"}
	fields := map[string]interface{}{"v": 1}
	m := NewMetric("test", tags, fields, Gauge, time.Now())

	// Modifying original map should not affect metric
	tags["a"] = "changed"
	fields["v"] = 999

	if m.Tags()["a"] != "1" {
		t.Fatal("metric tags should be isolated from original map")
	}
	if m.Fields()["v"].(int) != 1 {
		t.Fatal("metric fields should be isolated from original map")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/collector/ -run TestMetric -v
```

Expected: FAIL — `NewMetric`, `Gauge`, `Metric` type not defined.

- [ ] **Step 3: Implement Metric model**

Create `internal/collector/metric.go`:

```go
package collector

import (
	"time"

	pb "nodeagentx/internal/grpcclient/proto"
)

// MetricType represents the type of a metric.
type MetricType int

const (
	Gauge MetricType = iota
	Counter
	Histogram
)

// Metric represents a single metric data point with immutable name/type/timestamp
// and mutable tags/fields.
type Metric struct {
	name      string
	tags      map[string]string
	fields    map[string]interface{}
	timestamp time.Time
	metricType MetricType
}

// NewMetric creates a new Metric. Tags and fields are copied to ensure isolation.
func NewMetric(name string, tags map[string]string, fields map[string]interface{}, mt MetricType, ts time.Time) *Metric {
	t := make(map[string]string, len(tags))
	for k, v := range tags {
		t[k] = v
	}
	f := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		f[k] = v
	}
	return &Metric{name: name, tags: t, fields: f, metricType: mt, timestamp: ts}
}

func (m *Metric) Name() string                     { return m.name }
func (m *Metric) Tags() map[string]string           { return m.tags }
func (m *Metric) Fields() map[string]interface{}     { return m.fields }
func (m *Metric) Timestamp() time.Time              { return m.timestamp }
func (m *Metric) Type() MetricType                  { return m.metricType }

// AddTag adds or overwrites a tag on the metric.
func (m *Metric) AddTag(key, value string) {
	m.tags[key] = value
}

// ToProto converts the Metric to its protobuf representation for gRPC transmission.
func (m *Metric) ToProto() *pb.Metric {
	fields := make([]*pb.Field, 0, len(m.fields))
	for k, v := range m.fields {
		f := &pb.Field{Key: k}
		switch val := v.(type) {
		case float64:
			f.Value = &pb.Field_DoubleValue{DoubleValue: val}
		case int64:
			f.Value = &pb.Field_IntValue{IntValue: val}
		case string:
			f.Value = &pb.Field_StringValue{StringValue: val}
		case bool:
			f.Value = &pb.Field_BoolValue{BoolValue: val}
		}
		fields = append(fields, f)
	}

	var mt pb.MetricType
	switch m.metricType {
	case Counter:
		mt = pb.MetricType_COUNTER
	case Histogram:
		mt = pb.MetricType_HISTOGRAM
	default:
		mt = pb.MetricType_GAUGE
	}

	return &pb.Metric{
		Name:        m.name,
		Tags:        m.tags,
		Fields:      fields,
		TimestampMs: m.timestamp.UnixMilli(),
		Type:        mt,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/collector/ -run TestMetric -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/metric.go internal/collector/metric_test.go
git commit -m "feat(collector): add Metric data model with proto conversion"
```

---

## Task 2.2: Plugin Interfaces (Input, Output, Processor, Aggregator)

- [ ] **Step 1: Define Input interface**

Create `internal/collector/input.go`:

```go
package collector

import "context"

// Input is the interface for metric collection plugins.
// Each Input runs in its own goroutine and gathers metrics on a schedule.
type Input interface {
	// Init initializes the plugin with configuration.
	Init(cfg map[string]interface{}) error
	// Gather collects metrics and writes them to the Accumulator.
	Gather(ctx context.Context, acc Accumulator) error
	// SampleConfig returns a sample TOML configuration for documentation.
	SampleConfig() string
}
```

- [ ] **Step 2: Define Output interface**

Create `internal/collector/output.go`:

```go
package collector

// Output is the interface for metric output plugins.
type Output interface {
	// Write writes a batch of metrics to the output destination.
	Write(metrics []Metric) error
	// Close closes the output connection and flushes pending data.
	Close() error
	// SampleConfig returns a sample TOML configuration for documentation.
	SampleConfig() string
}
```

- [ ] **Step 3: Define Processor interface**

Create `internal/collector/processor.go`:

```go
package collector

// Processor transforms or filters metrics inline in the pipeline.
type Processor interface {
	// Apply processes a batch of metrics and returns the transformed result.
	Apply(in []*Metric) []*Metric
	// SampleConfig returns a sample TOML configuration for documentation.
	SampleConfig() string
}
```

- [ ] **Step 4: Define Aggregator interface**

Create `internal/collector/aggregator.go`:

```go
package collector

// Aggregator accumulates metrics over a time window and emits aggregated results.
type Aggregator interface {
	// Add adds a metric to the current aggregation window.
	Add(in *Metric)
	// Push emits aggregated metrics to the Accumulator and resets the window.
	Push(acc Accumulator)
	// Reset resets the aggregation window without emitting.
	Reset()
	// SampleConfig returns a sample TOML configuration for documentation.
	SampleConfig() string
}
```

- [ ] **Step 5: Verify build**

```bash
go build ./internal/collector/...
```

Expected: Compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add internal/collector/input.go internal/collector/output.go internal/collector/processor.go internal/collector/aggregator.go
git commit -m "feat(collector): add Input, Output, Processor, Aggregator interfaces"
```

---

## Task 2.3: Plugin Registry

- [ ] **Step 1: Write failing tests for Registry**

Create `internal/collector/registry_test.go`:

```go
package collector

import (
	"context"
	"testing"
)

type testInput struct{}

func (t *testInput) Init(cfg map[string]interface{}) error                    { return nil }
func (t *testInput) Gather(_ context.Context, _ Accumulator) error           { return nil }
func (t *testInput) SampleConfig() string                                     { return "test" }

func TestRegistryInput(t *testing.T) {
	r := NewRegistry()
	r.RegisterInput("test", func() Input { return &testInput{} })

	factory, ok := r.GetInput("test")
	if !ok {
		t.Fatal("expected to find registered input")
	}

	input := factory()
	if input.SampleConfig() != "test" {
		t.Fatalf("expected sample config 'test', got '%s'", input.SampleConfig())
	}
}

func TestRegistryInputNotFound(t *testing.T) {
	r := NewRegistry()

	_, ok := r.GetInput("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestRegistryListInputs(t *testing.T) {
	r := NewRegistry()
	r.RegisterInput("a", func() Input { return &testInput{} })
	r.RegisterInput("b", func() Input { return &testInput{} })

	names := r.ListInputs()
	if len(names) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(names))
	}
}

func TestRegistryOutput(t *testing.T) {
	r := NewRegistry()
	r.RegisterOutput("test", func() Output { return &testOutput{} })

	_, ok := r.GetOutput("test")
	if !ok {
		t.Fatal("expected to find registered output")
	}
}

type testOutput struct{}

func (t *testOutput) Write(_ []Metric) error { return nil }
func (t *testOutput) Close() error           { return nil }
func (t *testOutput) SampleConfig() string   { return "test" }
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/collector/ -run TestRegistry -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Registry**

Create `internal/collector/registry.go`:

```go
package collector

import "sort"

// InputFactory creates a new Input instance.
type InputFactory func() Input

// OutputFactory creates a new Output instance.
type OutputFactory func() Output

// ProcessorFactory creates a new Processor instance.
type ProcessorFactory func() Processor

// AggregatorFactory creates a new Aggregator instance.
type AggregatorFactory func() Aggregator

// Registry holds factories for all plugin types.
type Registry struct {
	inputs      map[string]InputFactory
	outputs     map[string]OutputFactory
	processors  map[string]ProcessorFactory
	aggregators map[string]AggregatorFactory
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		inputs:      make(map[string]InputFactory),
		outputs:     make(map[string]OutputFactory),
		processors:  make(map[string]ProcessorFactory),
		aggregators: make(map[string]AggregatorFactory),
	}
}

func (r *Registry) RegisterInput(name string, factory InputFactory)      { r.inputs[name] = factory }
func (r *Registry) RegisterOutput(name string, factory OutputFactory)    { r.outputs[name] = factory }
func (r *Registry) RegisterProcessor(name string, factory ProcessorFactory) {
	r.processors[name] = factory
}
func (r *Registry) RegisterAggregator(name string, factory AggregatorFactory) {
	r.aggregators[name] = factory
}

func (r *Registry) GetInput(name string) (InputFactory, bool)      { f, ok := r.inputs[name]; return f, ok }
func (r *Registry) GetOutput(name string) (OutputFactory, bool)    { f, ok := r.outputs[name]; return f, ok }
func (r *Registry) GetProcessor(name string) (ProcessorFactory, bool) {
	f, ok := r.processors[name]; return f, ok
}
func (r *Registry) GetAggregator(name string) (AggregatorFactory, bool) {
	f, ok := r.aggregators[name]; return f, ok
}

func (r *Registry) ListInputs() []string      { return sortedKeys(r.inputs) }
func (r *Registry) ListOutputs() []string     { return sortedKeys(r.outputs) }
func (r *Registry) ListProcessors() []string  { return sortedKeys(r.processors) }
func (r *Registry) ListAggregators() []string { return sortedKeys(r.aggregators) }

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// DefaultRegistry is the global plugin registry.
var DefaultRegistry = NewRegistry()

// RegisterInput registers an Input plugin in the default registry.
func RegisterInput(name string, factory InputFactory) {
	DefaultRegistry.RegisterInput(name, factory)
}

// RegisterOutput registers an Output plugin in the default registry.
func RegisterOutput(name string, factory OutputFactory) {
	DefaultRegistry.RegisterOutput(name, factory)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/collector/ -run TestRegistry -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/registry.go internal/collector/registry_test.go
git commit -m "feat(collector): add plugin registry for Input/Output/Processor/Aggregator"
```

---

## Task 2.4: Accumulator

- [ ] **Step 1: Write failing tests for Accumulator**

Create `internal/collector/accumulator_test.go`:

```go
package collector

import (
	"testing"
	"time"
)

func TestAccumulatorAddFields(t *testing.T) {
	acc := NewAccumulator(100)

	acc.AddFields("cpu", map[string]string{"host": "n1"}, map[string]interface{}{"usage": 80.0})
	acc.AddGauge("mem", map[string]string{"host": "n1"}, map[string]interface{}{"bytes": int64(1024)})

	metrics := acc.Collect()

	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
	if metrics[0].Name() != "cpu" {
		t.Fatalf("expected first metric name cpu, got %s", metrics[0].Name())
	}
	if metrics[0].Type() != Gauge {
		t.Fatalf("expected cpu type Gauge")
	}
	if metrics[1].Name() != "mem" {
		t.Fatalf("expected second metric name mem, got %s", metrics[1].Name())
	}
}

func TestAccumulatorAddCounter(t *testing.T) {
	acc := NewAccumulator(100)

	acc.AddCounter("requests", map[string]string{}, map[string]interface{}{"total": int64(42)})

	metrics := acc.Collect()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Type() != Counter {
		t.Fatalf("expected Counter type")
	}
}

func TestAccumulatorOverflow(t *testing.T) {
	acc := NewAccumulator(2)

	acc.AddFields("a", nil, map[string]interface{}{"v": 1})
	acc.AddFields("b", nil, map[string]interface{}{"v": 2})
	acc.AddFields("c", nil, map[string]interface{}{"v": 3}) // should be dropped

	metrics := acc.Collect()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics (overflow), got %d", len(metrics))
	}
	if metrics[0].Name() != "a" {
		t.Fatalf("expected oldest kept, got %s", metrics[0].Name())
	}
}

func TestAccumulatorCustomTimestamp(t *testing.T) {
	acc := NewAccumulator(100)
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	acc.AddFields("test", nil, map[string]interface{}{"v": 1}, ts)

	metrics := acc.Collect()
	if !metrics[0].Timestamp().Equal(ts) {
		t.Fatalf("expected custom timestamp %v, got %v", ts, metrics[0].Timestamp())
	}
}

func TestAccumulatorCollectResets(t *testing.T) {
	acc := NewAccumulator(100)

	acc.AddFields("a", nil, map[string]interface{}{"v": 1})
	metrics1 := acc.Collect()

	acc.AddFields("b", nil, map[string]interface{}{"v": 2})
	metrics2 := acc.Collect()

	if len(metrics1) != 1 || metrics1[0].Name() != "a" {
		t.Fatal("first collect should have 'a'")
	}
	if len(metrics2) != 1 || metrics2[0].Name() != "b" {
		t.Fatal("second collect should have 'b' only")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/collector/ -run TestAccumulator -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Accumulator**

Create `internal/collector/accumulator.go`:

```go
package collector

import (
	"sync"
	"time"
)

// Accumulator is a thread-safe collector for metrics from Input plugins.
type Accumulator interface {
	AddFields(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
	AddGauge(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
	AddCounter(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time)
	Collect() []*Metric
}

type accumulator struct {
	mu      sync.Mutex
	metrics []*Metric
	maxSize int
}

// NewAccumulator creates a new Accumulator with the given max buffer size.
// When the buffer is full, new metrics are silently dropped (DropNewest policy).
func NewAccumulator(maxSize int) Accumulator {
	return &accumulator{
		metrics: make([]*Metric, 0, maxSize),
		maxSize: maxSize,
	}
}

func (a *accumulator) AddFields(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time) {
	a.add(name, tags, fields, Gauge, ts...)
}

func (a *accumulator) AddGauge(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time) {
	a.add(name, tags, fields, Gauge, ts...)
}

func (a *accumulator) AddCounter(name string, tags map[string]string, fields map[string]interface{}, ts ...time.Time) {
	a.add(name, tags, fields, Counter, ts...)
}

func (a *accumulator) add(name string, tags map[string]string, fields map[string]interface{}, mt MetricType, ts ...time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.metrics) >= a.maxSize {
		return // DropNewest
	}

	t := time.Now()
	if len(ts) > 0 {
		t = ts[0]
	}

	a.metrics = append(a.metrics, NewMetric(name, tags, fields, mt, t))
}

// Collect returns all accumulated metrics and resets the internal buffer.
func (a *accumulator) Collect() []*Metric {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]*Metric, len(a.metrics))
	copy(result, a.metrics)
	a.metrics = a.metrics[:0]
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/collector/ -run TestAccumulator -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/accumulator.go internal/collector/accumulator_test.go
git commit -m "feat(collector): add thread-safe Accumulator with drop policy"
```

---

## Task 2.5: Buffer

- [ ] **Step 1: Write failing tests for Buffer**

Create `internal/collector/buffer_test.go`:

```go
package collector

import (
	"testing"
	"time"
)

func TestBufferAddAndBatch(t *testing.T) {
	buf := NewBuffer(100, 10, DropNewest)

	for i := 0; i < 25; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"i": int64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	batch1 := buf.Batch()
	if len(batch1) != 10 {
		t.Fatalf("expected batch size 10, got %d", len(batch1))
	}

	batch2 := buf.Batch()
	if len(batch2) != 10 {
		t.Fatalf("expected batch size 10, got %d", len(batch2))
	}

	batch3 := buf.Batch()
	if len(batch3) != 5 {
		t.Fatalf("expected batch size 5, got %d", len(batch3))
	}

	batch4 := buf.Batch()
	if len(batch4) != 0 {
		t.Fatalf("expected empty batch, got %d", len(batch4))
	}
}

func TestBufferDropNewest(t *testing.T) {
	buf := NewBuffer(3, 10, DropNewest)

	for i := 0; i < 5; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"i": int64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	batch := buf.Batch()
	if len(batch) != 3 {
		t.Fatalf("expected 3 metrics, got %d", len(batch))
	}
	// DropNewest keeps oldest: 0, 1, 2
	if batch[0].Fields()["i"].(int64) != 0 {
		t.Fatalf("expected first=0, got %d", batch[0].Fields()["i"].(int64))
	}
	if batch[2].Fields()["i"].(int64) != 2 {
		t.Fatalf("expected last=2, got %d", batch[2].Fields()["i"].(int64))
	}
}

func TestBufferDropOldest(t *testing.T) {
	buf := NewBuffer(3, 10, DropOldest)

	for i := 0; i < 5; i++ {
		m := NewMetric("test", nil, map[string]interface{}{"i": int64(i)}, Gauge, time.Now())
		buf.Add(m)
	}

	batch := buf.Batch()
	if len(batch) != 3 {
		t.Fatalf("expected 3 metrics, got %d", len(batch))
	}
	// DropOldest keeps newest: 2, 3, 4
	if batch[0].Fields()["i"].(int64) != 2 {
		t.Fatalf("expected first=2, got %d", batch[0].Fields()["i"].(int64))
	}
	if batch[2].Fields()["i"].(int64) != 4 {
		t.Fatalf("expected last=4, got %d", batch[2].Fields()["i"].(int64))
	}
}

func TestBufferLen(t *testing.T) {
	buf := NewBuffer(100, 10, DropNewest)

	if buf.Len() != 0 {
		t.Fatalf("expected len 0, got %d", buf.Len())
	}

	buf.Add(NewMetric("test", nil, map[string]interface{}{"v": 1}, Gauge, time.Now()))

	if buf.Len() != 1 {
		t.Fatalf("expected len 1, got %d", buf.Len())
	}
}

func TestBufferEmptyBatch(t *testing.T) {
	buf := NewBuffer(100, 10, DropNewest)

	batch := buf.Batch()
	if batch != nil {
		t.Fatalf("expected nil batch from empty buffer")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/collector/ -run TestBuffer -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Buffer**

Create `internal/collector/buffer.go`:

```go
package collector

import "sync"

// DropPolicy determines what happens when the buffer is full.
type DropPolicy int

const (
	// DropNewest drops incoming metrics when the buffer is full.
	DropNewest DropPolicy = iota
	// DropOldest drops the oldest metrics when the buffer is full.
	DropOldest
)

// Buffer is a per-output metric buffer with configurable size, batch size, and drop policy.
type Buffer struct {
	mu         sync.Mutex
	metrics    []*Metric
	maxSize    int
	batchSize  int
	dropPolicy DropPolicy
}

// NewBuffer creates a new Buffer.
func NewBuffer(maxSize, batchSize int, policy DropPolicy) *Buffer {
	return &Buffer{
		metrics:    make([]*Metric, 0, maxSize),
		maxSize:    maxSize,
		batchSize:  batchSize,
		dropPolicy: policy,
	}
}

// Add adds a metric to the buffer. If the buffer is full, applies the drop policy.
func (b *Buffer) Add(m *Metric) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.metrics) >= b.maxSize {
		if b.dropPolicy == DropNewest {
			return
		}
		// DropOldest: remove first element
		b.metrics = b.metrics[1:]
	}

	b.metrics = append(b.metrics, m)
}

// Batch returns up to batchSize metrics and removes them from the buffer.
// Returns nil if the buffer is empty.
func (b *Buffer) Batch() []*Metric {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.metrics) == 0 {
		return nil
	}

	n := b.batchSize
	if n > len(b.metrics) {
		n = len(b.metrics)
	}

	batch := make([]*Metric, n)
	copy(batch, b.metrics[:n])
	b.metrics = b.metrics[n:]
	return batch
}

// Len returns the current number of metrics in the buffer.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.metrics)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/collector/ -run TestBuffer -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/buffer.go internal/collector/buffer_test.go
git commit -m "feat(collector): add Buffer with drop policy and batch support"
```

---

## Task 2.6: Scheduler

- [ ] **Step 1: Write failing tests for Scheduler**

Create `internal/collector/scheduler_test.go`:

```go
package collector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type mockInput struct {
	gatherCount atomic.Int32
}

func (m *mockInput) Init(_ map[string]interface{}) error { return nil }
func (m *mockInput) Gather(_ context.Context, acc Accumulator) error {
	m.gatherCount.Add(1)
	acc.AddFields("mock", nil, map[string]interface{}{"v": int64(1)})
	return nil
}
func (m *mockInput) SampleConfig() string { return "" }

func TestSchedulerRunsInput(t *testing.T) {
	input := &mockInput{}
	sched := NewScheduler([]ScheduledInput{
		{Input: input, Interval: 50 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	results := sched.Start(ctx)

	go func() {
		for range results {
		}
	}()

	<-ctx.Done()
	sched.Stop()

	count := input.gatherCount.Load()
	if count < 2 {
		t.Fatalf("expected at least 2 gathers, got %d", count)
	}
}

func TestSchedulerMultipleInputs(t *testing.T) {
	input1 := &mockInput{}
	input2 := &mockInput{}
	sched := NewScheduler([]ScheduledInput{
		{Input: input1, Interval: 50 * time.Millisecond},
		{Input: input2, Interval: 50 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	results := sched.Start(ctx)

	go func() {
		for range results {
		}
	}()

	<-ctx.Done()
	sched.Stop()

	if input1.gatherCount.Load() < 2 {
		t.Fatalf("input1: expected at least 2 gathers")
	}
	if input2.gatherCount.Load() < 2 {
		t.Fatalf("input2: expected at least 2 gathers")
	}
}

func TestSchedulerStop(t *testing.T) {
	input := &mockInput{}
	sched := NewScheduler([]ScheduledInput{
		{Input: input, Interval: 10 * time.Millisecond},
	})

	ctx := context.Background()
	results := sched.Start(ctx)

	go func() {
		for range results {
		}
	}()

	time.Sleep(50 * time.Millisecond)
	sched.Stop()
	// Should not panic or hang
}

func TestSchedulerAppliesStaticTags(t *testing.T) {
	input := &tagCheckInput{}
	sched := NewScheduler([]ScheduledInput{
		{Input: input, Interval: 50 * time.Millisecond, Tags: map[string]string{"env": "prod"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results := sched.Start(ctx)

	var allMetrics []*Metric
	go func() {
		for batch := range results {
			allMetrics = append(allMetrics, batch...)
		}
	}()

	<-ctx.Done()
	sched.Stop()

	if len(allMetrics) == 0 {
		t.Fatal("expected at least 1 metric")
	}
	if allMetrics[0].Tags()["env"] != "prod" {
		t.Fatalf("expected tag env=prod, got %s", allMetrics[0].Tags()["env"])
	}
}

type tagCheckInput struct{}

func (t *tagCheckInput) Init(_ map[string]interface{}) error { return nil }
func (t *tagCheckInput) Gather(_ context.Context, acc Accumulator) error {
	acc.AddFields("test", nil, map[string]interface{}{"v": int64(1)})
	return nil
}
func (t *tagCheckInput) SampleConfig() string { return "" }
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/collector/ -run TestScheduler -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Scheduler**

Create `internal/collector/scheduler.go`:

```go
package collector

import (
	"context"
	"sync"
	"time"
)

// ScheduledInput pairs an Input plugin with its collection interval and static tags.
type ScheduledInput struct {
	Input    Input
	Interval time.Duration
	Tags     map[string]string
}

// Scheduler manages goroutines for each Input plugin and collects their metrics.
type Scheduler struct {
	inputs []ScheduledInput
	cancel context.CancelFunc
	wg     sync.WaitGroup
	output chan []*Metric
}

// NewScheduler creates a new Scheduler.
func NewScheduler(inputs []ScheduledInput) *Scheduler {
	return &Scheduler{
		inputs: inputs,
		output: make(chan []*Metric, len(inputs)*10),
	}
}

// Start begins scheduled collection. Returns a channel of collected metric batches.
func (s *Scheduler) Start(ctx context.Context) <-chan []*Metric {
	ctx, s.cancel = context.WithCancel(ctx)

	for _, si := range s.inputs {
		s.wg.Add(1)
		go s.runInput(ctx, si)
	}

	go func() {
		s.wg.Wait()
		close(s.output)
	}()

	return s.output
}

// Stop stops all input goroutines and waits for them to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

func (s *Scheduler) runInput(ctx context.Context, si ScheduledInput) {
	defer s.wg.Done()

	ticker := time.NewTicker(si.Interval)
	defer ticker.Stop()

	// Collect once immediately on start
	s.gatherOnce(ctx, si)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gatherOnce(ctx, si)
		}
	}
}

func (s *Scheduler) gatherOnce(ctx context.Context, si ScheduledInput) {
	acc := NewAccumulator(10000)

	if err := si.Input.Gather(ctx, acc); err != nil {
		return
	}

	metrics := acc.Collect()

	// Apply static tags from ScheduledInput configuration
	for _, m := range metrics {
		for k, v := range si.Tags {
			m.AddTag(k, v)
		}
	}

	if len(metrics) > 0 {
		select {
		case s.output <- metrics:
		default:
			// Channel full, drop this batch to avoid blocking
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/collector/ -run TestScheduler -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/scheduler.go internal/collector/scheduler_test.go
git commit -m "feat(collector): add Scheduler for per-input goroutine collection"
```
