package app

import (
	"context"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/cy77cc/opsagent/internal/server"
)

// GRPCClient abstracts the gRPC client used by the Agent to communicate
// with the platform.
type GRPCClient interface {
	Start(ctx context.Context) error
	Stop()
	FlushAndStop(ctx context.Context, persistPath string) error
	SendMetrics(metrics []*collector.Metric)
	SendExecOutput(taskID, streamName string, data []byte)
	SendExecResult(result *grpcclient.ExecResult)
	IsConnected() bool
}

// HTTPServer abstracts the local HTTP server for health, metrics, and task APIs.
type HTTPServer interface {
	Start() error
	Shutdown(ctx context.Context) error
	SetLatestMetric(metric *collector.MetricPayload)
	LatestMetricExists() bool
}

// Scheduler abstracts the collector pipeline scheduler that runs inputs
// on configured intervals.
type Scheduler interface {
	Start(ctx context.Context) <-chan []*collector.Metric
	Stop()
}

// PluginRuntime abstracts the external plugin runtime process manager.
type PluginRuntime interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ExecuteTask(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error)
}

// PluginGateway manages custom plugin lifecycle and routing.
type PluginGateway interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ExecuteTask(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error)
	ListPlugins() []PluginInfo
	GetPlugin(name string) *PluginInfo
	ReloadPlugin(name string) error
	EnablePlugin(name string) error
	DisablePlugin(name string) error
	OnPluginLoaded(fn func(name string, taskTypes []string))
	OnPluginUnloaded(fn func(name string, taskTypes []string))
}

// PluginInfo is the runtime status of a managed plugin.
type PluginInfo struct {
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Status       PluginStatus  `json:"status"`
	TaskTypes    []string      `json:"task_types"`
	SocketPath   string        `json:"socket_path"`
	RestartCount int           `json:"restart_count"`
	LastHealth   time.Time     `json:"last_health"`
	Uptime       time.Duration `json:"uptime"`
}

// PluginStatus represents the state of a managed plugin.
type PluginStatus string

const (
	PluginStatusStarting PluginStatus = "starting"
	PluginStatusRunning  PluginStatus = "running"
	PluginStatusStopped  PluginStatus = "stopped"
	PluginStatusError    PluginStatus = "error"
	PluginStatusDisabled PluginStatus = "disabled"
)

// Compile-time interface satisfaction checks.
var (
	_ GRPCClient     = (*grpcclient.Client)(nil)
	_ HTTPServer     = (*server.Server)(nil)
	_ Scheduler      = (*collector.Scheduler)(nil)
	_ PluginRuntime  = (*pluginruntime.Runtime)(nil)
)
