package task

import (
	"context"
	"fmt"
	"sync"
)

// Handler processes a specific task type.
type Handler func(ctx context.Context, task AgentTask) (any, error)

// Dispatcher routes tasks to registered handlers.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewDispatcher creates a task dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

// Register binds a task type to a handler.
func (d *Dispatcher) Register(taskType string, handler Handler) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[taskType] = handler
}

// Unregister removes a task type handler.
func (d *Dispatcher) Unregister(taskType string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.handlers, taskType)
}

// Dispatch resolves the task handler and executes it.
func (d *Dispatcher) Dispatch(ctx context.Context, task AgentTask) (any, error) {
	d.mu.RLock()
	h, ok := d.handlers[task.Type]
	d.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported task type: %s", task.Type)
	}
	return h(ctx, task)
}
