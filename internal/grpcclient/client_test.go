package grpcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/metadata"

	"github.com/cy77cc/opsagent/internal/collector"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

// mockConnectStream implements grpc.BidiStreamingClient[pb.AgentMessage, pb.PlatformMessage]
// for testing purposes.
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

func TestClientDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HeartbeatSeconds != 30 {
		t.Errorf("expected HeartbeatSeconds=30, got %d", cfg.HeartbeatSeconds)
	}
	if cfg.ReconnectMaxSec != 60 {
		t.Errorf("expected ReconnectMaxSec=60, got %d", cfg.ReconnectMaxSec)
	}
	if cfg.CacheMaxSize != 10000 {
		t.Errorf("expected CacheMaxSize=10000, got %d", cfg.CacheMaxSize)
	}
	if cfg.FlushIntervalSec != 10 {
		t.Errorf("expected FlushIntervalSec=10, got %d", cfg.FlushIntervalSec)
	}
}

func TestClientConfig(t *testing.T) {
	cfg := Config{
		ServerAddr:       "localhost:50051",
		AgentID:          "agent-test",
		EnrollmentToken:  "tok",
		HeartbeatSeconds: 15,
		ReconnectMaxSec:  120,
		CacheMaxSize:     500,
		FlushIntervalSec: 5,
		Capabilities:     []string{"exec", "metrics"},
	}

	logger := zerolog.Nop()
	receiver := NewReceiver(logger)
	c := NewClient(cfg, logger, receiver)

	if c.cfg.HeartbeatSeconds != 15 {
		t.Errorf("expected 15, got %d", c.cfg.HeartbeatSeconds)
	}
	if c.cfg.CacheMaxSize != 500 {
		t.Errorf("expected 500, got %d", c.cfg.CacheMaxSize)
	}
	if c.cfg.AgentID != "agent-test" {
		t.Errorf("expected agent-test, got %s", c.cfg.AgentID)
	}
}

func TestClientNilLogger(t *testing.T) {
	cfg := DefaultConfig()
	// zerolog.Logger zero value should work as a no-op logger.
	c := NewClient(cfg, zerolog.Logger{}, nil)
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.cache == nil {
		t.Fatal("expected non-nil cache")
	}
}

func TestClientNotConnectedByDefault(t *testing.T) {
	cfg := DefaultConfig()
	c := NewClient(cfg, zerolog.Nop(), nil)
	if c.IsConnected() {
		t.Error("expected not connected")
	}
}

func TestClientDefaultConfigNormalization(t *testing.T) {
	cfg := Config{} // all zeros
	c := NewClient(cfg, zerolog.Nop(), nil)
	if c.cfg.HeartbeatSeconds != 30 {
		t.Errorf("expected 30, got %d", c.cfg.HeartbeatSeconds)
	}
	if c.cfg.ReconnectMaxSec != 60 {
		t.Errorf("expected 60, got %d", c.cfg.ReconnectMaxSec)
	}
	if c.cfg.CacheMaxSize != 10000 {
		t.Errorf("expected 10000, got %d", c.cfg.CacheMaxSize)
	}
	if c.cfg.FlushIntervalSec != 10 {
		t.Errorf("expected 10, got %d", c.cfg.FlushIntervalSec)
	}
}

func TestBuildAgentInfo(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	info := c.buildAgentInfo()

	if info.Hostname == "" {
		t.Error("expected non-empty hostname")
	}
	if info.Os == "" {
		t.Error("expected non-empty OS")
	}
	if info.Arch == "" {
		t.Error("expected non-empty arch")
	}
	if info.CpuCores <= 0 {
		t.Errorf("expected positive CPU cores, got %d", info.CpuCores)
	}
	if info.MemoryBytes <= 0 {
		t.Errorf("expected positive memory bytes, got %d", info.MemoryBytes)
	}
}

func TestBuildTLSCredentials_NoConfig(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Fatal("expected error when no TLS certificates configured")
	}
	if !strings.Contains(err.Error(), "no TLS certificates configured") {
		t.Errorf("expected 'no TLS certificates configured' in error, got: %v", err)
	}
}

func TestBuildTLSCredentials_InvalidCAPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CAPath = "/nonexistent/ca.pem"
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for nonexistent CA file")
	}
}

func TestBuildTLSCredentials_InvalidCertPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CertPath = "/nonexistent/cert.pem"
	cfg.KeyPath = "/nonexistent/key.pem"
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for nonexistent cert files")
	}
}

func TestClientSendMetrics_CachesWhenDisconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	m := collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now())
	c.SendMetrics([]*collector.Metric{m})

	if got := c.cache.Len(); got != 1 {
		t.Errorf("expected cache length 1, got %d", got)
	}
}

func TestClientSendMetrics_EmptySlice(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// nil slice — should not panic
	c.SendMetrics(nil)
	if got := c.cache.Len(); got != 0 {
		t.Errorf("expected cache length 0 after nil send, got %d", got)
	}

	// empty slice — should not panic
	c.SendMetrics([]*collector.Metric{})
	if got := c.cache.Len(); got != 0 {
		t.Errorf("expected cache length 0 after empty send, got %d", got)
	}
}

func TestClientReplayCache_Empty(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// replayCache with empty cache and nil stream should not panic.
	c.replayCache()
}

func TestClientReplayCache_SendsBatch(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// Add metrics to cache.
	for i := 0; i < 5; i++ {
		m := collector.NewMetric(
			fmt.Sprintf("metric_%d", i),
			nil,
			map[string]interface{}{"v": float64(i)},
			collector.Gauge,
			time.Now(),
		)
		c.cache.Add(m)
	}

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	c.replayCache()

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
	if got := c.cache.Len(); got != 0 {
		t.Errorf("expected cache drained (len=0), got %d", got)
	}
}

func TestClientReplayCache_SendFailureRecaches(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// Add metrics to cache.
	for i := 0; i < 3; i++ {
		m := collector.NewMetric(
			fmt.Sprintf("metric_%d", i),
			nil,
			map[string]interface{}{"v": float64(i)},
			collector.Gauge,
			time.Now(),
		)
		c.cache.Add(m)
	}

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	c.replayCache()

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
	// Metrics should be re-cached after failure.
	if got := c.cache.Len(); got != 3 {
		t.Errorf("expected 3 metrics re-cached, got %d", got)
	}
}

func TestClientStop_Idempotent(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	// Calling Stop twice without Start should not panic.
	c.Stop()
	c.Stop()
}

func TestClientStartStop(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatalf("unexpected error from Start: %v", err)
	}

	// Cancel context and stop — should not hang or panic.
	cancel()
	c.Stop()
}

func TestFlushAndStop_PersistsOnStreamUnavailable(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)
	// Add metrics to cache without starting connection.
	for i := 0; i < 5; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	tmpFile := t.TempDir() + "/cache.json"
	err := c.FlushAndStop(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("FlushAndStop failed: %v", err)
	}

	// Verify persisted file exists and has metrics.
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read persisted cache: %v", err)
	}
	var metrics []*collector.Metric
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatalf("failed to unmarshal persisted cache: %v", err)
	}
	if len(metrics) != 5 {
		t.Errorf("persisted %d metrics, want 5", len(metrics))
	}
}

func TestHealthStatus_Disconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	status := c.HealthStatus()
	if status.Status != "disconnected" {
		t.Errorf("expected disconnected, got %s", status.Status)
	}
}

func TestHealthStatus_Connected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()

	status := c.HealthStatus()
	if status.Status != "connected" {
		t.Errorf("expected connected, got %s", status.Status)
	}
}

func TestSendExecOutput_DropsWhenDisconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Should not panic when disconnected.
	c.SendExecOutput("task-1", "stdout", []byte("hello"))
}

func TestSendExecOutput_SendsWhenConnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	c.SendExecOutput("task-1", "stdout", []byte("output"))

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestSendExecOutput_SendFailure(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	// Should not panic on send failure.
	c.SendExecOutput("task-1", "stderr", []byte("err"))

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestSendExecResult_DropsWhenDisconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	result := &ExecResult{TaskID: "task-1", ExitCode: 0}
	// Should not panic when disconnected.
	c.SendExecResult(result)
}

func TestSendExecResult_SendsWhenConnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	result := &ExecResult{TaskID: "task-2", ExitCode: 1, Duration: 3 * time.Second}
	c.SendExecResult(result)

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestSendExecResult_SendFailure(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	result := &ExecResult{TaskID: "task-3", ExitCode: 0}
	// Should not panic on send failure.
	c.SendExecResult(result)

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestSetOnStateChange(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	var called bool
	c.SetOnStateChange(func(connected bool) {
		called = true
	})

	// Trigger a state change via setConnected.
	c.setConnected(true)

	if !called {
		t.Error("expected onStateChange callback to be called")
	}
}

func TestSetConnected_NoDuplicateCallback(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	callCount := 0
	c.SetOnStateChange(func(connected bool) {
		callCount++
	})

	// Setting same state should not trigger callback.
	c.setConnected(false) // already false by default
	if callCount != 0 {
		t.Errorf("expected 0 callback calls, got %d", callCount)
	}

	// Setting to true should trigger.
	c.setConnected(true)
	if callCount != 1 {
		t.Errorf("expected 1 callback call, got %d", callCount)
	}

	// Setting to true again should not trigger.
	c.setConnected(true)
	if callCount != 1 {
		t.Errorf("expected 1 callback call, got %d", callCount)
	}

	// Setting back to false should trigger.
	c.setConnected(false)
	if callCount != 2 {
		t.Errorf("expected 2 callback calls, got %d", callCount)
	}
}

func TestSetConnected_NilCallback(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Should not panic with nil callback.
	c.setConnected(true)
	c.setConnected(false)
}

func TestSendMetrics_SendsWhenConnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	m := collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now())
	c.SendMetrics([]*collector.Metric{m})

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
	if got := c.cache.Len(); got != 0 {
		t.Errorf("expected cache empty after successful send, got %d", got)
	}
}

func TestSendMetrics_CachesOnSendFailure(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	m := collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now())
	c.SendMetrics([]*collector.Metric{m})

	if got := c.cache.Len(); got != 1 {
		t.Errorf("expected 1 cached metric after send failure, got %d", got)
	}
}

func TestBuildTLSCredentials_InvalidCAPEM(t *testing.T) {
	// Write a file that looks like PEM but contains garbage.
	tmpFile := t.TempDir() + "/bad-ca.pem"
	os.WriteFile(tmpFile, []byte("not a valid PEM certificate"), 0644)

	cfg := DefaultConfig()
	cfg.CAPath = tmpFile
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for invalid PEM CA file")
	}
}

func TestBuildTLSCredentials_CAOnlyPath(t *testing.T) {
	// When only CAPath is set (no client cert), should build server-verified TLS.
	// Use a self-signed CA PEM for this test. Since we can't easily generate one,
	// we test that setting only CAPath without CertPath/KeyPath does not error
	// on the cert loading path (it will error on CA file read if nonexistent).
	cfg := DefaultConfig()
	cfg.CAPath = "/nonexistent/ca.pem"
	cfg.CertPath = ""
	cfg.KeyPath = ""
	c := NewClient(cfg, zerolog.Nop(), nil)
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Error("expected error for nonexistent CA file with CA-only path")
	}
}

func TestBuildTLSCredentials_RejectsInsecureFallback(t *testing.T) {
	c := &Client{
		cfg: Config{
			CertPath: "",
			KeyPath:  "",
			CAPath:   "",
		},
	}
	_, err := c.buildTLSCredentials()
	if err == nil {
		t.Fatal("expected error when no TLS certificates configured")
	}
	if !strings.Contains(err.Error(), "no TLS certificates configured") {
		t.Errorf("expected 'no TLS certificates configured' in error, got: %v", err)
	}
}

func TestExtractServerName(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"platform.example.com:443", "platform.example.com"},
		{"10.0.0.1:8443", "10.0.0.1"},
		{"localhost:9090", "localhost"},
		{"no-port", "no-port"},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			got := extractServerName(tc.addr)
			if got != tc.want {
				t.Errorf("extractServerName(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestConnect_InvalidServerAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ServerAddr = "not-a-valid-address:99999"
	c := NewClient(cfg, zerolog.Nop(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.connect(ctx)
	if err == nil {
		t.Error("expected error connecting to invalid address")
	}
}

func TestConnect_EmptyServerAddress(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ServerAddr = ""
	c := NewClient(cfg, zerolog.Nop(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := c.connect(ctx)
	// grpc.NewClient with empty address may or may not fail immediately,
	// but the Connect stream call should fail.
	if err == nil {
		t.Error("expected error connecting to empty address")
	}
}

func TestCloseConn_WithMockStream(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	c.closeConn()

	c.mu.Lock()
	connected := c.connected
	stream := c.stream
	c.mu.Unlock()

	if connected {
		t.Error("expected connected=false after closeConn")
	}
	if stream != nil {
		t.Error("expected nil stream after closeConn")
	}
}

func TestCloseConn_Idempotent(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Calling closeConn multiple times should not panic.
	c.closeConn()
	c.closeConn()
}

func TestSendHeartbeat_WhenConnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	c.sendHeartbeat()

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestSendHeartbeat_WhenDisconnected(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	// Should not panic or send when disconnected.
	c.sendHeartbeat()
}

func TestSendHeartbeat_SendFailure(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	// Should not panic on send failure.
	c.sendHeartbeat()

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestMessageLoop_EOF(t *testing.T) {
	c := NewClient(Config{HeartbeatSeconds: 1}, zerolog.Nop(), nil)

	mock := &mockConnectStream{
		recvFn: func() (*pb.PlatformMessage, error) {
			return nil, io.EOF
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// messageLoop should return after EOF.
	c.messageLoop(ctx)

	if c.IsConnected() {
		t.Error("expected disconnected after EOF")
	}
}

func TestMessageLoop_RecvError(t *testing.T) {
	c := NewClient(Config{HeartbeatSeconds: 1}, zerolog.Nop(), nil)

	mock := &mockConnectStream{
		recvFn: func() (*pb.PlatformMessage, error) {
			return nil, fmt.Errorf("recv error")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.messageLoop(ctx)

	if c.IsConnected() {
		t.Error("expected disconnected after recv error")
	}
}

func TestMessageLoop_ContextCancelled(t *testing.T) {
	c := NewClient(Config{HeartbeatSeconds: 1}, zerolog.Nop(), nil)

	// recv blocks until the signal channel is closed, simulating a long-running recv.
	recvBlock := make(chan struct{})
	mock := &mockConnectStream{
		recvFn: func() (*pb.PlatformMessage, error) {
			<-recvBlock
			return nil, io.EOF
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.messageLoop(ctx)
		close(done)
	}()

	// Cancel context and unblock recv so the loop can exit.
	cancel()
	close(recvBlock)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("messageLoop did not return after context cancel")
	}
}

func TestMessageLoop_NilStream(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	c.mu.Lock()
	c.stream = nil
	c.connected = true
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c.messageLoop(ctx)

	if c.IsConnected() {
		t.Error("expected disconnected when stream is nil")
	}
}

func TestFlushAndStop_EmptyCache(t *testing.T) {
	c := NewClient(DefaultConfig(), zerolog.Nop(), nil)
	err := c.FlushAndStop(context.Background(), "")
	if err != nil {
		t.Fatalf("FlushAndStop failed: %v", err)
	}
}

func TestFlushAndStop_SendsBatchWithStream(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)

	for i := 0; i < 5; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	mock := &mockConnectStream{}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	err := c.FlushAndStop(context.Background(), "")
	if err != nil {
		t.Fatalf("FlushAndStop failed: %v", err)
	}

	if got := mock.sendCount.Load(); got != 1 {
		t.Errorf("expected 1 send call, got %d", got)
	}
}

func TestFlushAndStop_NoPersistPathWithRemainingMetrics(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)

	for i := 0; i < 3; i++ {
		c.cache.Add(collector.NewMetric("test", nil, map[string]interface{}{"v": float64(i)}, collector.Gauge, time.Now()))
	}

	mock := &mockConnectStream{
		sendFn: func(*pb.AgentMessage) error {
			return fmt.Errorf("send failed")
		},
	}
	c.mu.Lock()
	c.stream = mock
	c.connected = true
	c.mu.Unlock()

	// No persist path -- metrics are lost but should not panic.
	err := c.FlushAndStop(context.Background(), "")
	if err != nil {
		t.Fatalf("FlushAndStop failed: %v", err)
	}
}

func TestLoadPersistedCache_InvalidJSON(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)

	tmpFile := t.TempDir() + "/bad-cache.json"
	os.WriteFile(tmpFile, []byte("not valid json"), 0644)

	c.loadPersistedCache(tmpFile)

	// Cache should remain empty since JSON was invalid.
	if c.cache.Len() != 0 {
		t.Errorf("expected cache empty after invalid JSON, got %d", c.cache.Len())
	}

	// File should be removed.
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("invalid cache file should be removed")
	}
}

func TestLoadPersistedCache_NonExistentFile(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)
	// Should not panic for a file that doesn't exist.
	c.loadPersistedCache("/nonexistent/path/cache.json")
}

func TestLoadPersistedCache(t *testing.T) {
	c := NewClient(Config{CacheMaxSize: 100}, zerolog.Nop(), nil)

	// Create a persisted cache file.
	metrics := []*collector.Metric{
		collector.NewMetric("test", nil, map[string]interface{}{"v": 1.0}, collector.Gauge, time.Now()),
	}
	data, _ := json.Marshal(metrics)
	tmpFile := t.TempDir() + "/cache.json"
	os.WriteFile(tmpFile, data, 0644)

	c.loadPersistedCache(tmpFile)

	if c.cache.Len() != 1 {
		t.Errorf("cache len = %d, want 1", c.cache.Len())
	}

	// File should be removed after loading.
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("persisted cache file should be removed after loading")
	}
}
