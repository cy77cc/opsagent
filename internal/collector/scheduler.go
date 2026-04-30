package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/health"
	"github.com/rs/zerolog"
)

// defaultAccumulatorSize is the default buffer capacity for per-gather accumulators.
const defaultAccumulatorSize = 1000

// ReloadConfig is the collector pipeline config snapshot, converted from config.CollectorConfig
// by CollectorReloader to avoid circular imports.
type ReloadConfig struct {
	Inputs      []PluginConfig
	Processors  []PluginConfig
	Aggregators []PluginConfig
	Outputs     []PluginConfig
}

// PluginConfig is a single plugin instance config.
type PluginConfig struct {
	Type   string
	Config map[string]interface{}
}

// ScheduledInput pairs an Input with its collection interval and static tags.
type ScheduledInput struct {
	Input    Input
	Interval time.Duration
	Tags     map[string]string
}

// Scheduler runs multiple inputs on their own intervals and sends
// collected metric batches to a shared output channel.
type Scheduler struct {
	inputs      []ScheduledInput
	processors  []Processor
	aggregators []Aggregator
	outputs     []Output
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	logger      zerolog.Logger
	accSize     int
	running     bool
	mu          sync.RWMutex
	interval    time.Duration
	outCh          chan []*Metric
	lastCollection time.Time
}

// defaultSchedulerInterval is the fallback collection interval when no inputs are configured.
const defaultSchedulerInterval = 10 * time.Second

// NewScheduler creates a Scheduler for the given inputs, processors, aggregators, and outputs.
func NewScheduler(inputs []ScheduledInput, processors []Processor, aggregators []Aggregator, outputs []Output, logger zerolog.Logger) *Scheduler {
	interval := defaultSchedulerInterval
	if len(inputs) > 0 && inputs[0].Interval > 0 {
		interval = inputs[0].Interval
	}
	return &Scheduler{
		inputs:      inputs,
		processors:  processors,
		aggregators: aggregators,
		outputs:     outputs,
		logger:      logger,
		accSize:     defaultAccumulatorSize,
		interval:    interval,
	}
}

// Start begins collection goroutines for each input. It returns a channel
// that receives batches of metrics. The channel is closed when Stop is called.
func (s *Scheduler) Start(ctx context.Context) <-chan []*Metric {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan []*Metric, len(s.inputs))
	s.outCh = ch
	ctx, s.cancel = context.WithCancel(ctx)
	s.running = true

	for _, si := range s.inputs {
		s.wg.Add(1)
		go s.runInput(ctx, si, ch)
	}
	// Periodically push aggregators.
	if len(s.aggregators) > 0 {
		s.wg.Add(1)
		go s.runAggregatorPush(ctx)
	}
	// Close channel when all goroutines are done.
	go func() {
		s.wg.Wait()
		close(ch)
	}()

	return ch
}

// Stop cancels all input goroutines and waits for them to finish.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	s.mu.Unlock()
	s.wg.Wait()
}

// HealthStatus reports the scheduler's running state.
func (s *Scheduler) HealthStatus() health.Status {
	s.mu.RLock()
	running := s.running
	inputCount := len(s.inputs)
	lastColl := s.lastCollection
	s.mu.RUnlock()
	status := "stopped"
	if running {
		status = "running"
	}
	details := map[string]any{"inputs_active": inputCount}
	if !lastColl.IsZero() {
		details["last_collection"] = lastColl.UTC().Format(time.RFC3339)
	}
	return health.Status{
		Status:  status,
		Details: details,
	}
}

// Reload stops all current inputs, rebuilds the pipeline from cfg, and restarts.
func (s *Scheduler) Reload(ctx context.Context, cfg ReloadConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop current goroutines.
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()

	// Push aggregator results before teardown.
	if len(s.aggregators) > 0 {
		acc := NewAccumulator(defaultAccumulatorSize)
		for _, agg := range s.aggregators {
			agg.Push(acc)
			agg.Reset()
		}
	}

	// Rebuild pipeline.
	var scheduledInputs []ScheduledInput
	for _, inCfg := range cfg.Inputs {
		factory, ok := DefaultRegistry.GetInput(inCfg.Type)
		if !ok {
			return fmt.Errorf("unknown input type: %q", inCfg.Type)
		}
		input := factory()
		if err := input.Init(inCfg.Config); err != nil {
			return fmt.Errorf("init input %q: %w", inCfg.Type, err)
		}
		scheduledInputs = append(scheduledInputs, ScheduledInput{Input: input, Interval: s.interval})
	}

	var processors []Processor
	for _, pCfg := range cfg.Processors {
		factory, ok := DefaultRegistry.GetProcessor(pCfg.Type)
		if !ok {
			return fmt.Errorf("unknown processor type: %q", pCfg.Type)
		}
		p := factory()
		if err := p.Init(pCfg.Config); err != nil {
			return fmt.Errorf("init processor %q: %w", pCfg.Type, err)
		}
		processors = append(processors, p)
	}

	var aggregators []Aggregator
	for _, aCfg := range cfg.Aggregators {
		factory, ok := DefaultRegistry.GetAggregator(aCfg.Type)
		if !ok {
			return fmt.Errorf("unknown aggregator type: %q", aCfg.Type)
		}
		agg := factory()
		if err := agg.Init(aCfg.Config); err != nil {
			return fmt.Errorf("init aggregator %q: %w", aCfg.Type, err)
		}
		aggregators = append(aggregators, agg)
	}

	var outputs []Output
	for _, oCfg := range cfg.Outputs {
		factory, ok := DefaultRegistry.GetOutput(oCfg.Type)
		if !ok {
			return fmt.Errorf("unknown output type: %q", oCfg.Type)
		}
		out := factory()
		if err := out.Init(oCfg.Config); err != nil {
			return fmt.Errorf("init output %q: %w", oCfg.Type, err)
		}
		outputs = append(outputs, out)
	}

	// Replace fields.
	s.inputs = scheduledInputs
	s.processors = processors
	s.aggregators = aggregators
	s.outputs = outputs

	// Restart goroutines if previously running.
	if s.running {
		ctx, s.cancel = context.WithCancel(ctx)
		for _, si := range s.inputs {
			s.wg.Add(1)
			go s.runInput(ctx, si, s.outCh)
		}
		if len(s.aggregators) > 0 {
			s.wg.Add(1)
			go s.runAggregatorPush(ctx)
		}
	}

	return nil
}

func (s *Scheduler) runInput(ctx context.Context, si ScheduledInput, ch chan<- []*Metric) {
	defer s.wg.Done()

	ticker := time.NewTicker(si.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gatherOnce(ctx, si, ch)
		}
	}
}

func (s *Scheduler) gatherOnce(ctx context.Context, si ScheduledInput, ch chan<- []*Metric) {
	accSize := s.accSize
	if accSize <= 0 {
		accSize = defaultAccumulatorSize
	}
	acc := NewAccumulator(accSize)

	if err := si.Input.Gather(ctx, acc); err != nil {
		s.logger.Error().Err(err).Msg("gather failed")
		return
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		return
	}

	// Apply static tags to all metrics.
	for _, m := range metrics {
		for k, v := range si.Tags {
			m.AddTag(k, v)
		}
	}

	// Apply processors in order.
	for _, p := range s.processors {
		metrics = p.Apply(metrics)
	}

	// Feed aggregators.
	for _, m := range metrics {
		for _, agg := range s.aggregators {
			agg.Add(m)
		}
	}

	// Write to outputs.
	if len(s.outputs) > 0 {
		for _, out := range s.outputs {
			batch := make([]Metric, len(metrics))
			for i, m := range metrics {
				batch[i] = *m
			}
			if err := out.Write(ctx, batch); err != nil {
				s.logger.Error().Err(err).Msg("output write failed")
			}
		}
	}

	s.mu.Lock()
	s.lastCollection = time.Now()
	s.mu.Unlock()

	select {
	case ch <- metrics:
	case <-ctx.Done():
	}
}

// runAggregators periodically pushes aggregator results and resets them.
func (s *Scheduler) runAggregatorPush(ctx context.Context) {
	defer s.wg.Done()
	// Push every 60 seconds by default.
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final push before exit.
			s.pushAggregators(ctx)
			return
		case <-ticker.C:
			s.pushAggregators(ctx)
		}
	}
}

// pushAggregators pushes all aggregator results to a temporary accumulator
// and sends the results to the output channel.
func (s *Scheduler) pushAggregators(ctx context.Context) {
	acc := NewAccumulator(defaultAccumulatorSize)
	for _, agg := range s.aggregators {
		agg.Push(acc)
		agg.Reset()
	}
	metrics := acc.Collect()
	if len(metrics) == 0 {
		return
	}
	// Write aggregated metrics to outputs.
	if len(s.outputs) > 0 {
		for _, out := range s.outputs {
			batch := make([]Metric, len(metrics))
			for i, m := range metrics {
				batch[i] = *m
			}
			if err := out.Write(ctx, batch); err != nil {
				s.logger.Error().Err(err).Msg("aggregator output write failed")
			}
		}
	}
}
