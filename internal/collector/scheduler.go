package collector

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// defaultAccumulatorSize is the default buffer capacity for per-gather accumulators.
const defaultAccumulatorSize = 1000

// ScheduledInput pairs an Input with its collection interval and static tags.
type ScheduledInput struct {
	Input    Input
	Interval time.Duration
	Tags     map[string]string
}

// Scheduler runs multiple inputs on their own intervals and sends
// collected metric batches to a shared output channel.
type Scheduler struct {
	inputs        []ScheduledInput
	processors    []Processor
	aggregators   []Aggregator
	outputs       []Output
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	logger        zerolog.Logger
	accSize       int
	startOnce     sync.Once
}

// NewScheduler creates a Scheduler for the given inputs, processors, aggregators, and outputs.
func NewScheduler(inputs []ScheduledInput, processors []Processor, aggregators []Aggregator, outputs []Output, logger zerolog.Logger) *Scheduler {
	return &Scheduler{
		inputs:      inputs,
		processors:  processors,
		aggregators: aggregators,
		outputs:     outputs,
		logger:      logger,
		accSize:     defaultAccumulatorSize,
	}
}

// Start begins collection goroutines for each input. It returns a channel
// that receives batches of metrics. The channel is closed when Stop is called.
func (s *Scheduler) Start(ctx context.Context) <-chan []*Metric {
	ch := make(chan []*Metric, len(s.inputs))
	s.startOnce.Do(func() {
		ctx, s.cancel = context.WithCancel(ctx)
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
	})
	return ch
}

// Stop cancels all input goroutines and waits for them to finish.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
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
