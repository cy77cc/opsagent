package collector

import (
	"context"
	"sync"
	"time"
)

// ScheduledInput pairs an Input with its collection interval and static tags.
type ScheduledInput struct {
	Input    Input
	Interval time.Duration
	Tags     map[string]string
}

// Scheduler runs multiple inputs on their own intervals and sends
// collected metric batches to a shared output channel.
type Scheduler struct {
	inputs []ScheduledInput
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a Scheduler for the given inputs.
func NewScheduler(inputs []ScheduledInput) *Scheduler {
	return &Scheduler{inputs: inputs}
}

// Start begins collection goroutines for each input. It returns a channel
// that receives batches of metrics. The channel is closed when Stop is called.
func (s *Scheduler) Start(ctx context.Context) <-chan []*Metric {
	ctx, s.cancel = context.WithCancel(ctx)
	ch := make(chan []*Metric, len(s.inputs))

	for _, si := range s.inputs {
		s.wg.Add(1)
		go s.runInput(ctx, si, ch)
	}

	// Close channel when all goroutines are done
	go func() {
		s.wg.Wait()
		close(ch)
	}()

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
	acc := NewAccumulator(1000)

	if err := si.Input.Gather(ctx, acc); err != nil {
		return
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		return
	}

	// Apply static tags to all metrics
	for _, m := range metrics {
		for k, v := range si.Tags {
			m.AddTag(k, v)
		}
	}

	select {
	case ch <- metrics:
	case <-ctx.Done():
	}
}
