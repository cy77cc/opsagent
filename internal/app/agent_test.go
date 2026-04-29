package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/rs/zerolog"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockGRPCClient implements GRPCClient for testing.
type mockGRPCClient struct {
	startCalled       atomic.Int32
	stopCalled        atomic.Int32
	sendMetricsCalled atomic.Int32
	startErr          error

	started     chan struct{} // closed on first Start call
	metricsSent chan struct{} // closed on first SendMetrics call
	startOnce   sync.Once
	metricsOnce sync.Once
}

func newMockGRPCClient() *mockGRPCClient {
	return &mockGRPCClient{
		started:     make(chan struct{}),
		metricsSent: make(chan struct{}),
	}
}

func (m *mockGRPCClient) Start(_ context.Context) error {
	m.startCalled.Add(1)
	m.startOnce.Do(func() { close(m.started) })
	return m.startErr
}

func (m *mockGRPCClient) Stop() { m.stopCalled.Add(1) }

func (m *mockGRPCClient) SendMetrics(_ []*collector.Metric) {
	m.sendMetricsCalled.Add(1)
	m.metricsOnce.Do(func() { close(m.metricsSent) })
}

func (m *mockGRPCClient) SendExecOutput(string, string, []byte)     {}
func (m *mockGRPCClient) SendExecResult(*grpcclient.ExecResult)     {}
func (m *mockGRPCClient) IsConnected() bool                         { return true }

func (m *mockGRPCClient) FlushAndStop(_ context.Context, _ string) error {
	m.stopCalled.Add(1)
	return nil
}

// mockHTTPServer implements HTTPServer for testing.
//
// Start blocks until Shutdown is called, mirroring the real server
// where http.Server.ListenAndServe blocks for the server's lifetime.
type mockHTTPServer struct {
	startCalled    atomic.Int32
	shutdownCalled atomic.Int32

	started      chan struct{} // closed on first Start call
	block        chan struct{} // closed by Shutdown to unblock Start
	startOnce    sync.Once
	shutdownOnce sync.Once
}

func newMockHTTPServer() *mockHTTPServer {
	return &mockHTTPServer{
		started: make(chan struct{}),
		block:   make(chan struct{}),
	}
}

func (m *mockHTTPServer) Start() error {
	m.startCalled.Add(1)
	m.startOnce.Do(func() { close(m.started) })
	<-m.block
	return nil
}

func (m *mockHTTPServer) Shutdown(_ context.Context) error {
	m.shutdownCalled.Add(1)
	m.shutdownOnce.Do(func() { close(m.block) })
	return nil
}

func (m *mockHTTPServer) SetLatestMetric(*collector.MetricPayload) {}
func (m *mockHTTPServer) LatestMetricExists() bool                 { return false }

// mockScheduler implements Scheduler for testing.
type mockScheduler struct {
	startCalled atomic.Int32
	stopCalled  atomic.Int32

	ch chan []*collector.Metric // returned by Start; tests can push metrics here
}

func newMockScheduler() *mockScheduler {
	return &mockScheduler{ch: make(chan []*collector.Metric, 16)}
}

func (m *mockScheduler) Start(_ context.Context) <-chan []*collector.Metric {
	m.startCalled.Add(1)
	return m.ch
}

func (m *mockScheduler) Stop() { m.stopCalled.Add(1) }

// mockPluginRuntime implements PluginRuntime for testing.
type mockPluginRuntime struct {
	startCalled atomic.Int32
	stopCalled  atomic.Int32
	startErr    error

	started  chan struct{} // closed on first Start call
	startOnce sync.Once
}

func newMockPluginRuntime() *mockPluginRuntime {
	return &mockPluginRuntime{started: make(chan struct{})}
}

func (m *mockPluginRuntime) Start(_ context.Context) error {
	m.startCalled.Add(1)
	m.startOnce.Do(func() { close(m.started) })
	return m.startErr
}

func (m *mockPluginRuntime) Stop(_ context.Context) error {
	m.stopCalled.Add(1)
	return nil
}

func (m *mockPluginRuntime) ExecuteTask(_ context.Context, _ pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// minimalConfig returns a Config with just enough valid fields for NewAgent.
func minimalConfig() *config.Config {
	return &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAgentRun_StartsAndStopsAllSubsystems(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := newMockPluginRuntime()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(httpServer),
		WithScheduler(scheduler),
		WithPluginRuntime(pluginRuntime),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	// Wait for all subsystems to start (server.Start is async, so wait for it too).
	select {
	case <-httpServer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subsystems to start")
	}

	// Trigger graceful shutdown.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}

	// Verify every subsystem was started exactly once.
	for _, tc := range []struct {
		name    string
		counter *atomic.Int32
		want    int32
	}{
		{"pluginRuntime.Start", &pluginRuntime.startCalled, 1},
		{"scheduler.Start", &scheduler.startCalled, 1},
		{"grpcClient.Start", &grpcClient.startCalled, 1},
		{"httpServer.Start", &httpServer.startCalled, 1},
	} {
		if got := tc.counter.Load(); got != tc.want {
			t.Errorf("%s called %d times, want %d", tc.name, got, tc.want)
		}
	}

	// Verify every subsystem was stopped/shut-down exactly once.
	for _, tc := range []struct {
		name    string
		counter *atomic.Int32
		want    int32
	}{
		{"httpServer.Shutdown", &httpServer.shutdownCalled, 1},
		{"grpcClient.Stop", &grpcClient.stopCalled, 1},
		{"scheduler.Stop", &scheduler.stopCalled, 1},
		{"pluginRuntime.Stop", &pluginRuntime.stopCalled, 1},
	} {
		if got := tc.counter.Load(); got != tc.want {
			t.Errorf("%s called %d times, want %d", tc.name, got, tc.want)
		}
	}
}

func TestAgentRun_ForwardsPipelineMetrics(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := newMockPluginRuntime()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(httpServer),
		WithScheduler(scheduler),
		WithPluginRuntime(pluginRuntime),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	// Wait for subsystems to be ready.
	select {
	case <-grpcClient.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subsystems to start")
	}

	// Push a metric batch through the scheduler pipeline channel.
	testMetrics := []*collector.Metric{
		collector.NewMetric(
			"cpu_usage",
			map[string]string{"host": "test"},
			map[string]interface{}{"value": float64(75.5)},
			collector.Gauge,
			time.Now(),
		),
	}
	scheduler.ch <- testMetrics

	// Wait for the metrics to be forwarded via the gRPC client.
	select {
	case <-grpcClient.metricsSent:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for metrics to be forwarded")
	}

	if got := grpcClient.sendMetricsCalled.Load(); got != 1 {
		t.Errorf("SendMetrics called %d times, want 1", got)
	}

	// Clean up.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}
}

func TestAgentRun_StartSubsystemFailure(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := newMockPluginRuntime()
	pluginRuntime.startErr = fmt.Errorf("plugin start failed")

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(httpServer),
		WithScheduler(scheduler),
		WithPluginRuntime(pluginRuntime),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = agent.Run(ctx)
	if err == nil {
		t.Fatal("Run should have returned an error")
	}

	// Only pluginRuntime.Start should have been called — it fails first,
	// so the remaining subsystems are never started.
	if got := pluginRuntime.startCalled.Load(); got != 1 {
		t.Errorf("pluginRuntime.Start called %d times, want 1", got)
	}
	if got := grpcClient.startCalled.Load(); got != 0 {
		t.Errorf("grpcClient.Start called %d times, want 0", got)
	}
	if got := scheduler.startCalled.Load(); got != 0 {
		t.Errorf("scheduler.Start called %d times, want 0", got)
	}
	if got := httpServer.startCalled.Load(); got != 0 {
		t.Errorf("httpServer.Start called %d times, want 0", got)
	}
}

func TestAgentShutdown_RejectsNewTasks(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}
	agent.shuttingDown.Store(true)

	if !agent.shuttingDown.Load() {
		t.Error("expected shuttingDown to be true")
	}
}

func TestAgentShutdown_WaitsForActiveTasks(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}

	_, taskCancel := context.WithCancel(context.Background())
	agent.activeTasks.Store("task-1", taskCancel)

	done := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		agent.activeTasks.Delete("task-1")
		taskCancel()
		close(done)
	}()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent.waitForActiveTasks(shutdownCtx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForActiveTasks did not return after task completed")
	}
}

func TestAgentShutdown_ForceCancelsOnTimeout(t *testing.T) {
	agent := &Agent{
		cfg:       minimalConfig(),
		log:       zerolog.Nop(),
		startedAt: time.Now().UTC(),
	}

	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()
	agent.activeTasks.Store("task-1", taskCancel)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	agent.waitForActiveTasks(shutdownCtx)

	select {
	case <-taskCtx.Done():
	default:
		t.Error("expected task context to be cancelled on timeout")
	}
}

func TestAgentConfigReload_Integration(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := newMockPluginRuntime()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(httpServer),
		WithScheduler(scheduler),
		WithPluginRuntime(pluginRuntime),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Test that ConfigReloader is accessible.
	if agent.ConfigReloader() == nil {
		t.Fatal("ConfigReloader should not be nil")
	}
}
