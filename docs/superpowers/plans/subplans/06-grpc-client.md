# Sub-Plan 6: gRPC Client

> **Parent:** [OpsAgent Full Implementation Plan](../2026-04-28-opsagent-full-implementation.md)
> **Depends on:** [Sub-Plan 1: Proto & gRPC Foundation](01-proto-grpc.md), [Sub-Plan 2: Collector Pipeline Core](02-collector-pipeline.md)

**Goal:** Implement the gRPC client with connection management, heartbeat, reconnection, message sending/receiving, and local metric cache.

**Files:**
- Create: `internal/grpcclient/client.go`
- Create: `internal/grpcclient/client_test.go`
- Create: `internal/grpcclient/sender.go`
- Create: `internal/grpcclient/sender_test.go`
- Create: `internal/grpcclient/receiver.go`
- Create: `internal/grpcclient/cache.go`
- Create: `internal/grpcclient/cache_test.go`

---

## Task 6.1: Ring Buffer Cache

- [ ] **Step 1: Write failing tests for Cache**

Create `internal/grpcclient/cache_test.go`:

```go
package grpcclient

import (
	"testing"
	"time"

	"opsagent/internal/collector"
)

func TestCacheAddAndDrain(t *testing.T) {
	cache := NewMetricCache(100)

	m1 := collector.NewMetric("cpu", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now())
	m2 := collector.NewMetric("mem", nil, map[string]interface{}{"v": 2.0}, collector.Gauge, time.Now())

	cache.Add(m1)
	cache.Add(m2)

	drained := cache.Drain()
	if len(drained) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(drained))
	}
	if drained[0].Name() != "cpu" {
		t.Fatalf("expected first=cpu, got %s", drained[0].Name())
	}

	// After drain, should be empty
	drained2 := cache.Drain()
	if len(drained2) != 0 {
		t.Fatalf("expected 0 after drain, got %d", len(drained2))
	}
}

func TestCacheOverflow(t *testing.T) {
	cache := NewMetricCache(3)

	for i := 0; i < 5; i++ {
		m := collector.NewMetric("test", nil, map[string]interface{}{"i": float64(i)}, collector.Gauge, time.Now())
		cache.Add(m)
	}

	drained := cache.Drain()
	if len(drained) != 3 {
		t.Fatalf("expected 3 (overflow), got %d", len(drained))
	}
	// Ring buffer: oldest dropped, so we get 2, 3, 4
	if drained[0].Fields()["i"].(float64) != 2.0 {
		t.Fatalf("expected first=2.0, got %v", drained[0].Fields()["i"])
	}
}

func TestCacheLen(t *testing.T) {
	cache := NewMetricCache(100)

	if cache.Len() != 0 {
		t.Fatalf("expected len 0, got %d", cache.Len())
	}

	cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now()))

	if cache.Len() != 1 {
		t.Fatalf("expected len 1, got %d", cache.Len())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/grpcclient/ -run TestCache -v
```

Expected: FAIL.

- [ ] **Step 3: Implement MetricCache**

Create `internal/grpcclient/cache.go`:

```go
package grpcclient

import (
	"sync"

	"opsagent/internal/collector"
)

// MetricCache is a ring buffer for caching metrics during disconnection.
type MetricCache struct {
	mu      sync.Mutex
	buf     []*collector.Metric
	maxSize int
	head    int
	tail    int
	count   int
}

// NewMetricCache creates a new MetricCache with the given max capacity.
func NewMetricCache(maxSize int) *MetricCache {
	return &MetricCache{
		buf:     make([]*collector.Metric, maxSize),
		maxSize: maxSize,
	}
}

// Add adds a metric to the cache. If full, overwrites the oldest entry.
func (c *MetricCache) Add(m *collector.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.buf[c.tail] = m
	c.tail = (c.tail + 1) % c.maxSize

	if c.count < c.maxSize {
		c.count++
	} else {
		// Overwrite oldest: advance head
		c.head = (c.head + 1) % c.maxSize
	}
}

// Drain returns all cached metrics and clears the cache.
func (c *MetricCache) Drain() []*collector.Metric {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.count == 0 {
		return nil
	}

	result := make([]*collector.Metric, c.count)
	for i := 0; i < c.count; i++ {
		idx := (c.head + i) % c.maxSize
		result[i] = c.buf[idx]
	}

	c.head = 0
	c.tail = 0
	c.count = 0

	return result
}

// Len returns the current number of cached metrics.
func (c *MetricCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/grpcclient/ -run TestCache -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grpcclient/cache.go internal/grpcclient/cache_test.go
git commit -m "feat(grpcclient): add ring buffer MetricCache for offline buffering"
```

---

## Task 6.2: Sender

- [ ] **Step 1: Write failing tests for Sender**

Create `internal/grpcclient/sender_test.go`:

```go
package grpcclient

import (
	"testing"
	"time"

	"opsagent/internal/collector"
	pb "opsagent/internal/grpcclient/proto"
)

func TestSenderBatchToProto(t *testing.T) {
	sender := &Sender{}

	metrics := []*collector.Metric{
		collector.NewMetric("cpu", map[string]string{"host": "n1"}, map[string]interface{}{"usage": 80.0}, collector.Gauge, time.Now()),
		collector.NewMetric("mem", map[string]string{"host": "n1"}, map[string]interface{}{"bytes": int64(1024)}, collector.Gauge, time.Now()),
	}

	batch := sender.metricsToBatch(metrics)

	if len(batch.Metrics) != 2 {
		t.Fatalf("expected 2 metrics in batch, got %d", len(batch.Metrics))
	}
	if batch.Metrics[0].Name != "cpu" {
		t.Fatalf("expected first metric name cpu, got %s", batch.Metrics[0].Name)
	}
}

func TestSenderEmptyBatch(t *testing.T) {
	sender := &Sender{}

	batch := sender.metricsToBatch(nil)

	if len(batch.Metrics) != 0 {
		t.Fatalf("expected 0 metrics, got %d", len(batch.Metrics))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/grpcclient/ -run TestSender -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Sender**

Create `internal/grpcclient/sender.go`:

```go
package grpcclient

import (
	"time"

	"opsagent/internal/collector"
	pb "opsagent/internal/grpcclient/proto"
)

// Sender handles converting and sending outbound messages.
type Sender struct{}

// MetricsToBatch converts collector metrics to a protobuf MetricBatch.
func (s *Sender) metricsToBatch(metrics []*collector.Metric) *pb.MetricBatch {
	pbMetrics := make([]*pb.Metric, 0, len(metrics))
	for _, m := range metrics {
		pbMetrics = append(pbMetrics, m.ToProto())
	}
	return &pb.MetricBatch{Metrics: pbMetrics}
}

// NewHeartbeat creates a Heartbeat message.
func (s *Sender) NewHeartbeat(agentID, status string, info *pb.AgentInfo) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				AgentId:    agentID,
				TimestampMs: time.Now().UnixMilli(),
				Status:     status,
				AgentInfo:  info,
			},
		},
	}
}

// NewMetricBatchMessage creates an AgentMessage with a MetricBatch payload.
func (s *Sender) NewMetricBatchMessage(metrics []*collector.Metric) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Metrics{
			Metrics: s.metricsToBatch(metrics),
		},
	}
}

// NewExecOutputMessage creates an AgentMessage with execution output.
func (s *Sender) NewExecOutputMessage(taskID, stream string, data []byte) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_ExecOutput{
			ExecOutput: &pb.ExecOutput{
				TaskId:      taskID,
				Stream:      stream,
				Data:        data,
				TimestampMs: time.Now().UnixMilli(),
			},
		},
	}
}

// NewExecResultMessage creates an AgentMessage with execution result.
func (s *Sender) NewExecResultMessage(result *ExecResult) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_ExecResult{
			ExecResult: result.ToProto(),
		},
	}
}

// NewRegistrationMessage creates an AgentMessage with registration.
func (s *Sender) NewRegistrationMessage(agentID, token string, info *pb.AgentInfo, caps []string) *pb.AgentMessage {
	return &pb.AgentMessage{
		Payload: &pb.AgentMessage_Registration{
			Registration: &pb.AgentRegistration{
				AgentId:      agentID,
				Token:        token,
				AgentInfo:    info,
				Capabilities: caps,
			},
		},
	}
}

// ExecResult is the local representation of an execution result.
type ExecResult struct {
	TaskID    string
	ExitCode  int32
	Duration  time.Duration
	TimedOut  bool
	Truncated bool
	Killed    bool
	Stats     ExecStats
}

// ExecStats contains resource usage statistics.
type ExecStats struct {
	PeakMemoryBytes int64
	CPUTimeUserMs   int64
	CPUTimeSystemMs int64
	ProcessCount    int32
	BytesWritten    int64
	BytesRead       int64
}

// ToProto converts ExecResult to protobuf.
func (r *ExecResult) ToProto() *pb.ExecResult {
	return &pb.ExecResult{
		TaskId:     r.TaskID,
		ExitCode:   r.ExitCode,
		DurationMs: r.Duration.Milliseconds(),
		TimedOut:   r.TimedOut,
		Truncated:  r.Truncated,
		Killed:     r.Killed,
		Stats: &pb.ExecStats{
			PeakMemoryBytes: r.Stats.PeakMemoryBytes,
			CpuTimeUserMs:   r.Stats.CPUTimeUserMs,
			CpuTimeSystemMs: r.Stats.CPUTimeSystemMs,
			ProcessCount:    r.Stats.ProcessCount,
			BytesWritten:    r.Stats.BytesWritten,
			BytesRead:       r.Stats.BytesRead,
		},
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/grpcclient/ -run TestSender -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grpcclient/sender.go internal/grpcclient/sender_test.go
git commit -m "feat(grpcclient): add Sender for outbound gRPC messages"
```

---

## Task 6.3: Receiver

- [ ] **Step 1: Implement Receiver**

Create `internal/grpcclient/receiver.go`:

```go
package grpcclient

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	pb "opsagent/internal/grpcclient/proto"
)

// CommandHandler is called when the platform sends an ExecuteCommand.
type CommandHandler func(ctx context.Context, cmd *pb.ExecuteCommand)

// ScriptHandler is called when the platform sends an ExecuteScript.
type ScriptHandler func(ctx context.Context, script *pb.ExecuteScript)

// CancelHandler is called when the platform sends a CancelJob.
type CancelHandler func(ctx context.Context, cancel *pb.CancelJob)

// ConfigUpdateHandler is called when the platform sends a ConfigUpdate.
type ConfigUpdateHandler func(ctx context.Context, update *pb.ConfigUpdate)

// Receiver handles incoming PlatformMessage dispatch.
type Receiver struct {
	logger         zerolog.Logger
	onCommand      CommandHandler
	onScript       ScriptHandler
	onCancel       CancelHandler
	onConfigUpdate ConfigUpdateHandler
}

// NewReceiver creates a new Receiver.
func NewReceiver(logger zerolog.Logger) *Receiver {
	return &Receiver{logger: logger}
}

// SetCommandHandler sets the handler for ExecuteCommand messages.
func (r *Receiver) SetCommandHandler(h CommandHandler) { r.onCommand = h }

// SetScriptHandler sets the handler for ExecuteScript messages.
func (r *Receiver) SetScriptHandler(h ScriptHandler) { r.onScript = h }

// SetCancelHandler sets the handler for CancelJob messages.
func (r *Receiver) SetCancelHandler(h CancelHandler) { r.onCancel = h }

// SetConfigUpdateHandler sets the handler for ConfigUpdate messages.
func (r *Receiver) SetConfigUpdateHandler(h ConfigUpdateHandler) { r.onConfigUpdate = h }

// Handle dispatches a PlatformMessage to the appropriate handler.
func (r *Receiver) Handle(ctx context.Context, msg *pb.PlatformMessage) error {
	switch p := msg.Payload.(type) {
	case *pb.PlatformMessage_ExecCommand:
		if r.onCommand != nil {
			r.onCommand(ctx, p.ExecCommand)
		}
	case *pb.PlatformMessage_ExecScript:
		if r.onScript != nil {
			r.onScript(ctx, p.ExecScript)
		}
	case *pb.PlatformMessage_CancelJob:
		if r.onCancel != nil {
			r.onCancel(ctx, p.CancelJob)
		}
	case *pb.PlatformMessage_ConfigUpdate:
		if r.onConfigUpdate != nil {
			r.onConfigUpdate(ctx, p.ConfigUpdate)
		}
	case *pb.PlatformMessage_Ack:
		r.logger.Debug().Str("ref_id", p.Ack.RefId).Bool("success", p.Ack.Success).Msg("received ack")
	default:
		return fmt.Errorf("unknown platform message type: %T", msg.Payload)
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/grpcclient/...
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/grpcclient/receiver.go
git commit -m "feat(grpcclient): add Receiver for inbound message dispatch"
```

---

## Task 6.4: gRPC Client (Connection Manager)

- [ ] **Step 1: Write failing test for Client**

Create `internal/grpcclient/client_test.go`:

```go
package grpcclient

import (
	"testing"

	"opsagent/internal/collector"
)

func TestClientConfig(t *testing.T) {
	cfg := Config{
		ServerAddr:       "localhost:50051",
		AgentID:          "test-agent",
		HeartbeatSeconds: 30,
		ReconnectMaxSec:  60,
		CacheMaxSize:     10000,
	}

	client := NewClient(cfg, nil, nil) // nil logger and receiver for test

	if client.cfg.AgentID != "test-agent" {
		t.Fatalf("expected agent ID test-agent, got %s", client.cfg.AgentID)
	}
	if client.cache == nil {
		t.Fatal("expected cache to be initialized")
	}
}

func TestClientDefaultConfig(t *testing.T) {
	cfg := Config{}

	client := NewClient(cfg, nil, nil)

	if client.cfg.HeartbeatSeconds != 30 {
		t.Fatalf("expected default heartbeat 30, got %d", client.cfg.HeartbeatSeconds)
	}
	if client.cfg.ReconnectMaxSec != 60 {
		t.Fatalf("expected default reconnect max 60, got %d", client.cfg.ReconnectMaxSec)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/grpcclient/ -run TestClient -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Client**

Create `internal/grpcclient/client.go`:

```go
package grpcclient

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"opsagent/internal/collector"
	pb "opsagent/internal/grpcclient/proto"
)

// Config holds gRPC client configuration.
type Config struct {
	ServerAddr        string
	AgentID           string
	EnrollmentToken   string
	CertPath          string
	KeyPath           string
	CAPath            string
	HeartbeatSeconds  int
	ReconnectMaxSec   int
	CacheMaxSize      int
	FlushIntervalSec  int
	Capabilities      []string
}

// Client manages the gRPC connection to the platform.
type Client struct {
	cfg      Config
	logger   zerolog.Logger
	receiver *Receiver
	sender   *Sender
	cache    *MetricCache

	conn   *grpc.ClientConn
	stream pb.AgentService_ConnectClient
	mu     sync.Mutex

	cancel context.CancelFunc
	wg     sync.WaitGroup

	connected bool
}

// NewClient creates a new gRPC Client.
func NewClient(cfg Config, logger zerolog.Logger, receiver *Receiver) *Client {
	if cfg.HeartbeatSeconds <= 0 {
		cfg.HeartbeatSeconds = 30
	}
	if cfg.ReconnectMaxSec <= 0 {
		cfg.ReconnectMaxSec = 60
	}
	if cfg.CacheMaxSize <= 0 {
		cfg.CacheMaxSize = 10000
	}
	if cfg.FlushIntervalSec <= 0 {
		cfg.FlushIntervalSec = 10
	}

	return &Client{
		cfg:      cfg,
		logger:   logger,
		receiver: receiver,
		sender:   &Sender{},
		cache:    NewMetricCache(cfg.CacheMaxSize),
	}
}

// Start starts the gRPC client connection and background goroutines.
func (c *Client) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)

	c.wg.Add(1)
	go c.connectLoop(ctx)

	return nil
}

// Stop stops the gRPC client gracefully.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
	}
}

// SendMetrics sends metrics to the platform, caching them if disconnected.
func (c *Client) SendMetrics(metrics []*collector.Metric) {
	if len(metrics) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream == nil || !c.connected {
		// Cache metrics for later replay
		for _, m := range metrics {
			c.cache.Add(m)
		}
		return
	}

	msg := c.sender.NewMetricBatchMessage(metrics)
	if err := c.stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("failed to send metrics, caching")
		for _, m := range metrics {
			c.cache.Add(m)
		}
		c.connected = false
	}
}

// SendExecOutput sends execution output to the platform.
func (c *Client) SendExecOutput(taskID, stream string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream == nil || !c.connected {
		return
	}

	msg := c.sender.NewExecOutputMessage(taskID, stream, data)
	if err := c.stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Str("task_id", taskID).Msg("failed to send exec output")
	}
}

// SendExecResult sends execution result to the platform.
func (c *Client) SendExecResult(result *ExecResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream == nil || !c.connected {
		return
	}

	msg := c.sender.NewExecResultMessage(result)
	if err := c.stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Str("task_id", result.TaskID).Msg("failed to send exec result")
	}
}

// IsConnected returns whether the client is currently connected to the platform.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *Client) connectLoop(ctx context.Context) {
	defer c.wg.Done()

	backoff := time.Second
	maxBackoff := time.Duration(c.cfg.ReconnectMaxSec) * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := c.connect(ctx)
		if err != nil {
			c.logger.Warn().Err(err).Dur("backoff", backoff).Msg("connection failed, retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Reset backoff on successful connection
		backoff = time.Second

		// Run the message loop
		c.messageLoop(ctx)

		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()

		c.logger.Info().Msg("disconnected, will reconnect")
	}
}

func (c *Client) connect(ctx context.Context) error {
	c.mu.Lock()
	addr := c.cfg.ServerAddr
	c.mu.Unlock()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithInsecure()) // TODO: add mTLS
	if err != nil {
		return err
	}

	client := pb.NewAgentServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		conn.Close()
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.stream = stream
	c.connected = true
	c.mu.Unlock()

	c.logger.Info().Str("addr", addr).Msg("connected to platform")

	// Send registration
	info := c.buildAgentInfo()
	reg := c.sender.NewRegistrationMessage(c.cfg.AgentID, c.cfg.EnrollmentToken, info, c.cfg.Capabilities)
	if err := stream.Send(reg); err != nil {
		conn.Close()
		return err
	}

	// Replay cached metrics
	c.replayCache()

	return nil
}

func (c *Client) messageLoop(ctx context.Context) {
	heartbeatTicker := time.NewTicker(time.Duration(c.cfg.HeartbeatSeconds) * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			c.sendHeartbeat()
		default:
		}

		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()

		if stream == nil {
			return
		}

		msg, err := stream.Recv()
		if err != nil {
			c.logger.Warn().Err(err).Msg("stream recv error")
			return
		}

		if c.receiver != nil {
			if err := c.receiver.Handle(ctx, msg); err != nil {
				c.logger.Warn().Err(err).Msg("error handling platform message")
			}
		}
	}
}

func (c *Client) sendHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream == nil || !c.connected {
		return
	}

	info := c.buildAgentInfo()
	msg := c.sender.NewHeartbeat(c.cfg.AgentID, "ready", info)
	if err := c.stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("failed to send heartbeat")
		c.connected = false
	}
}

func (c *Client) replayCache() {
	cached := c.cache.Drain()
	if len(cached) == 0 {
		return
	}

	c.logger.Info().Int("count", len(cached)).Msg("replaying cached metrics")

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream == nil || !c.connected {
		return
	}

	msg := c.sender.NewMetricBatchMessage(cached)
	if err := c.stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("failed to replay cached metrics")
	}
}

func (c *Client) buildAgentInfo() *pb.AgentInfo {
	// Basic info; will be enhanced with actual system info
	return &pb.AgentInfo{
		Hostname: "localhost",
		Os:       "linux",
		Arch:     "amd64",
		Version:  "0.1.0",
	}
}

// GetConnState returns the gRPC connection state for health checks.
func (c *Client) GetConnState() connectivity.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return connectivity.Shutdown
	}
	return c.conn.GetState()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/grpcclient/ -run TestClient -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/grpcclient/client.go internal/grpcclient/client_test.go
git commit -m "feat(grpcclient): add gRPC Client with connection, heartbeat, reconnection, cache"
```
