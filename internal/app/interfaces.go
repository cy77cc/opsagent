package app

import (
	"context"

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

// Compile-time interface satisfaction checks.
var (
	_ GRPCClient     = (*grpcclient.Client)(nil)
	_ HTTPServer     = (*server.Server)(nil)
	_ Scheduler      = (*collector.Scheduler)(nil)
	_ PluginRuntime  = (*pluginruntime.Runtime)(nil)
)
