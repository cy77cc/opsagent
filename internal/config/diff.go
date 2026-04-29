package config

import (
	"fmt"
	"reflect"
)

// ChangeSet records which reloadable field groups changed.
type ChangeSet struct {
	CollectorChanged  bool
	ReporterChanged   bool
	AuthChanged       bool
	PrometheusChanged bool
}

// NonReloadableChange records a change to a field that requires restart.
type NonReloadableChange struct {
	Field  string
	OldVal interface{}
	NewVal interface{}
}

// Diff compares old and new configs, returning a ChangeSet for reloadable
// fields and a list of non-reloadable changes. The new config is validated first.
func Diff(old, new *Config) (*ChangeSet, []NonReloadableChange, error) {
	if err := new.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid new config: %w", err)
	}

	cs := &ChangeSet{}
	var nonReloadable []NonReloadableChange

	// Reloadable: collector
	if !reflect.DeepEqual(old.Collector, new.Collector) {
		cs.CollectorChanged = true
	}

	// Reloadable: reporter
	if diffReporter(old, new) {
		cs.ReporterChanged = true
	}

	// Reloadable: auth
	if diffAuth(old, new) {
		cs.AuthChanged = true
	}

	// Reloadable: prometheus
	if diffPrometheus(old, new) {
		cs.PrometheusChanged = true
	}

	// Non-reloadable checks
	nonReloadable = append(nonReloadable, diffAgent(old, new)...)
	nonReloadable = append(nonReloadable, diffServer(old, new)...)
	nonReloadable = append(nonReloadable, diffGRPC(old, new)...)
	nonReloadable = append(nonReloadable, diffExecutor(old, new)...)

	if !reflect.DeepEqual(old.Sandbox, new.Sandbox) {
		nonReloadable = append(nonReloadable, NonReloadableChange{
			Field:  "sandbox.*",
			OldVal: old.Sandbox,
			NewVal: new.Sandbox,
		})
	}

	if !reflect.DeepEqual(old.Plugin, new.Plugin) {
		nonReloadable = append(nonReloadable, NonReloadableChange{
			Field:  "plugin.*",
			OldVal: old.Plugin,
			NewVal: new.Plugin,
		})
	}

	return cs, nonReloadable, nil
}

func diffReporter(old, new *Config) bool {
	return old.Reporter != new.Reporter
}

func diffAuth(old, new *Config) bool {
	return old.Auth != new.Auth
}

func diffPrometheus(old, new *Config) bool {
	return old.Prometheus != new.Prometheus
}

func diffAgent(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Agent.ID != new.Agent.ID {
		changes = append(changes, NonReloadableChange{"agent.id", old.Agent.ID, new.Agent.ID})
	}
	if old.Agent.Name != new.Agent.Name {
		changes = append(changes, NonReloadableChange{"agent.name", old.Agent.Name, new.Agent.Name})
	}
	if old.Agent.IntervalSeconds != new.Agent.IntervalSeconds {
		changes = append(changes, NonReloadableChange{"agent.interval_seconds", old.Agent.IntervalSeconds, new.Agent.IntervalSeconds})
	}
	if old.Agent.ShutdownTimeoutSeconds != new.Agent.ShutdownTimeoutSeconds {
		changes = append(changes, NonReloadableChange{"agent.shutdown_timeout_seconds", old.Agent.ShutdownTimeoutSeconds, new.Agent.ShutdownTimeoutSeconds})
	}
	return changes
}

func diffServer(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Server.ListenAddr != new.Server.ListenAddr {
		changes = append(changes, NonReloadableChange{"server.listen_addr", old.Server.ListenAddr, new.Server.ListenAddr})
	}
	return changes
}

func diffGRPC(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.GRPC.ServerAddr != new.GRPC.ServerAddr {
		changes = append(changes, NonReloadableChange{"grpc.server_addr", old.GRPC.ServerAddr, new.GRPC.ServerAddr})
	}
	if old.GRPC.EnrollToken != new.GRPC.EnrollToken {
		changes = append(changes, NonReloadableChange{"grpc.enroll_token", old.GRPC.EnrollToken, new.GRPC.EnrollToken})
	}
	if old.GRPC.MTLS != new.GRPC.MTLS {
		changes = append(changes, NonReloadableChange{"grpc.mtls", old.GRPC.MTLS, new.GRPC.MTLS})
	}
	if old.GRPC.HeartbeatIntervalSeconds != new.GRPC.HeartbeatIntervalSeconds {
		changes = append(changes, NonReloadableChange{"grpc.heartbeat_interval_seconds", old.GRPC.HeartbeatIntervalSeconds, new.GRPC.HeartbeatIntervalSeconds})
	}
	if old.GRPC.ReconnectInitialBackoffMS != new.GRPC.ReconnectInitialBackoffMS {
		changes = append(changes, NonReloadableChange{"grpc.reconnect_initial_backoff_ms", old.GRPC.ReconnectInitialBackoffMS, new.GRPC.ReconnectInitialBackoffMS})
	}
	if old.GRPC.ReconnectMaxBackoffMS != new.GRPC.ReconnectMaxBackoffMS {
		changes = append(changes, NonReloadableChange{"grpc.reconnect_max_backoff_ms", old.GRPC.ReconnectMaxBackoffMS, new.GRPC.ReconnectMaxBackoffMS})
	}
	if old.GRPC.CachePersistPath != new.GRPC.CachePersistPath {
		changes = append(changes, NonReloadableChange{"grpc.cache_persist_path", old.GRPC.CachePersistPath, new.GRPC.CachePersistPath})
	}
	return changes
}

func diffExecutor(old, new *Config) []NonReloadableChange {
	var changes []NonReloadableChange
	if old.Executor.TimeoutSeconds != new.Executor.TimeoutSeconds {
		changes = append(changes, NonReloadableChange{"executor.timeout_seconds", old.Executor.TimeoutSeconds, new.Executor.TimeoutSeconds})
	}
	if !reflect.DeepEqual(old.Executor.AllowedCommands, new.Executor.AllowedCommands) {
		changes = append(changes, NonReloadableChange{"executor.allowed_commands", old.Executor.AllowedCommands, new.Executor.AllowedCommands})
	}
	if old.Executor.MaxOutputBytes != new.Executor.MaxOutputBytes {
		changes = append(changes, NonReloadableChange{"executor.max_output_bytes", old.Executor.MaxOutputBytes, new.Executor.MaxOutputBytes})
	}
	return changes
}
