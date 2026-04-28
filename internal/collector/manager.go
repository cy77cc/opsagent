package collector

import (
	"context"
	"errors"
	"fmt"
)

// Manager orchestrates one or more collectors.
type Manager struct {
	collectors []Collector
}

// NewManager builds a collector manager.
func NewManager(collectors []Collector) *Manager {
	return &Manager{collectors: collectors}
}

// CollectAll runs all registered collectors.
func (m *Manager) CollectAll(ctx context.Context) ([]*MetricPayload, error) {
	results := make([]*MetricPayload, 0, len(m.collectors))
	var collectErr error

	for _, c := range m.collectors {
		payload, err := c.Collect(ctx)
		if err != nil {
			collectErr = errors.Join(collectErr, fmt.Errorf("collector %s: %w", c.Name(), err))
			continue
		}
		results = append(results, payload)
	}

	if len(results) == 0 && collectErr != nil {
		return nil, collectErr
	}
	return results, collectErr
}
