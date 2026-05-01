# Production Hardening — Test & Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Raise test coverage from 43.3% to ≥70%, refactor Agent with interface-based DI, remove legacy communication path, and clean up technical debt.

**Architecture:** Define interfaces for Agent dependencies (GRPCClient, HTTPServer, Scheduler, PluginRuntime), inject via functional options, decompose Run() into startSubsystems/eventLoop/shutdown. Test each package incrementally using table-driven tests with hand-written mocks.

**Tech Stack:** Go 1.26.1, zerolog, gRPC, cobra, stdlib testing (no third-party test libs)

---

## File Structure

### New Files
| File | Responsibility |
|------|---------------|
| `internal/app/interfaces.go` | Interface definitions for Agent dependencies |
| `internal/app/options.go` | Functional options for Agent DI |
| `internal/app/agent_test.go` | Agent lifecycle unit tests |
| `internal/grpcclient/receiver_test.go` | Receiver handler dispatch tests |
| `internal/logger/logger_test.go` | Logger level/format tests |
| `internal/collector/host_test.go` | HostCollector tests |
| `internal/collector/manager_test.go` | Manager.CollectAll tests |

### Modified Files
| File | Changes |
|------|---------|
| `internal/app/agent.go` | Refactor Agent struct to use interfaces, decompose Run(), remove legacy path |
| `internal/grpcclient/client.go` | No changes needed (already implements interface) |
| `internal/grpcclient/client_test.go` | Add connectLoop/messageLoop/replayCache tests |
| `internal/server/server_test.go` | Add Start/Shutdown tests |
| `internal/pluginruntime/runtime_test.go` | Add Start/Stop tests |
| `internal/sandbox/executor_test.go` | Add WriteConfigFile/buildConfigContent tests |
| `internal/collector/processors/tagger/tagger_test.go` | Add edge case tests |
| `internal/collector/processors/regex/regex_test.go` | Add edge case tests |
| `internal/config/config_test.go` | Add boundary value tests |

### Deleted/Unchanged
| File | Action |
|------|--------|
| `dist/nodeagentx-*` | Delete via `make clean` |

---

## Phase 1: Interface Definitions + Cleanup

### Task 1: Define Agent dependency interfaces

**Files:**
- Create: `internal/app/interfaces.go`

- [ ] **Step 1: Create interfaces.go with all dependency interfaces**

Interfaces must match the exact method signatures of the concrete types they abstract. Read `internal/grpcclient/client.go`, `internal/server/server.go`, `internal/collector/scheduler.go`, and `internal/pluginruntime/runtime.go` to confirm signatures before writing.

```go
package app

import (
	"context"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/grpcclient"
	"github.com/cy77cc/opsagent/internal/pluginruntime"
	"github.com/cy77cc/opsagent/internal/server"
)

// GRPCClient defines the behavior the Agent expects from a gRPC client.
// Matches *grpcclient.Client method signatures exactly.
type GRPCClient interface {
	Start(ctx context.Context) error
	Stop()
	SendMetrics(metrics []*collector.Metric)
	SendExecOutput(taskID, streamName string, data []byte)
	SendExecResult(result *grpcclient.ExecResult)
	IsConnected() bool
}

// HTTPServer defines the behavior the Agent expects from an HTTP server.
// Matches *server.Server method signatures exactly.
type HTTPServer interface {
	Start() error
	Shutdown(ctx context.Context) error
}

// Scheduler defines the behavior the Agent expects from a collector scheduler.
// Matches *collector.Scheduler method signatures exactly.
type Scheduler interface {
	Start(ctx context.Context) <-chan []*collector.Metric
	Stop()
}

// PluginRuntime defines the behavior the Agent expects from a plugin runtime.
// Matches *pluginruntime.Runtime method signatures exactly.
type PluginRuntime interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Compile-time interface satisfaction checks.
var (
	_ GRPCClient    = (*grpcclient.Client)(nil)
	_ HTTPServer    = (*server.Server)(nil)
	_ Scheduler     = (*collector.Scheduler)(nil)
	_ PluginRuntime = (*pluginruntime.Runtime)(nil)
)
```

- [ ] **Step 2: Verify the file compiles**

Run: `go vet ./internal/app/`
Expected: no errors (the compile-time assertions confirm concrete types satisfy the interfaces).

- [ ] **Step 3: Commit**

```bash
git add internal/app/interfaces.go
git commit -m "feat(app): define dependency interfaces for Agent DI"
```

### Task 3: Clean up dist/ leftovers

**Files:**
- Delete: `dist/amd64/nodeagentx`, `dist/arm64/nodeagentx-arm64`, `dist/nodeagentx-*.tar.gz`

- [ ] **Step 1: Run make clean**

Run: `make clean`
Expected: dist/ directory removed

- [ ] **Step 2: Verify dist/ is gone**

Run: `ls dist/ 2>&1`
Expected: "No such file or directory"

- [ ] **Step 3: Commit**

```bash
git add -A dist/
git commit -m "chore: clean up stale nodeagentx build artifacts"
```

---

## Phase 2: Agent Refactoring (DI + Run Decomposition + Legacy Removal)

### Task 4: Add functional options to Agent

**Files:**
- Create: `internal/app/options.go`
- Modify: `internal/app/agent.go`

- [ ] **Step 1: Create options.go**

```go
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
```

- [ ] **Step 2: Verify compilation**

Run: `go vet ./internal/app/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/app/options.go
git commit -m "feat(app): add functional options for Agent DI"
```

### Task 5: Refactor Agent struct to use interfaces

**Files:**
- Modify: `internal/app/agent.go`

- [ ] **Step 1: Change Agent struct fields to interface types**

Replace the concrete type fields in the Agent struct:

```go
type Agent struct {
	cfg           *config.Config
	log           zerolog.Logger
	manager       *collector.Manager
	reporter      reporter.Reporter
	server        HTTPServer            // was *server.Server
	executor      *executor.Executor
	pluginRuntime PluginRuntime         // was *pluginruntime.Runtime
	scheduler     Scheduler             // was *collector.Scheduler
	grpcClient    GRPCClient            // was *grpcclient.Client
	sandboxExec   *sandbox.Executor
	startedAt     time.Time
	activeTasks   sync.Map
}
```

- [ ] **Step 2: Update NewAgent to accept options**

Change the signature from:
```go
func NewAgent(cfg *config.Config, log zerolog.Logger) (*Agent, error)
```
to:
```go
func NewAgent(cfg *config.Config, log zerolog.Logger, opts ...Option) (*Agent, error)
```

Add option application at the start of the function body:
```go
a := &Agent{
	cfg:       cfg,
	log:       log,
	startedAt: time.Now().UTC(),
}
for _, opt := range opts {
	opt(a)
}
```

Then, only create default implementations for fields that weren't injected:
```go
// Only create defaults for non-injected dependencies.
if a.pluginRuntime == nil {
	a.pluginRuntime = pluginruntime.New(...)
}
if a.scheduler == nil {
	sched, err := buildScheduler(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("build scheduler: %w", err)
	}
	a.scheduler = sched
}
if a.grpcClient == nil {
	// ... build default grpc client
}
if a.server == nil {
	// ... build default server
}
```

- [ ] **Step 3: Update the caller in NewRootCommand**

In `NewRootCommand()`, the call `NewAgent(cfg, log)` still works because opts is variadic. No change needed.

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent.go
git commit -m "refactor(app): Agent struct uses interfaces, NewAgent accepts options"
```

### Task 6: Decompose Run() and remove legacy path

**Files:**
- Modify: `internal/app/agent.go`

- [ ] **Step 1: Create startSubsystems method**

Extract the startup logic from Run() into a new method:

```go
func (a *Agent) startSubsystems(ctx context.Context) (<-chan []*collector.Metric, chan error, error) {
	a.log.Info().Str("agent_id", a.cfg.Agent.ID).Str("listen_addr", a.cfg.Server.ListenAddr).Msg("agent starting")

	if err := a.pluginRuntime.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start plugin runtime: %w", err)
	}

	var pipelineCh <-chan []*collector.Metric
	if a.scheduler != nil {
		pipelineCh = a.scheduler.Start(ctx)
		a.log.Info().Msg("collector pipeline scheduler started")
	}

	if err := a.grpcClient.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start grpc client: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.server.Start()
	}()

	return pipelineCh, errCh, nil
}
```

- [ ] **Step 2: Create eventLoop method**

```go
func (a *Agent) eventLoop(ctx context.Context, pipelineCh <-chan []*collector.Metric, errCh chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if err != nil {
				a.log.Error().Err(err).Msg("http server stopped with error")
			}
			return
		case metrics, ok := <-pipelineCh:
			if !ok {
				pipelineCh = nil
				continue
			}
			a.handlePipelineMetrics(metrics)
		}
	}
}
```

- [ ] **Step 3: Create shutdown method**

```go
func (a *Agent) shutdown() {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Error().Err(err).Msg("failed to shutdown server")
	}
	a.grpcClient.Stop()
	a.scheduler.Stop()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := a.pluginRuntime.Stop(stopCtx); err != nil {
		a.log.Error().Err(err).Msg("failed to stop plugin runtime")
	}
}
```

- [ ] **Step 4: Rewrite Run() to use the three new methods**

```go
func (a *Agent) Run(ctx context.Context) error {
	pipelineCh, errCh, err := a.startSubsystems(ctx)
	if err != nil {
		return err
	}
	defer a.shutdown()
	a.eventLoop(ctx, pipelineCh, errCh)
	return nil
}
```

- [ ] **Step 5: Remove legacy code**

Delete these functions/fields from `agent.go`:
- `collectAndReport()` method
- `handlePipelineMetrics()` stays (it sends via gRPC, which is the new path)
- Remove the `manager` field from Agent struct
- Remove the `reporter` field from Agent struct
- Remove the `ticker` and legacy select case from eventLoop
- Remove the `manager` and `reporter` creation from NewAgent
- Remove the `collectAndReport` call and initial collect from Run()
- Keep `registerTaskHandlers` (it uses `a.manager` for collect-metrics task — update to use scheduler or remove that handler)

**Note:** The `registerTaskHandlers` function references `a.manager` for the `TypeCollectMetrics` task. Since we're removing the manager, update that handler to return an error indicating the legacy path is removed, or remove the handler entirely.

- [ ] **Step 6: Verify compilation**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 7: Run existing integration tests**

Run: `go test ./internal/integration/ -race -count=1`
Expected: tests pass (or identify what needs updating)

- [ ] **Step 8: Commit**

```bash
git add internal/app/agent.go
git commit -m "refactor(app): decompose Run() into startSubsystems/eventLoop/shutdown, remove legacy path"
```

### Task 7: Write Agent lifecycle tests

**Files:**
- Create: `internal/app/agent_test.go`

- [ ] **Step 1: Create mock implementations for testing**

```go
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
	"github.com/rs/zerolog"
)

// mockGRPCClient implements GRPCClient for testing.
type mockGRPCClient struct {
	startFn      func(ctx context.Context) error
	stopFn       func()
	metricsSent  atomic.Int32
	started      atomic.Bool
	stopped      atomic.Bool
}

func (m *mockGRPCClient) Start(ctx context.Context) error {
	m.started.Store(true)
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return nil
}

func (m *mockGRPCClient) Stop() {
	m.stopped.Store(true)
	if m.stopFn != nil {
		m.stopFn()
	}
}

func (m *mockGRPCClient) SendMetrics(metrics []*collector.Metric) {
	m.metricsSent.Add(int32(len(metrics)))
}

func (m *mockGRPCClient) SendExecOutput(taskID, streamName string, data []byte) {}

func (m *mockGRPCClient) SendExecResult(result *grpcclient.ExecResult) {}

func (m *mockGRPCClient) IsConnected() bool { return m.started.Load() }

// mockHTTPServer implements HTTPServer for testing.
type mockHTTPServer struct {
	startFn    func() error
	shutdownFn func(ctx context.Context) error
	started    atomic.Bool
	shutdown   atomic.Bool
}

func (m *mockHTTPServer) Start() error {
	m.started.Store(true)
	if m.startFn != nil {
		return m.startFn()
	}
	// Block until shutdown is called.
	for m.started.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (m *mockHTTPServer) Shutdown(ctx context.Context) error {
	m.started.Store(false)
	m.shutdown.Store(true)
	if m.shutdownFn != nil {
		return m.shutdownFn(ctx)
	}
	return nil
}

// mockScheduler implements Scheduler for testing.
type mockScheduler struct {
	ch      chan []*collector.Metric
	started atomic.Bool
	stopped atomic.Bool
}

func newMockScheduler() *mockScheduler {
	return &mockScheduler{
		ch: make(chan []*collector.Metric, 10),
	}
}

func (m *mockScheduler) Start(ctx context.Context) <-chan []*collector.Metric {
	m.started.Store(true)
	return m.ch
}

func (m *mockScheduler) Stop() {
	m.stopped.Store(true)
	close(m.ch)
}

// mockPluginRuntime implements PluginRuntime for testing.
type mockPluginRuntime struct {
	startFn func(ctx context.Context) error
	stopFn  func(ctx context.Context) error
	started atomic.Bool
	stopped atomic.Bool
}

func (m *mockPluginRuntime) Start(ctx context.Context) error {
	m.started.Store(true)
	if m.startFn != nil {
		return m.startFn(ctx)
	}
	return nil
}

func (m *mockPluginRuntime) Stop(ctx context.Context) error {
	m.stopped.Store(true)
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}
```

- [ ] **Step 2: Write TestAgentRun_StartsAndStopsAllSubsystems**

```go
func TestAgentRun_StartsAndStopsAllSubsystems(t *testing.T) {
	grpc := &mockGRPCClient{}
	srv := &mockHTTPServer{}
	sched := newMockScheduler()
	rt := &mockPluginRuntime{}

	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test", IntervalSeconds: 1},
		Server: config.ServerConfig{ListenAddr: ":0"},
	}

	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(grpc),
		WithServer(srv),
		WithScheduler(sched),
		WithPluginRuntime(rt),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx)
	}()

	// Give subsystems time to start.
	time.Sleep(100 * time.Millisecond)

	if !rt.started.Load() {
		t.Error("plugin runtime not started")
	}
	if !sched.started.Load() {
		t.Error("scheduler not started")
	}
	if !grpc.started.Load() {
		t.Error("grpc client not started")
	}
	if !srv.started.Load() {
		t.Error("server not started")
	}

	// Cancel to trigger shutdown.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}

	if !rt.stopped.Load() {
		t.Error("plugin runtime not stopped")
	}
	if !sched.stopped.Load() {
		t.Error("scheduler not stopped")
	}
	if !grpc.stopped.Load() {
		t.Error("grpc client not stopped")
	}
	if !srv.shutdown.Load() {
		t.Error("server not shutdown")
	}
}
```

- [ ] **Step 3: Write TestAgentRun_ForwardsPipelineMetrics**

```go
func TestAgentRun_ForwardsPipelineMetrics(t *testing.T) {
	grpc := &mockGRPCClient{}
	srv := &mockHTTPServer{}
	sched := newMockScheduler()
	rt := &mockPluginRuntime{}

	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test", IntervalSeconds: 1},
		Server: config.ServerConfig{ListenAddr: ":0"},
	}

	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(grpc),
		WithServer(srv),
		WithScheduler(sched),
		WithPluginRuntime(rt),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agent.Run(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send metrics through the pipeline channel.
	metrics := []*collector.Metric{
		collector.NewMetric("test", map[string]string{}, map[string]interface{}{"value": 1.0}, collector.Gauge, time.Now()),
	}
	sched.ch <- metrics

	time.Sleep(100 * time.Millisecond)

	if grpc.metricsSent.Load() == 0 {
		t.Error("metrics not forwarded to gRPC client")
	}
}
```

- [ ] **Step 4: Write TestAgentRun_StartSubsystemFailure**

```go
func TestAgentRun_StartSubsystemFailure(t *testing.T) {
	rt := &mockPluginRuntime{
		startFn: func(_ context.Context) error {
			return fmt.Errorf("plugin start failed")
		},
	}

	cfg := &config.Config{
		Agent:  config.AgentConfig{ID: "test", Name: "test"},
		Server: config.ServerConfig{ListenAddr: ":0"},
	}

	agent, err := NewAgent(cfg, zerolog.Nop(),
		WithGRPCClient(&mockGRPCClient{}),
		WithServer(&mockHTTPServer{}),
		WithScheduler(newMockScheduler()),
		WithPluginRuntime(rt),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	err = agent.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Run when plugin runtime fails to start")
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/app/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/app/agent_test.go
git commit -m "test(app): add Agent lifecycle tests with mock dependencies"
```

---

## Phase 3: gRPC Client Tests

### Task 8: Add connectLoop/messageLoop/replayCache tests

**Files:**
- Modify: `internal/grpcclient/client_test.go`

- [ ] **Step 1: Add mock stream for testing**

Add to the existing import block in `client_test.go`:
```go
import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/metadata"
)
```

Then append to `client_test.go`:

// mockConnectStream implements pb.AgentService_ConnectClient for testing.
type mockConnectStream struct {
	sendFn    func(*pb.AgentMessage) error
	recvFn    func() (*pb.PlatformMessage, error)
	sendCount atomic.Int32
	recvCount atomic.Int32
}

func (m *mockConnectStream) Send(msg *pb.AgentMessage) error {
	m.sendCount.Add(1)
	if m.sendFn != nil {
		return m.sendFn(msg)
	}
	return nil
}

func (m *mockConnectStream) Recv() (*pb.PlatformMessage, error) {
	m.recvCount.Add(1)
	if m.recvFn != nil {
		return m.recvFn()
	}
	return nil, io.EOF
}

func (m *mockConnectStream) Header() (metadata.MD, error)  { return nil, nil }
func (m *mockConnectStream) Trailer() metadata.MD           { return nil }
func (m *mockConnectStream) CloseSend() error               { return nil }
func (m *mockConnectStream) Context() context.Context       { return context.Background() }
func (m *mockConnectStream) SendMsg(interface{}) error      { return nil }
func (m *mockConnectStream) RecvMsg(interface{}) error      { return nil }
```

- [ ] **Step 2: Write TestClientSendMetrics_CachesWhenDisconnected**

```go
func TestClientSendMetrics_CachesWhenDisconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	metrics := []*collector.Metric{
		collector.NewMetric("cpu", map[string]string{}, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now()),
	}
	c.SendMetrics(metrics)

	if c.cache.Len() != 1 {
		t.Errorf("expected 1 cached metric, got %d", c.cache.Len())
	}
}
```

- [ ] **Step 3: Write TestClientSendMetrics_EmptySlice**

```go
func TestClientSendMetrics_EmptySlice(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	c.SendMetrics(nil)
	c.SendMetrics([]*collector.Metric{})
	// Should not panic and cache should remain empty.
	if c.cache.Len() != 0 {
		t.Errorf("expected 0 cached metrics, got %d", c.cache.Len())
	}
}
```

- [ ] **Step 4: Write TestClientReplayCache_Empty**

```go
func TestClientReplayCache_Empty(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Should not panic when cache is empty and stream is nil.
	c.replayCache()
}
```

- [ ] **Step 5: Write TestClientReplayCache_SendsBatch**

```go
func TestClientReplayCache_SendsBatch(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// Add metrics to cache.
	for i := 0; i < 3; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	stream := &mockConnectStream{
		sendFn: func(msg *pb.AgentMessage) error { return nil },
	}

	c.mu.Lock()
	c.stream = stream
	c.connected = true
	c.mu.Unlock()

	c.replayCache()

	if stream.sendCount.Load() != 1 {
		t.Errorf("expected 1 send call, got %d", stream.sendCount.Load())
	}
	if c.cache.Len() != 0 {
		t.Errorf("expected cache drained (0), got %d", c.cache.Len())
	}
}
```

- [ ] **Step 6: Write TestClientReplayCache_SendFailureRecaches**

```go
func TestClientReplayCache_SendFailureRecaches(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	for i := 0; i < 3; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	stream := &mockConnectStream{
		sendFn: func(msg *pb.AgentMessage) error { return fmt.Errorf("send failed") },
	}

	c.mu.Lock()
	c.stream = stream
	c.connected = true
	c.mu.Unlock()

	c.replayCache()

	if c.cache.Len() != 3 {
		t.Errorf("expected 3 re-cached metrics, got %d", c.cache.Len())
	}
}
```

- [ ] **Step 7: Write TestClientStop_Idempotent**

```go
func TestClientStop_Idempotent(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Stop without Start should not panic.
	c.Stop()
	c.Stop()
}
```

- [ ] **Step 8: Write TestClientStartStop**

```go
func TestClientStartStop(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	ctx, cancel := context.WithCancel(context.Background())

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Cancel to trigger connectLoop exit.
	cancel()
	c.Stop()

	// Should not panic on double stop.
	c.Stop()
}
```

- [ ] **Step 9: Run tests**

Run: `go test ./internal/grpcclient/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 10: Commit**

```bash
git add internal/grpcclient/client_test.go
git commit -m "test(grpcclient): add SendMetrics, replayCache, and lifecycle tests"
```

### Task 9: Add Receiver handler dispatch tests

**Files:**
- Create: `internal/grpcclient/receiver_test.go`

- [ ] **Step 1: Write table-driven tests for Receiver.Handle**

```go
package grpcclient

import (
	"context"
	"sync"
	"testing"

	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
	"github.com/rs/zerolog"
)

func TestReceiverHandle_DispatchesToCorrectHandler(t *testing.T) {
	tests := []struct {
		name      string
		msg       *pb.PlatformMessage
		wantCmd   bool
		wantScript bool
		wantCancel bool
		wantConfig bool
	}{
		{
			name:    "command message",
			msg:     &pb.PlatformMessage{Payload: &pb.PlatformMessage_Command{Command: &pb.ExecuteCommand{TaskId: "t1"}}},
			wantCmd: true,
		},
		{
			name:       "script message",
			msg:        &pb.PlatformMessage{Payload: &pb.PlatformMessage_Script{Script: &pb.ExecuteScript{TaskId: "t2"}}},
			wantScript: true,
		},
		{
			name:       "cancel message",
			msg:        &pb.PlatformMessage{Payload: &pb.PlatformMessage_Cancel{Cancel: &pb.CancelJob{TaskId: "t3"}}},
			wantCancel: true,
		},
		{
			name:       "config update message",
			msg:        &pb.PlatformMessage{Payload: &pb.PlatformMessage_Config{Config: &pb.ConfigUpdate{Version: 1}}},
			wantConfig: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				cmdCalled    bool
				scriptCalled bool
				cancelCalled bool
				configCalled bool
				mu           sync.Mutex
			)

			r := NewReceiver(zerolog.Nop())
			r.SetCommandHandler(func(_ context.Context, _ *pb.ExecuteCommand) error {
				mu.Lock()
				cmdCalled = true
				mu.Unlock()
				return nil
			})
			r.SetScriptHandler(func(_ context.Context, _ *pb.ExecuteScript) error {
				mu.Lock()
				scriptCalled = true
				mu.Unlock()
				return nil
			})
			r.SetCancelHandler(func(_ context.Context, _ *pb.CancelJob) error {
				mu.Lock()
				cancelCalled = true
				mu.Unlock()
				return nil
			})
			r.SetConfigUpdateHandler(func(_ context.Context, _ *pb.ConfigUpdate) error {
				mu.Lock()
				configCalled = true
				mu.Unlock()
				return nil
			})

			err := r.Handle(context.Background(), tt.msg)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}

			mu.Lock()
			if cmdCalled != tt.wantCmd {
				t.Errorf("cmdCalled = %v, want %v", cmdCalled, tt.wantCmd)
			}
			if scriptCalled != tt.wantScript {
				t.Errorf("scriptCalled = %v, want %v", scriptCalled, tt.wantScript)
			}
			if cancelCalled != tt.wantCancel {
				t.Errorf("cancelCalled = %v, want %v", cancelCalled, tt.wantCancel)
			}
			if configCalled != tt.wantConfig {
				t.Errorf("configCalled = %v, want %v", configCalled, tt.wantConfig)
			}
			mu.Unlock()
		})
	}
}

func TestReceiverHandle_NilHandler(t *testing.T) {
	r := NewReceiver(zerolog.Nop())
	// Should not panic with nil handlers.
	msg := &pb.PlatformMessage{Payload: &pb.PlatformMessage_Command{Command: &pb.ExecuteCommand{TaskId: "t1"}}}
	err := r.Handle(context.Background(), msg)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

func TestReceiverHandle_NilPayload(t *testing.T) {
	r := NewReceiver(zerolog.Nop())
	err := r.Handle(context.Background(), &pb.PlatformMessage{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/grpcclient/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 3: Commit**

```bash
git add internal/grpcclient/receiver_test.go
git commit -m "test(grpcclient): add Receiver handler dispatch tests"
```

---

## Phase 4: Server/PluginRuntime/Sandbox/Logger Tests

### Task 10: Add Server Start/Shutdown tests

**Files:**
- Modify: `internal/server/server_test.go` (or create if needed)

- [ ] **Step 1: Write TestServerStartAndShutdown**

```go
func TestServerStartAndShutdown(t *testing.T) {
	// Use port 0 to get a random available port.
	srv := server.New(":0", zerolog.Nop(), nil, nil, time.Now(), server.Options{})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Give server time to start.
	time.Sleep(50 * time.Millisecond)

	// Shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Start should return nil after graceful shutdown.
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Start returned error after shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not return after shutdown")
	}
}
```

- [ ] **Step 2: Write TestServerSetLatestMetric**

```go
func TestServerSetLatestMetric(t *testing.T) {
	srv := server.New(":0", zerolog.Nop(), nil, nil, time.Now(), server.Options{})

	if srv.LatestMetricExists() {
		t.Error("expected no metric initially")
	}

	srv.SetLatestMetric(&collector.MetricPayload{Collector: "test"})

	if !srv.LatestMetricExists() {
		t.Error("expected metric to exist after set")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/server/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/server/server_test.go
git commit -m "test(server): add Start/Shutdown and SetLatestMetric tests"
```

### Task 11: Add PluginRuntime Start/Stop tests

**Files:**
- Modify: `internal/pluginruntime/runtime_test.go` (rename from client_test.go or create)

- [ ] **Step 1: Write TestRuntimeStart_Disabled**

```go
func TestRuntimeStart_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	r := New(cfg, zerolog.Nop())

	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
}
```

- [ ] **Step 2: Write TestRuntimeStart_MissingSocketPath**

```go
func TestRuntimeStart_MissingSocketPath(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: ""}
	r := New(cfg, zerolog.Nop())

	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing socket path")
	}
}
```

- [ ] **Step 3: Write TestRuntimeStart_MissingRuntimePath**

```go
func TestRuntimeStart_MissingRuntimePath(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: true, RuntimePath: ""}
	r := New(cfg, zerolog.Nop())

	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing runtime path when autostart=true")
	}
}
```

- [ ] **Step 4: Write TestRuntimeStart_NoAutoStart**

```go
func TestRuntimeStart_NoAutoStart(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: false}
	r := New(cfg, zerolog.Nop())

	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	r.mu.Lock()
	started := r.started
	r.mu.Unlock()

	if !started {
		t.Error("expected runtime to be marked as started")
	}
}
```

- [ ] **Step 5: Write TestRuntimeStop_NotStarted**

```go
func TestRuntimeStop_NotStarted(t *testing.T) {
	cfg := Config{Enabled: true}
	r := New(cfg, zerolog.Nop())

	err := r.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/pluginruntime/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/pluginruntime/
git commit -m "test(pluginruntime): add Start/Stop lifecycle tests"
```

### Task 12: Add Logger tests

**Files:**
- Create: `internal/logger/logger_test.go`

- [ ] **Step 1: Write table-driven tests**

```go
package logger

import (
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_LogLevelParsing(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantLevel zerolog.Level
	}{
		{"debug", "debug", zerolog.DebugLevel},
		{"info", "info", zerolog.InfoLevel},
		{"warn", "warn", zerolog.WarnLevel},
		{"error", "error", zerolog.ErrorLevel},
		{"uppercase", "DEBUG", zerolog.DebugLevel},
		{"invalid defaults to info", "invalid", zerolog.InfoLevel},
		{"empty defaults to info", "", zerolog.InfoLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(tt.level)
			if l.GetLevel() != tt.wantLevel {
				t.Errorf("GetLevel() = %v, want %v", l.GetLevel(), tt.wantLevel)
			}
		})
	}
}

func TestNew_ReturnsFunctionalLogger(t *testing.T) {
	l := New("info")
	// Should not panic.
	l.Info().Msg("test message")
	l.Debug().Msg("debug message")
	l.Error().Msg("error message")
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/logger/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 3: Commit**

```bash
git add internal/logger/logger_test.go
git commit -m "test(logger): add level parsing and functional tests"
```

### Task 13: Add Sandbox config/policy tests

**Files:**
- Modify: `internal/sandbox/nsjail_test.go` or `internal/sandbox/executor_test.go`

- [ ] **Step 1: Read existing sandbox tests to understand patterns**

Read `internal/sandbox/nsjail_test.go` and `internal/sandbox/policy_test.go` to understand existing patterns.

- [ ] **Step 2: Add TestBuildConfigContent if not covered**

Check if `buildConfigContent` (or equivalent) is already tested. If not, add tests for nsjail config generation.

- [ ] **Step 3: Add policy edge case tests**

Add tests for:
- Empty allow list (should allow all)
- Empty block list (should block nothing)
- Command in both allow and block list (block wins)
- Script with shell injection in various positions

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sandbox/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/
git commit -m "test(sandbox): add config generation and policy edge case tests"
```

---

## Phase 5: Collector/Processor/Config Test Supplements

### Task 14: Add Collector Manager and HostCollector tests

**Files:**
- Create: `internal/collector/manager_test.go`
- Create: `internal/collector/host_test.go`

- [ ] **Step 1: Write TestManagerCollectAll**

```go
package collector

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type mockCollector struct {
	name    string
	payload *MetricPayload
	err     error
}

func (m *mockCollector) Name() string { return m.name }
func (m *mockCollector) Collect(_ context.Context) (*MetricPayload, error) {
	return m.payload, m.err
}

func TestManagerCollectAll_Success(t *testing.T) {
	c1 := &mockCollector{name: "c1", payload: &MetricPayload{Collector: "c1", Timestamp: time.Now()}}
	c2 := &mockCollector{name: "c2", payload: &MetricPayload{Collector: "c2", Timestamp: time.Now()}}

	mgr := NewManager([]Collector{c1, c2})
	payloads, err := mgr.CollectAll(context.Background())
	if err != nil {
		t.Fatalf("CollectAll: %v", err)
	}
	if len(payloads) != 2 {
		t.Errorf("expected 2 payloads, got %d", len(payloads))
	}
}

func TestManagerCollectAll_PartialFailure(t *testing.T) {
	c1 := &mockCollector{name: "c1", payload: &MetricPayload{Collector: "c1", Timestamp: time.Now()}}
	c2 := &mockCollector{name: "c2", err: fmt.Errorf("collect failed")}

	mgr := NewManager([]Collector{c1, c2})
	payloads, err := mgr.CollectAll(context.Background())
	if err == nil {
		t.Fatal("expected error from partial failure")
	}
	if len(payloads) != 1 {
		t.Errorf("expected 1 successful payload, got %d", len(payloads))
	}
}

func TestManagerCollectAll_Empty(t *testing.T) {
	mgr := NewManager(nil)
	payloads, err := mgr.CollectAll(context.Background())
	if err == nil {
		t.Fatal("expected error for no collectors")
	}
	if len(payloads) != 0 {
		t.Errorf("expected 0 payloads, got %d", len(payloads))
	}
}
```

- [ ] **Step 2: Write TestHostCollectorCollect**

```go
func TestHostCollectorCollect(t *testing.T) {
	c := NewHostCollector("agent-1", "test-agent", time.Now())
	payload, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if payload.Collector != "host" {
		t.Errorf("Collector = %q, want %q", payload.Collector, "host")
	}
}

func TestHostCollectorName(t *testing.T) {
	c := NewHostCollector("agent-1", "test-agent", time.Now())
	if c.Name() != "host" {
		t.Errorf("Name() = %q, want %q", c.Name(), "host")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/collector/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/collector/manager_test.go internal/collector/host_test.go
git commit -m "test(collector): add Manager and HostCollector tests"
```

### Task 15: Add Processor edge case tests

**Files:**
- Modify: `internal/collector/processors/tagger/tagger_test.go`
- Modify: `internal/collector/processors/regex/regex_test.go`

- [ ] **Step 1: Read existing tests to avoid duplication**

Read both test files.

- [ ] **Step 2: Add tagger edge cases**

Add tests for:
- Nil metric in input slice
- Empty tags map
- Tag override (existing tag gets overwritten by static tag)
- Condition-based tagging (WhenName matching)

- [ ] **Step 3: Add regex edge cases**

Add tests for:
- No match (field unchanged)
- Multiple rules applied in order
- Invalid regex pattern in Init
- Empty input slice

- [ ] **Step 4: Run tests**

Run: `go test ./internal/collector/processors/... -race -v -count=1`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/collector/processors/
git commit -m "test(processors): add tagger and regex edge case tests"
```

### Task 16: Add Config boundary value tests

**Files:**
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Read existing tests**

Read `internal/config/config_test.go` to understand existing patterns.

- [ ] **Step 2: Add boundary value tests**

Add tests for:
- Port 0 (invalid)
- Port 65535 (valid boundary)
- Port 65536 (invalid)
- Port -1 (invalid)
- Empty agent ID
- Very long agent ID (>256 chars)
- reporter.mode=http with empty endpoint
- reporter.mode=stdout (endpoint not required)

- [ ] **Step 3: Run tests**

Run: `go test ./internal/config/ -race -v -count=1`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add internal/config/config_test.go
git commit -m "test(config): add boundary value and cross-field validation tests"
```

---

## Phase 6: Error Wrapping + Final Cleanup

### Task 17: Audit and fix error wrapping

**Files:**
- Various files across the codebase

- [ ] **Step 1: Find all fmt.Errorf with %v instead of %w**

Run: `grep -rn 'fmt.Errorf.*%v' internal/ --include='*.go' | grep -v _test.go | grep -v proto/`
Expected: list of files to fix

- [ ] **Step 2: Fix each occurrence**

For each occurrence, change `%v` to `%w` where the argument is an error. Do NOT change `%v` for non-error arguments.

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 4: Run full test suite**

Run: `go test -race ./...`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "fix: unify error wrapping to use %w for error chains"
```

### Task 18: Final verification

**Files:** None (verification only)

- [ ] **Step 1: Run full CI suite**

Run: `make ci`
Expected: tidy, vet, test-race, security all pass

- [ ] **Step 2: Check coverage**

Run: `go test -race -coverprofile=coverage.out ./...`
Run: `go tool cover -func=coverage.out | tail -1`
Expected: total >= 70.0%

- [ ] **Step 3: Check per-package coverage for key packages**

```bash
go test -race -coverprofile=app.out ./internal/app/ && go tool cover -func=app.out | tail -1
go test -race -coverprofile=grpc.out ./internal/grpcclient/ && go tool cover -func=grpc.out | tail -1
go test -race -coverprofile=server.out ./internal/server/ && go tool cover -func=server.out | tail -1
```

Expected: each >= 70%

- [ ] **Step 4: Verify no stale artifacts**

Run: `ls dist/ 2>&1`
Expected: "No such file or directory"

- [ ] **Step 5: Commit final state if any remaining changes**

```bash
git add -A
git status
# Only commit if there are changes.
```
