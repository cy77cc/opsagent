package app

// Option configures an Agent.
type Option func(*Agent)

// WithGRPCClient injects a custom GRPCClient (for testing).
func WithGRPCClient(c GRPCClient) Option {
	return func(a *Agent) { a.grpcClient = c }
}

// WithServer injects a custom HTTPServer (for testing).
func WithServer(s HTTPServer) Option {
	return func(a *Agent) { a.server = s }
}

// WithScheduler injects a custom Scheduler (for testing).
func WithScheduler(s Scheduler) Option {
	return func(a *Agent) { a.scheduler = s }
}

// WithPluginRuntime injects a custom PluginRuntime (for testing).
func WithPluginRuntime(r PluginRuntime) Option {
	return func(a *Agent) { a.pluginRuntime = r }
}
