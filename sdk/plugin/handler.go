package plugin

import "context"

// Handler is the interface that plugin authors implement to receive tasks
// from the OpsAgent PluginGateway.
type Handler interface {
	// Init is called once when the plugin starts. cfg may be nil.
	Init(cfg map[string]interface{}) error

	// TaskTypes returns the list of task type strings this handler supports.
	TaskTypes() []string

	// Execute processes a single task request and returns a response.
	Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error)

	// Shutdown is called when the plugin is being terminated gracefully.
	Shutdown(ctx context.Context) error

	// HealthCheck returns nil if the plugin is healthy.
	HealthCheck(ctx context.Context) error
}
