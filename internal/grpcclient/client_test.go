package grpcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	creds, err := c.buildTLSCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds == nil {
		t.Fatal("expected non-nil credentials")
	}
	// Should be insecure when no TLS config is set.
	if creds.Info().SecurityProtocol != "insecure" {
		t.Errorf("expected insecure protocol, got %s", creds.Info().SecurityProtocol)
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
