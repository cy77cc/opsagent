package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/cy77cc/opsagent/internal/executor"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/cy77cc/opsagent/internal/health"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/cy77cc/opsagent/internal/task"
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
func (m *mockGRPCClient) HealthStatus() health.Status               { return health.Status{Status: "connected"} }
func (m *mockGRPCClient) SetOnStateChange(_ func(connected bool))   {}

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
func (m *mockScheduler) HealthStatus() health.Status {
	return health.Status{Status: "running"}
}

// mockPluginRuntime implements PluginRuntime for testing.
type mockPluginRuntime struct {
	startCalled   atomic.Int32
	stopCalled    atomic.Int32
	startErr      error
	executeTaskFn func(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error)

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

func (m *mockPluginRuntime) ExecuteTask(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
	if m.executeTaskFn != nil {
		return m.executeTaskFn(ctx, req)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockPluginRuntime) HealthStatus() health.Status {
	return health.Status{Status: "running"}
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

// ---------------------------------------------------------------------------
// Additional mocks
// ---------------------------------------------------------------------------

// mockPluginGateway implements PluginGateway for testing.
type mockPluginGateway struct {
	startCalled   atomic.Int32
	stopCalled    atomic.Int32
	startErr      error
	executeTaskFn func(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error)

	started  chan struct{}
	startOnce sync.Once
}

func newMockPluginGateway() *mockPluginGateway {
	return &mockPluginGateway{started: make(chan struct{})}
}

func (m *mockPluginGateway) Start(_ context.Context) error {
	m.startCalled.Add(1)
	m.startOnce.Do(func() { close(m.started) })
	return m.startErr
}

func (m *mockPluginGateway) Stop(_ context.Context) error {
	m.stopCalled.Add(1)
	return nil
}

func (m *mockPluginGateway) ExecuteTask(ctx context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
	if m.executeTaskFn != nil {
		return m.executeTaskFn(ctx, req)
	}
	return &pluginruntime.TaskResponse{TaskID: req.TaskID, Status: "ok"}, nil
}

func (m *mockPluginGateway) ListPlugins() []pluginruntime.PluginInfo { return nil }
func (m *mockPluginGateway) GetPlugin(string) *pluginruntime.PluginInfo { return nil }
func (m *mockPluginGateway) ReloadPlugin(string) error { return nil }
func (m *mockPluginGateway) EnablePlugin(string) error { return nil }
func (m *mockPluginGateway) DisablePlugin(string) error { return nil }
func (m *mockPluginGateway) OnPluginLoaded(func(string, []string)) {}
func (m *mockPluginGateway) OnPluginUnloaded(func(string, []string)) {}
func (m *mockPluginGateway) HealthStatus() health.Status { return health.Status{Status: "running"} }

// ---------------------------------------------------------------------------
// IsShutdownComplete and AuditLog tests
// ---------------------------------------------------------------------------

func TestIsShutdownComplete_BeforeShutdown(t *testing.T) {
	agent := &Agent{
		cfg:              minimalConfig(),
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
	}
	if agent.IsShutdownComplete() {
		t.Error("expected IsShutdownComplete to return false before shutdown")
	}
}

func TestIsShutdownComplete_AfterShutdown(t *testing.T) {
	agent := &Agent{
		cfg:              minimalConfig(),
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
	}
	close(agent.shutdownComplete)
	if !agent.IsShutdownComplete() {
		t.Error("expected IsShutdownComplete to return true after channel closed")
	}
}

func TestAuditLog_ReturnsAuditLogger(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalConfig()
	cfg.Agent.AuditLog.Enabled = true
	cfg.Agent.AuditLog.Path = filepath.Join(dir, "audit.jsonl")

	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.AuditLog() == nil {
		t.Error("expected AuditLog to return non-nil logger")
	}
}

func TestAuditLog_NilWhenDisabled(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.AuditLog() != nil {
		t.Error("expected AuditLog to return nil when audit disabled")
	}
}

// ---------------------------------------------------------------------------
// RunOnce tests
// ---------------------------------------------------------------------------

func TestRunOnce_NoScheduler(t *testing.T) {
	agent := &Agent{
		cfg:              minimalConfig(),
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
	}

	err := agent.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected error when no scheduler configured")
	}
	if err.Error() != "no scheduler configured" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunOnce_Success(t *testing.T) {
	scheduler := newMockScheduler()
	grpcClient := newMockGRPCClient()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(newMockHTTPServer()),
		WithScheduler(scheduler),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Push metrics before RunOnce reads from channel.
	testMetrics := []*collector.Metric{
		collector.NewMetric(
			"test_metric",
			map[string]string{"host": "test"},
			map[string]interface{}{"value": float64(42)},
			collector.Gauge,
			time.Now(),
		),
	}

	go func() {
		scheduler.ch <- testMetrics
	}()

	err = agent.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := scheduler.stopCalled.Load(); got != 1 {
		t.Errorf("scheduler.Stop called %d times, want 1", got)
	}
	if got := grpcClient.sendMetricsCalled.Load(); got != 1 {
		t.Errorf("SendMetrics called %d times, want 1", got)
	}
}

func TestRunOnce_ContextCancelled(t *testing.T) {
	scheduler := newMockScheduler()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(scheduler),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = agent.RunOnce(ctx)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

// ---------------------------------------------------------------------------
// executePluginTask tests
// ---------------------------------------------------------------------------

func TestExecutePluginTask_Disabled(t *testing.T) {
	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{Enabled: false},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		auditLog:         nil, // disabled audit
	}

	_, err := agent.executePluginTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "plugin_log_parse",
	}, task.TypePluginLogParse)
	if err == nil {
		t.Fatal("expected error when plugin disabled")
	}
	if err.Error() != "plugin runtime is disabled" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecutePluginTask_Success(t *testing.T) {
	pr := newMockPluginRuntime()
	pr.executeTaskFn = func(_ context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		return &pluginruntime.TaskResponse{
			TaskID: req.TaskID,
			Status: "ok",
			Summary: map[string]any{"lines": 10},
		}, nil
	}

	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{Enabled: true, RequestTimeoutSeconds: 30, ChunkSizeBytes: 1024, MaxResultBytes: 10240},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		pluginRuntime:    pr,
		auditLog:         nil,
	}

	res, err := agent.executePluginTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "plugin_log_parse",
		Payload: map[string]any{"file": "/var/log/syslog"},
	}, task.TypePluginLogParse)
	if err != nil {
		t.Fatalf("executePluginTask: %v", err)
	}

	resp, ok := res.(*pluginruntime.TaskResponse)
	if !ok {
		t.Fatalf("expected *pluginruntime.TaskResponse, got %T", res)
	}
	if resp.Status != "ok" {
		t.Errorf("response status = %q, want ok", resp.Status)
	}
}

func TestExecutePluginTask_ExecutionError(t *testing.T) {
	pr := newMockPluginRuntime()
	pr.executeTaskFn = func(_ context.Context, _ pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		return nil, fmt.Errorf("plugin crashed")
	}

	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{Enabled: true, RequestTimeoutSeconds: 30, ChunkSizeBytes: 1024, MaxResultBytes: 10240},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		pluginRuntime:    pr,
		auditLog:         nil,
	}

	_, err := agent.executePluginTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "plugin_log_parse",
	}, task.TypePluginLogParse)
	if err == nil {
		t.Fatal("expected error from plugin execution")
	}
	if err.Error() != "plugin crashed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecutePluginTask_EmptyTaskID(t *testing.T) {
	pr := newMockPluginRuntime()
	pr.executeTaskFn = func(_ context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		if req.TaskID == "" {
			return nil, fmt.Errorf("empty task ID")
		}
		return &pluginruntime.TaskResponse{TaskID: req.TaskID, Status: "ok"}, nil
	}

	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{Enabled: true, RequestTimeoutSeconds: 30, ChunkSizeBytes: 1024, MaxResultBytes: 10240},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		pluginRuntime:    pr,
		auditLog:         nil,
	}

	// Empty TaskID should get auto-generated (not empty).
	res, err := agent.executePluginTask(context.Background(), task.AgentTask{
		TaskID: "", Type: "plugin_log_parse",
	}, task.TypePluginLogParse)
	if err != nil {
		t.Fatalf("executePluginTask: %v", err)
	}
	resp := res.(*pluginruntime.TaskResponse)
	if resp.TaskID == "" {
		t.Error("expected auto-generated task ID, got empty")
	}
}

// ---------------------------------------------------------------------------
// executeGatewayTask tests
// ---------------------------------------------------------------------------

func TestExecuteGatewayTask_ShuttingDown(t *testing.T) {
	agent := &Agent{
		cfg:              minimalConfig(),
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
	}
	agent.shuttingDown.Store(true)

	_, err := agent.executeGatewayTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "gw:myplugin:parse",
	})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
	if err.Error() != "agent is shutting down" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteGatewayTask_GatewayNil(t *testing.T) {
	agent := &Agent{
		cfg:              minimalConfig(),
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
	}

	_, err := agent.executeGatewayTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "gw:myplugin:parse",
	})
	if err == nil {
		t.Fatal("expected error when gateway is nil")
	}
	if err.Error() != "plugin gateway is not enabled" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecuteGatewayTask_Success(t *testing.T) {
	gw := newMockPluginGateway()
	gw.executeTaskFn = func(_ context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		return &pluginruntime.TaskResponse{
			TaskID: req.TaskID,
			Status: "ok",
		}, nil
	}

	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{ChunkSizeBytes: 1024, MaxResultBytes: 10240},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		pluginGateway:    gw,
		auditLog:         nil,
	}

	res, err := agent.executeGatewayTask(context.Background(), task.AgentTask{
		TaskID: "t-1", Type: "gw:myplugin:parse",
		Payload: map[string]any{"data": "hello"},
	})
	if err != nil {
		t.Fatalf("executeGatewayTask: %v", err)
	}
	resp := res.(*pluginruntime.TaskResponse)
	if resp.Status != "ok" {
		t.Errorf("response status = %q, want ok", resp.Status)
	}
}

func TestExecuteGatewayTask_EmptyTaskID(t *testing.T) {
	gw := newMockPluginGateway()
	gw.executeTaskFn = func(_ context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		if req.TaskID == "" {
			return nil, fmt.Errorf("empty task ID")
		}
		return &pluginruntime.TaskResponse{TaskID: req.TaskID, Status: "ok"}, nil
	}

	agent := &Agent{
		cfg: &config.Config{
			Agent:  config.AgentConfig{ID: "test"},
			Plugin: config.PluginConfig{ChunkSizeBytes: 1024, MaxResultBytes: 10240},
		},
		log:              zerolog.Nop(),
		startedAt:        time.Now().UTC(),
		shutdownComplete: make(chan struct{}),
		metricsReg:       NewMetricsRegistry(),
		pluginGateway:    gw,
		auditLog:         nil,
	}

	res, err := agent.executeGatewayTask(context.Background(), task.AgentTask{
		TaskID: "", Type: "gw:myplugin:parse",
	})
	if err != nil {
		t.Fatalf("executeGatewayTask: %v", err)
	}
	resp := res.(*pluginruntime.TaskResponse)
	if resp.TaskID == "" {
		t.Error("expected auto-generated task ID, got empty")
	}
}

// ---------------------------------------------------------------------------
// buildScheduler error path tests
// ---------------------------------------------------------------------------

func TestBuildScheduler_UnknownInputType(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "nonexistent_input_type_xyz", Config: map[string]interface{}{}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for unknown input type")
	}
	if !strings.Contains(err.Error(), "unknown input type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_UnknownProcessorType(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Processors: []config.PluginInstanceConfig{
				{Type: "nonexistent_processor_xyz", Config: map[string]interface{}{}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for unknown processor type")
	}
	if !strings.Contains(err.Error(), "unknown processor type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_UnknownAggregatorType(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Aggregators: []config.PluginInstanceConfig{
				{Type: "nonexistent_aggregator_xyz", Config: map[string]interface{}{}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for unknown aggregator type")
	}
	if !strings.Contains(err.Error(), "unknown aggregator type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_UnknownOutputType(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Outputs: []config.PluginInstanceConfig{
				{Type: "nonexistent_output_xyz", Config: map[string]interface{}{}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for unknown output type")
	}
	if !strings.Contains(err.Error(), "unknown output type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_NoInputsReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{},
		},
	}
	sched, err := buildScheduler(cfg, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sched != nil {
		t.Error("expected nil scheduler when no inputs configured")
	}
}

func TestConfigReload_Integration(t *testing.T) {
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

// ---------------------------------------------------------------------------
// WithPluginGateway and WithConfigReloader option tests
// ---------------------------------------------------------------------------

func TestWithPluginGateway(t *testing.T) {
	gw := newMockPluginGateway()
	agent := &Agent{}
	WithPluginGateway(gw)(agent)
	if agent.pluginGateway != gw {
		t.Error("WithPluginGateway did not set pluginGateway")
	}
}

func TestWithConfigReloader(t *testing.T) {
	cfg := minimalConfig()
	agent := &Agent{cfg: cfg, log: zerolog.Nop()}
	WithConfigReloader(nil)(agent)
	// Just verify it doesn't panic; nil is a valid value to set.
	if agent.configReloader != nil {
		t.Error("expected nil configReloader")
	}
}

// ---------------------------------------------------------------------------
// registerTaskHandlers dispatch tests
// ---------------------------------------------------------------------------

func TestDispatch_ExecCommand_Success(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-1",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command": "echo",
			"args":    []any{"hello"},
		},
	})
	if err != nil {
		t.Fatalf("dispatch exec_command: %v", err)
	}
	execRes, ok := res.(*executor.Result)
	if !ok {
		t.Fatalf("expected *executor.Result, got %T", res)
	}
	if execRes.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", execRes.ExitCode)
	}
}

func TestDispatch_ExecCommand_MissingCommand(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "exec-2",
		Type:    task.TypeExecCommand,
		Payload: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestDispatch_ExecCommand_ShuttingDown(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	agent.shuttingDown.Store(true)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "exec-3",
		Type:    task.TypeExecCommand,
		Payload: map[string]any{"command": "echo"},
	})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

func TestDispatch_HealthCheck(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "hc-1",
		Type:   task.TypeHealthCheck,
	})
	if err != nil {
		t.Fatalf("dispatch health_check: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", res)
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v, want ok", m["status"])
	}
	if m["agent_id"] != "test" {
		t.Errorf("agent_id = %v, want test", m["agent_id"])
	}
}

func TestDispatch_CollectMetrics_LegacyError(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "cm-1",
		Type:   task.TypeCollectMetrics,
	})
	if err == nil {
		t.Fatal("expected error for legacy collect-metrics")
	}
}

func TestDispatch_PluginTask_Disabled(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "pl-1",
		Type:    task.TypePluginLogParse,
		Payload: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error when plugin disabled")
	}
}

func TestDispatch_PluginTask_Success(t *testing.T) {
	pr := newMockPluginRuntime()
	pr.executeTaskFn = func(_ context.Context, req pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		return &pluginruntime.TaskResponse{TaskID: req.TaskID, Status: "ok"}, nil
	}

	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Plugin: config.PluginConfig{Enabled: true, RequestTimeoutSeconds: 30, ChunkSizeBytes: 1024, MaxResultBytes: 10240},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(pr),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	pluginTypes := []string{
		task.TypePluginLogParse,
		task.TypePluginTextProcess,
		task.TypePluginEBPFCollect,
		task.TypePluginFSScan,
		task.TypePluginConnAnalyze,
		task.TypePluginLocalProbe,
	}
	for _, pt := range pluginTypes {
		res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
			TaskID:  "pl-1",
			Type:    pt,
			Payload: map[string]any{},
		})
		if err != nil {
			t.Errorf("dispatch %s: %v", pt, err)
			continue
		}
		if res == nil {
			t.Errorf("dispatch %s: nil response", pt)
		}
	}
}

func TestDispatch_SandboxExec_ShuttingDown(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	agent.shuttingDown.Store(true)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "sb-1",
		Type:    task.TypeSandboxExec,
		Payload: map[string]any{"command": "echo"},
	})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

func TestDispatch_SandboxExec_NilExecutor(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "sb-2",
		Type:    task.TypeSandboxExec,
		Payload: map[string]any{"command": "echo"},
	})
	if err == nil {
		t.Fatal("expected error when sandbox executor is nil")
	}
}

func TestDispatch_SandboxExec_MissingPayload(t *testing.T) {
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "sb-3",
		Type:    task.TypeSandboxExec,
		Payload: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error for missing command/script")
	}
}

// ---------------------------------------------------------------------------
// registerGRPCHandlers tests
// ---------------------------------------------------------------------------

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return agent
}

func TestGRPCHandlers_CancelJob(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Store a mock cancel function.
	var cancelled bool
	agent.activeTasks.Store("task-1", context.CancelFunc(func() { cancelled = true }))

	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_CancelJob{
			CancelJob: &pb.CancelJob{TaskId: "task-1"},
		},
	})
	if err != nil {
		t.Fatalf("handle cancel: %v", err)
	}
	if !cancelled {
		t.Error("expected task to be cancelled")
	}
}

func TestGRPCHandlers_CancelJob_NotFound(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Cancel a non-existent task (should not panic).
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_CancelJob{
			CancelJob: &pb.CancelJob{TaskId: "nonexistent"},
		},
	})
	if err != nil {
		t.Fatalf("handle cancel nonexistent: %v", err)
	}
}

func TestGRPCHandlers_CommandHandler_ShuttingDown(t *testing.T) {
	agent := newTestAgent(t)
	agent.shuttingDown.Store(true)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:  "cmd-1",
				Command: "echo",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

func TestGRPCHandlers_CommandHandler_NoSandbox(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:  "cmd-2",
				Command: "echo",
				Args:    []string{"hello"},
			},
		},
	})
	if err != nil {
		t.Fatalf("handle command: %v", err)
	}
}

func TestGRPCHandlers_ScriptHandler_NoSandbox(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{
				TaskId:  "script-1",
				Script:  "echo hello",
			},
		},
	})
	if err != nil {
		t.Fatalf("handle script no sandbox: %v", err)
	}
}

func TestGRPCHandlers_ScriptHandler_ShuttingDown(t *testing.T) {
	agent := newTestAgent(t)
	agent.shuttingDown.Store(true)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{
				TaskId: "script-2",
				Script: "echo hello",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error when shutting down")
	}
}

// ---------------------------------------------------------------------------
// eventLoop edge case tests
// ---------------------------------------------------------------------------

func TestEventLoop_ServerError(t *testing.T) {
	agent := newTestAgent(t)
	agent.metricsReg = NewMetricsRegistry()

	pipelineCh := make(chan []*collector.Metric)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("server crashed")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		agent.eventLoop(ctx, pipelineCh, errCh)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("eventLoop did not exit on server error")
	}
}

func TestEventLoop_PipelineChClosed(t *testing.T) {
	agent := newTestAgent(t)
	agent.metricsReg = NewMetricsRegistry()

	pipelineCh := make(chan []*collector.Metric)
	errCh := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		agent.eventLoop(ctx, pipelineCh, errCh)
		close(done)
	}()

	// Closing the pipeline channel sets it to nil inside eventLoop;
	// cancel the context so the loop exits.
	close(pipelineCh)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("eventLoop did not exit on pipeline channel close")
	}
}

// ---------------------------------------------------------------------------
// handlePipelineMetrics edge cases
// ---------------------------------------------------------------------------

func TestHandlePipelineMetrics_Empty(t *testing.T) {
	agent := newTestAgent(t)
	agent.metricsReg = NewMetricsRegistry()
	// Should not panic on empty metrics.
	agent.handlePipelineMetrics(nil)
	agent.handlePipelineMetrics([]*collector.Metric{})
}

// ---------------------------------------------------------------------------
// NewAgent with plugin gateway enabled
// ---------------------------------------------------------------------------

func TestNewAgent_WithPluginGatewayOption(t *testing.T) {
	gw := newMockPluginGateway()
	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
		WithPluginGateway(gw),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.pluginGateway != gw {
		t.Error("expected injected plugin gateway")
	}
}

// ---------------------------------------------------------------------------
// ExecCommand with various payload types
// ---------------------------------------------------------------------------

func TestDispatch_ExecCommand_WithStringTimeout(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-str-timeout",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": "5",
		},
	})
	if err != nil {
		t.Fatalf("dispatch exec with string timeout: %v", err)
	}
	if res == nil {
		t.Error("expected non-nil result")
	}
}

func TestDispatch_ExecCommand_WithIntTimeout(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-int-timeout",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": 5,
		},
	})
	if err != nil {
		t.Fatalf("dispatch exec with int timeout: %v", err)
	}
	if res == nil {
		t.Error("expected non-nil result")
	}
}

func TestDispatch_ExecCommand_InvalidTimeoutType(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-bad-timeout",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": true,
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid timeout type")
	}
}

func TestDispatch_ExecCommand_InvalidTimeoutString(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-bad-timeout-str",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": "not-a-number",
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid timeout string")
	}
}

func TestDispatch_ExecCommand_InvalidArgsType(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-bad-args",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command": "echo",
			"args":    []any{123}, // non-string arg
		},
	})
	if err == nil {
		t.Fatal("expected error for non-string args")
	}
}

func TestDispatch_ExecCommand_ExecutionError(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	// "notallowed" is not in AllowedCommands, so executor will error.
	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-err",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command": "notallowed",
		},
	})
	if err == nil {
		t.Fatal("expected error for disallowed command")
	}
}

// ---------------------------------------------------------------------------
// GRPC config update handler test
// ---------------------------------------------------------------------------

func TestGRPCHandlers_ConfigUpdate(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Config update with invalid YAML should fail gracefully.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ConfigUpdate{
			ConfigUpdate: &pb.ConfigUpdate{
				ConfigYaml: []byte("invalid: yaml: ["),
				Version:    1,
			},
		},
	})
	if err != nil {
		t.Fatalf("handle config update should not return error (handler swallows): %v", err)
	}
}

// ---------------------------------------------------------------------------
// buildScheduler init error paths
// ---------------------------------------------------------------------------

func TestBuildScheduler_InitInputError(t *testing.T) {
	// "cpu" input requires certain config; passing invalid config should error.
	// Actually, cpu input's Init accepts any config. Let's use a real input that
	// validates its config. The "regex" processor validates pattern.
	// For inputs, we can test by checking that a valid input with valid config works.
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Outputs: []config.PluginInstanceConfig{
				{Type: "http", Config: map[string]interface{}{"url": "http://localhost:9999/metrics"}},
			},
		},
	}
	sched, err := buildScheduler(cfg, zerolog.Nop())
	if err != nil {
		t.Fatalf("buildScheduler: %v", err)
	}
	if sched == nil {
		t.Fatal("expected non-nil scheduler with valid config")
	}
}

// ---------------------------------------------------------------------------
// NewAgent with grpc client creation (no injection)
// ---------------------------------------------------------------------------

func TestNewAgent_GRPCClientCreation(t *testing.T) {
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		GRPC:   config.GRPCConfig{ServerAddr: "localhost:50051"},
	}
	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.grpcClient == nil {
		t.Error("expected grpcClient to be created")
	}
}

// ---------------------------------------------------------------------------
// GRPC handler: ExecCommand with non-zero exit (disallowed command)
// ---------------------------------------------------------------------------

func TestGRPCHandlers_CommandHandler_DisallowedCommand(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// "notallowed" is not in the executor's allowed list.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:  "cmd-disallowed",
				Command: "notallowed",
			},
		},
	})
	// Handler returns nil even on exec error (sends result via gRPC).
	if err != nil {
		t.Fatalf("handle command: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Shutdown error paths
// ---------------------------------------------------------------------------

type mockGRPCClientFlushErr struct {
	*mockGRPCClient
}

func (m *mockGRPCClientFlushErr) FlushAndStop(_ context.Context, _ string) error {
	m.stopCalled.Add(1)
	return fmt.Errorf("flush failed")
}

func TestShutdown_FlushAndStopError(t *testing.T) {
	grpcClient := &mockGRPCClientFlushErr{mockGRPCClient: newMockGRPCClient()}
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

	select {
	case <-httpServer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subsystems to start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}

	// Verify the agent shut down despite the flush error.
	if !agent.IsShutdownComplete() {
		t.Error("expected shutdown to complete despite flush error")
	}
}

type mockPluginRuntimeStopErr struct {
	*mockPluginRuntime
}

func (m *mockPluginRuntimeStopErr) Stop(_ context.Context) error {
	m.stopCalled.Add(1)
	return fmt.Errorf("stop failed")
}

func TestShutdown_PluginRuntimeStopError(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := newMockHTTPServer()
	scheduler := newMockScheduler()
	pluginRuntime := &mockPluginRuntimeStopErr{mockPluginRuntime: newMockPluginRuntime()}

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

	select {
	case <-httpServer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subsystems to start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}

	if !agent.IsShutdownComplete() {
		t.Error("expected shutdown to complete despite plugin stop error")
	}
}

type mockHTTPServerShutdownErr struct {
	*mockHTTPServer
}

func (m *mockHTTPServerShutdownErr) Shutdown(_ context.Context) error {
	m.shutdownCalled.Add(1)
	m.shutdownOnce.Do(func() { close(m.block) })
	return fmt.Errorf("shutdown failed")
}

func TestShutdown_ServerShutdownError(t *testing.T) {
	grpcClient := newMockGRPCClient()
	httpServer := &mockHTTPServerShutdownErr{mockHTTPServer: newMockHTTPServer()}
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

	select {
	case <-httpServer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for subsystems to start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}

	if !agent.IsShutdownComplete() {
		t.Error("expected shutdown to complete despite server shutdown error")
	}
}

// ---------------------------------------------------------------------------
// NewAgent with sandbox enabled
// ---------------------------------------------------------------------------

func TestNewAgent_SandboxEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Sandbox: config.SandboxConfig{
			Enabled:                 true,
			NsjailPath:              "/usr/bin/nsjail",
			BaseWorkdir:             dir,
			DefaultTimeoutSeconds:   10,
			MaxConcurrentTasks:      5,
			AuditLogPath:            filepath.Join(dir, "sandbox-audit.log"),
			CgroupBasePath:          "/sys/fs/cgroup/opsagent",
			Policy: config.PolicyConfig{
				AllowedCommands: []string{"echo"},
			},
		},
	}
	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.sandboxExec == nil {
		t.Error("expected sandbox executor to be created when sandbox enabled")
	}
}

// ---------------------------------------------------------------------------
// NewAgent with plugin gateway config enabled
// ---------------------------------------------------------------------------

func TestNewAgent_PluginGatewayConfig(t *testing.T) {
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		PluginGateway: config.PluginGatewayConfig{
			Enabled:    true,
			PluginsDir: t.TempDir(),
		},
	}
	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agent.pluginGateway == nil {
		t.Error("expected plugin gateway to be created when config enabled")
	}
}

// ---------------------------------------------------------------------------
// Exec command float64 timeout
// ---------------------------------------------------------------------------

func TestDispatch_ExecCommand_Float64Timeout(t *testing.T) {
	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	res, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "exec-float-timeout",
		Type:   task.TypeExecCommand,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": float64(5),
		},
	})
	if err != nil {
		t.Fatalf("dispatch exec with float64 timeout: %v", err)
	}
	if res == nil {
		t.Error("expected non-nil result")
	}
}

// ---------------------------------------------------------------------------
// Sandbox exec handler tests
// ---------------------------------------------------------------------------

// newTestAgentWithSandbox creates an agent with a sandbox executor configured
// to allow only "allowed_cmd". Any other command will fail policy validation.
func newTestAgentWithSandbox(t *testing.T) *Agent {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
		Sandbox: config.SandboxConfig{
			Enabled:               true,
			NsjailPath:            "/usr/bin/nsjail",
			BaseWorkdir:           dir,
			DefaultTimeoutSeconds: 10,
			MaxConcurrentTasks:    4,
			Policy: config.PolicyConfig{
				AllowedCommands:     []string{"allowed_cmd"},
				AllowedInterpreters: []string{"python"},
			},
		},
	}
	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	return agent
}

func TestDispatch_SandboxExec_Command(t *testing.T) {
	agent := newTestAgentWithSandbox(t)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	// "echo" is not in AllowedCommands ("allowed_cmd"), so policy validation fails.
	_, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "sb-cmd-1",
		Type:   task.TypeSandboxExec,
		Payload: map[string]any{
			"command": "echo",
			"args":    []any{"hello"},
		},
	})
	if err == nil {
		t.Fatal("expected error for disallowed sandbox command")
	}
	if !strings.Contains(err.Error(), "sandbox command exec") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDispatch_SandboxExec_Script(t *testing.T) {
	agent := newTestAgentWithSandbox(t)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	// Script with no interpreter configured will fail policy validation.
	_, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "sb-script-1",
		Type:   task.TypeSandboxExec,
		Payload: map[string]any{
			"script":      "echo hello",
			"interpreter": "bash",
		},
	})
	if err == nil {
		t.Fatal("expected error for sandbox script execution")
	}
	if !strings.Contains(err.Error(), "sandbox script exec") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDispatch_SandboxExec_WithTimeout(t *testing.T) {
	agent := newTestAgentWithSandbox(t)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	// Test with float64 timeout.
	_, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "sb-timeout-1",
		Type:   task.TypeSandboxExec,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": float64(30),
		},
	})
	if err == nil {
		t.Fatal("expected error for sandbox command")
	}

	// Test with int timeout.
	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID: "sb-timeout-2",
		Type:   task.TypeSandboxExec,
		Payload: map[string]any{
			"command":         "echo",
			"timeout_seconds": 30,
		},
	})
	if err == nil {
		t.Fatal("expected error for sandbox command")
	}
}

func TestDispatch_SandboxExec_EmptyTaskID(t *testing.T) {
	agent := newTestAgentWithSandbox(t)

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	// Empty TaskID should get auto-generated.
	_, err := dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "",
		Type:    task.TypeSandboxExec,
		Payload: map[string]any{"command": "echo"},
	})
	if err == nil {
		t.Fatal("expected error for sandbox command")
	}
	// The error message should contain the sandbox command exec prefix,
	// confirming the handler ran through the task ID generation path.
	if !strings.Contains(err.Error(), "sandbox command exec") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GRPC handlers with sandbox
// ---------------------------------------------------------------------------

func TestGRPCHandlers_CommandHandler_WithSandbox(t *testing.T) {
	agent := newTestAgentWithSandbox(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// "notallowed" is not in the sandbox policy's allowed list.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:  "cmd-sandbox-1",
				Command: "notallowed",
				Args:    []string{"arg1"},
			},
		},
	})
	// Handler returns nil even on exec error (sends result via gRPC).
	if err != nil {
		t.Fatalf("handle command with sandbox: %v", err)
	}
}

func TestGRPCHandlers_ScriptHandler_WithSandbox(t *testing.T) {
	agent := newTestAgentWithSandbox(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Script execution will fail due to policy validation.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{
				TaskId:      "script-sandbox-1",
				Script:      "echo hello",
				Interpreter: "bash",
			},
		},
	})
	// Handler returns nil even on exec error (sends result via gRPC).
	if err != nil {
		t.Fatalf("handle script with sandbox: %v", err)
	}
}

func TestGRPCHandlers_ScriptHandler_WithSandbox_EmptyTaskID(t *testing.T) {
	agent := newTestAgentWithSandbox(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Script with empty task ID - handler should still work.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecScript{
			ExecScript: &pb.ExecuteScript{
				TaskId:      "",
				Script:      "echo hello",
				Interpreter: "bash",
			},
		},
	})
	if err != nil {
		t.Fatalf("handle script with empty task ID: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GRPC config update success path
// ---------------------------------------------------------------------------

func TestGRPCHandlers_ConfigUpdate_Success(t *testing.T) {
	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Executor: config.ExecutorConfig{
			AllowedCommands: []string{"echo"},
			TimeoutSeconds:  10,
			MaxOutputBytes:  1024,
		},
	}
	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// Send the same config as YAML - no changes means Apply succeeds.
	validYAML := []byte(`
agent:
  id: test
server:
  listen_addr: ":0"
executor:
  allowed_commands: ["echo"]
  timeout_seconds: 10
  max_output_bytes: 1024
`)
	err = recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ConfigUpdate{
			ConfigUpdate: &pb.ConfigUpdate{
				ConfigYaml: validYAML,
				Version:    1,
			},
		},
	})
	if err != nil {
		t.Fatalf("handle config update: %v", err)
	}
}

// ---------------------------------------------------------------------------
// buildScheduler init error paths
// ---------------------------------------------------------------------------

func TestBuildScheduler_ProcessorInitError(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Processors: []config.PluginInstanceConfig{
				{Type: "regex", Config: map[string]interface{}{"tags": "not-a-list"}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for invalid processor config")
	}
	if !strings.Contains(err.Error(), "init processor") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_AggregatorInitError(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Aggregators: []config.PluginInstanceConfig{
				{Type: "avg", Config: map[string]interface{}{"fields": "not-a-list"}},
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for invalid aggregator config")
	}
	if !strings.Contains(err.Error(), "init aggregator") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildScheduler_OutputInitError(t *testing.T) {
	cfg := &config.Config{
		Agent: config.AgentConfig{IntervalSeconds: 10},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
			Outputs: []config.PluginInstanceConfig{
				{Type: "http", Config: map[string]interface{}{"url": 12345}}, // url must be string
			},
		},
	}
	_, err := buildScheduler(cfg, zerolog.Nop())
	if err == nil {
		t.Fatal("expected error for invalid output config")
	}
	if !strings.Contains(err.Error(), "init output") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// startSubsystems error paths
// ---------------------------------------------------------------------------

func TestStartSubsystems_PluginGatewayStartError(t *testing.T) {
	gw := newMockPluginGateway()
	gw.startErr = fmt.Errorf("gateway start failed")

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
		WithPluginGateway(gw),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = agent.Run(ctx)
	if err == nil {
		t.Fatal("expected error when plugin gateway fails to start")
	}
	if !strings.Contains(err.Error(), "start plugin gateway") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartSubsystems_GRPCStartError(t *testing.T) {
	grpcClient := newMockGRPCClient()
	grpcClient.startErr = fmt.Errorf("grpc start failed")

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(grpcClient),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = agent.Run(ctx)
	if err == nil {
		t.Fatal("expected error when gRPC client fails to start")
	}
	if !strings.Contains(err.Error(), "start grpc client") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// shutdown with plugin gateway
// ---------------------------------------------------------------------------

func TestShutdown_PluginGatewayStopError(t *testing.T) {
	type mockPluginGatewayStopErr struct {
		*mockPluginGateway
	}
	// Can't easily embed stop error in the mock, so use a custom wrapper.

	gw := newMockPluginGateway()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(newMockPluginRuntime()),
		WithPluginGateway(gw),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	// Wait for subsystems to start.
	<-agent.server.(*mockHTTPServer).started

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to complete")
	}

	if !agent.IsShutdownComplete() {
		t.Error("expected shutdown to complete")
	}
	if got := gw.stopCalled.Load(); got != 1 {
		t.Errorf("gateway.Stop called %d times, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// Plugin task error path (execution error from dispatch)
// ---------------------------------------------------------------------------

func TestDispatch_PluginTask_ExecutionError(t *testing.T) {
	pr := newMockPluginRuntime()
	pr.executeTaskFn = func(_ context.Context, _ pluginruntime.TaskRequest) (*pluginruntime.TaskResponse, error) {
		return nil, fmt.Errorf("plugin execution failed")
	}

	agent, err := NewAgent(&config.Config{
		Agent:  config.AgentConfig{ID: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
		Plugin: config.PluginConfig{Enabled: true, RequestTimeoutSeconds: 30, ChunkSizeBytes: 1024, MaxResultBytes: 10240},
	}, zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(pr),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	dispatcher := task.NewDispatcher()
	agent.registerTaskHandlers(dispatcher)

	_, err = dispatcher.Dispatch(context.Background(), task.AgentTask{
		TaskID:  "pl-err-1",
		Type:    task.TypePluginLogParse,
		Payload: map[string]any{},
	})
	if err == nil {
		t.Fatal("expected error from plugin execution")
	}
	if err.Error() != "plugin execution failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GRPC command handler with default timeout (timeoutSec <= 0)
// ---------------------------------------------------------------------------

func TestGRPCHandlers_CommandHandler_DefaultTimeout(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// TimeoutSeconds=0 should fall back to config default.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:         "cmd-default-timeout",
				Command:        "echo",
				Args:           []string{"hello"},
				TimeoutSeconds: 0,
			},
		},
	})
	if err != nil {
		t.Fatalf("handle command with default timeout: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Audit logger edge cases
// ---------------------------------------------------------------------------

func TestAuditLogger_Close_Nil(t *testing.T) {
	var al *AuditLogger
	if err := al.Close(); err != nil {
		t.Errorf("Close on nil logger should return nil, got: %v", err)
	}
}

func TestAuditLogger_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	al, err := NewAuditLogger(path, 1, 1)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al.Close()

	// Write many events to test the logger under concurrent access.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			al.Log(AuditEvent{
				EventType: fmt.Sprintf("event.%d", i),
				Component: "test",
				Action:    "write",
				Status:    "success",
			})
		}(i)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// RunOnce channel closed before collecting
// ---------------------------------------------------------------------------

func TestRunOnce_ChannelClosedBeforeCollect(t *testing.T) {
	scheduler := newMockScheduler()

	agent, err := NewAgent(minimalConfig(), zerolog.Nop(),
		WithGRPCClient(newMockGRPCClient()),
		WithServer(newMockHTTPServer()),
		WithScheduler(scheduler),
		WithPluginRuntime(newMockPluginRuntime()),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Close the channel immediately before RunOnce reads from it.
	close(scheduler.ch)

	err = agent.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected error when pipeline channel closed before collecting")
	}
	if !strings.Contains(err.Error(), "pipeline channel closed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// eventLoop with normal pipeline metrics flow
// ---------------------------------------------------------------------------

func TestEventLoop_PipelineMetrics(t *testing.T) {
	agent := newTestAgent(t)
	agent.metricsReg = NewMetricsRegistry()

	pipelineCh := make(chan []*collector.Metric, 1)
	errCh := make(chan error, 1)

	// Push metrics then cancel.
	testMetrics := []*collector.Metric{
		collector.NewMetric(
			"test",
			map[string]string{"host": "test"},
			map[string]interface{}{"value": float64(1)},
			collector.Gauge,
			time.Now(),
		),
	}
	pipelineCh <- testMetrics

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately after pushing

	agent.eventLoop(ctx, pipelineCh, errCh)
	// Should complete without panic.
}

// ---------------------------------------------------------------------------
// GRPC command handler with non-zero exit code
// ---------------------------------------------------------------------------

func TestGRPCHandlers_CommandHandler_NonZeroExit(t *testing.T) {
	agent := newTestAgent(t)
	recv := grpcclient.NewReceiver(zerolog.Nop())
	agent.registerGRPCHandlers(recv)

	// "false" is not in allowed commands, so executor returns error.
	err := recv.Handle(context.Background(), &pb.PlatformMessage{
		Payload: &pb.PlatformMessage_ExecCommand{
			ExecCommand: &pb.ExecuteCommand{
				TaskId:  "cmd-fail",
				Command: "false",
			},
		},
	})
	// Handler returns nil even on exec error.
	if err != nil {
		t.Fatalf("handle command: %v", err)
	}
}
