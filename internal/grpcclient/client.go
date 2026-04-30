package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/mem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cy77cc/opsagent/internal/collector"
	"github.com/cy77cc/opsagent/internal/health"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

// Config holds configuration for the gRPC client.
type Config struct {
	ServerAddr       string
	AgentID          string
	EnrollmentToken  string
	CertPath         string
	KeyPath          string
	CAPath           string
	HeartbeatSeconds int
	ReconnectMaxSec  int
	CacheMaxSize     int
	FlushIntervalSec int
	Capabilities     []string
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		HeartbeatSeconds: 30,
		ReconnectMaxSec:  60,
		CacheMaxSize:     10000,
		FlushIntervalSec: 10,
	}
}

// Client manages a bidirectional gRPC stream to the platform.
type Client struct {
	cfg       Config
	logger    zerolog.Logger
	receiver  *Receiver
	cache     *MetricCache
	conn      *grpc.ClientConn
	stream    pb.AgentService_ConnectClient
	mu        sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	connected     bool
	onStateChange func(connected bool)
}

// NewClient creates a Client. If logger is nil, a no-op logger is used.
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

	// zerolog.Logger zero-value is a no-op logger; no special handling needed.

	return &Client{
		cfg:      cfg,
		logger:   logger,
		receiver: receiver,
		cache:    NewMetricCache(cfg.CacheMaxSize),
	}
}

// Start begins the connection loop in a background goroutine.
func (c *Client) Start(ctx context.Context) error {
	ctx, c.cancel = context.WithCancel(ctx)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.connectLoop(ctx)
	}()
	return nil
}

// Stop cancels the connection loop and waits for it to finish.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	c.closeConn()
}

// SendMetrics sends metrics to the platform or caches them if disconnected.
func (c *Client) SendMetrics(metrics []*collector.Metric) {
	if len(metrics) == 0 {
		return
	}

	c.mu.Lock()
	connected := c.connected
	stream := c.stream
	c.mu.Unlock()

	if !connected || stream == nil {
		for _, m := range metrics {
			c.cache.Add(m)
		}
		return
	}

	msg := NewMetricBatchMessage(metrics)
	if err := stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("failed to send metrics, caching")
		for _, m := range metrics {
			c.cache.Add(m)
		}
	}
}

// SendExecOutput sends execution output to the platform.
func (c *Client) SendExecOutput(taskID, streamName string, data []byte) {
	c.mu.Lock()
	stream := c.stream
	connected := c.connected
	c.mu.Unlock()

	if !connected || stream == nil {
		c.logger.Warn().Str("task_id", taskID).Msg("not connected, dropping exec output")
		return
	}

	msg := NewExecOutputMessage(taskID, streamName, data)
	if err := stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Str("task_id", taskID).Msg("failed to send exec output")
	}
}

// SendExecResult sends an execution result to the platform.
func (c *Client) SendExecResult(result *ExecResult) {
	c.mu.Lock()
	stream := c.stream
	connected := c.connected
	c.mu.Unlock()

	if !connected || stream == nil {
		c.logger.Warn().Str("task_id", result.TaskID).Msg("not connected, dropping exec result")
		return
	}

	msg := NewExecResultMessage(result)
	if err := stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Str("task_id", result.TaskID).Msg("failed to send exec result")
	}
}

// IsConnected returns true if the client has an active stream.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// SetOnStateChange registers a callback for connection state changes.
func (c *Client) SetOnStateChange(fn func(connected bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// HealthStatus reports the gRPC client connection health.
func (c *Client) HealthStatus() health.Status {
	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()
	status := "disconnected"
	if connected {
		status = "connected"
	}
	return health.Status{Status: status}
}

// connectLoop attempts to connect with exponential backoff.
func (c *Client) connectLoop(ctx context.Context) {
	for {
		if err := c.connect(ctx); err != nil {
			c.logger.Error().Err(err).Msg("connection failed")
		}

		// Exponential backoff: 1s, 2s, 4s, ... up to ReconnectMaxSec.
		backoff := time.Second
		maxBackoff := time.Duration(c.cfg.ReconnectMaxSec) * time.Second

		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			if err := c.connect(ctx); err != nil {
				c.logger.Error().Err(err).Msg("reconnect failed")
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				continue
			}
			break
		}

		// Connected — run message loop until it returns.
		c.messageLoop(ctx)

		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// connect dials the server, creates a stream, sends registration, and replays cache.
func (c *Client) connect(ctx context.Context) error {
	c.logger.Info().Str("addr", c.cfg.ServerAddr).Msg("connecting to platform")

	creds, err := c.buildTLSCredentials()
	if err != nil {
		return fmt.Errorf("build TLS credentials: %w", err)
	}

	conn, err := grpc.NewClient(c.cfg.ServerAddr,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.cfg.ServerAddr, err)
	}

	client := pb.NewAgentServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("connect stream: %w", err)
	}

	// Send registration.
	info := c.buildAgentInfo()
	regMsg := NewRegistrationMessage(c.cfg.AgentID, c.cfg.EnrollmentToken, info, c.cfg.Capabilities)
	if err := stream.Send(regMsg); err != nil {
		conn.Close()
		return fmt.Errorf("send registration: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.stream = stream
	c.connected = true
	onStateChange := c.onStateChange
	c.mu.Unlock()

	if onStateChange != nil {
		onStateChange(true)
	}

	c.logger.Info().Msg("connected to platform")

	// Replay cached metrics.
	c.replayCache()

	return nil
}

// messageLoop runs heartbeat + recv until the context is cancelled or stream errors.
func (c *Client) messageLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(c.cfg.HeartbeatSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.setConnected(false)
			return
		case <-ticker.C:
			c.sendHeartbeat()
		default:
		}

		// Non-blocking check then recv.
		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()

		if stream == nil {
			c.setConnected(false)
			return
		}

		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				c.logger.Warn().Msg("stream closed by server")
			} else {
				c.logger.Error().Err(err).Msg("stream recv error")
			}
			c.setConnected(false)
			c.closeConn()
			return
		}

		if c.receiver != nil {
			if err := c.receiver.Handle(ctx, msg); err != nil {
				c.logger.Error().Err(err).Msg("handler error")
			}
		}
	}
}

// sendHeartbeat sends a heartbeat message on the stream.
func (c *Client) sendHeartbeat() {
	c.mu.Lock()
	stream := c.stream
	connected := c.connected
	c.mu.Unlock()

	if !connected || stream == nil {
		return
	}

	info := c.buildAgentInfo()
	msg := NewHeartbeat(c.cfg.AgentID, "running", info)
	if err := stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("heartbeat send failed")
	}
}

// replayCache drains the cache and sends all metrics.
func (c *Client) replayCache() {
	metrics := c.cache.Drain()
	if len(metrics) == 0 {
		return
	}
	c.logger.Info().Int("count", len(metrics)).Msg("replaying cached metrics")

	c.mu.Lock()
	stream := c.stream
	c.mu.Unlock()

	if stream == nil {
		return
	}

	msg := NewMetricBatchMessage(metrics)
	if err := stream.Send(msg); err != nil {
		c.logger.Warn().Err(err).Msg("cache replay failed, re-caching")
		for _, m := range metrics {
			c.cache.Add(m)
		}
	}
}

// buildAgentInfo constructs the AgentInfo protobuf from system data.
func (c *Client) buildAgentInfo() *pb.AgentInfo {
	hostname, _ := os.Hostname()
	var memBytes int64
	if v, err := mem.VirtualMemory(); err == nil {
		memBytes = int64(v.Total)
	}
	return &pb.AgentInfo{
		Hostname:    hostname,
		Os:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		CpuCores:    int32(runtime.NumCPU()),
		MemoryBytes: memBytes,
	}
}

// buildTLSCredentials creates transport credentials based on the mTLS configuration.
// If no client certs are configured, it falls back to system CA for server verification.
// If all cert paths are empty, it returns insecure credentials for dev environments.
func (c *Client) buildTLSCredentials() (credentials.TransportCredentials, error) {
	if c.cfg.CertPath == "" && c.cfg.KeyPath == "" && c.cfg.CAPath == "" {
		return insecure.NewCredentials(), nil
	}

	tlsCfg := &tls.Config{}

	// Load client certificate if provided.
	if c.cfg.CertPath != "" && c.cfg.KeyPath != "" {
		cert, err := tls.LoadX509KeyPair(c.cfg.CertPath, c.cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	// Load custom CA pool if provided.
	if c.cfg.CAPath != "" {
		caCert, err := os.ReadFile(c.cfg.CAPath)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsCfg.RootCAs = pool
	}

	return credentials.NewTLS(tlsCfg), nil
}

// closeConn closes the gRPC connection if open.
func (c *Client) closeConn() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.stream = nil
	c.connected = false
}

// setConnected updates the connected state.
func (c *Client) setConnected(v bool) {
	c.mu.Lock()
	wasConnected := c.connected
	c.connected = v
	onStateChange := c.onStateChange
	c.mu.Unlock()
	if v != wasConnected && onStateChange != nil {
		onStateChange(v)
	}
}

// FlushAndStop drains the cache, sends all metrics, and closes the connection.
// If sending fails and persistPath is non-empty, remaining metrics are written to disk.
func (c *Client) FlushAndStop(ctx context.Context, persistPath string) error {
	// Cancel the connection loop.
	if c.cancel != nil {
		c.cancel()
	}

	metrics := c.cache.Drain()
	if len(metrics) > 0 {
		c.mu.Lock()
		stream := c.stream
		c.mu.Unlock()

		if stream != nil {
			// Send in batches of 100.
			batchSize := 100
			for i := 0; i < len(metrics); i += batchSize {
				end := i + batchSize
				if end > len(metrics) {
					end = len(metrics)
				}
				batch := metrics[i:end]
				msg := NewMetricBatchMessage(batch)
				if err := stream.Send(msg); err != nil {
					c.logger.Warn().Err(err).Msg("flush send failed, will persist remaining")
					metrics = metrics[i:] // remaining
					goto persist
				}
			}
			metrics = nil // all sent
		}

	persist:
		if len(metrics) > 0 && persistPath != "" {
			data, err := json.Marshal(metrics)
			if err != nil {
				c.logger.Error().Err(err).Msg("failed to marshal metrics for persistence")
			} else {
				if err := os.WriteFile(persistPath, data, 0644); err != nil {
					c.logger.Error().Err(err).Str("path", persistPath).Msg("failed to persist cache")
				} else {
					c.logger.Info().Int("count", len(metrics)).Str("path", persistPath).Msg("cache persisted to disk")
				}
			}
		} else if len(metrics) > 0 {
			c.logger.Warn().Int("count", len(metrics)).Msg("cache not persisted (no persist path configured)")
		}
	}

	// Close connection.
	c.closeConn()

	// Wait for goroutines.
	c.wg.Wait()
	return nil
}

// loadPersistedCache loads metrics from a JSON file into the cache and removes the file.
func (c *Client) loadPersistedCache(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // file doesn't exist or read error, ignore
	}
	var metrics []*collector.Metric
	if err := json.Unmarshal(data, &metrics); err != nil {
		c.logger.Warn().Err(err).Msg("failed to parse persisted cache, discarding")
		os.Remove(path)
		return
	}
	for _, m := range metrics {
		c.cache.Add(m)
	}
	os.Remove(path)
	c.logger.Info().Int("count", len(metrics)).Msg("loaded persisted cache")
}
