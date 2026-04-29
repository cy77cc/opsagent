package grpcclient

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cy77cc/opsagent/internal/collector"
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
	sender    *Sender
	cache     *MetricCache
	conn      *grpc.ClientConn
	stream    pb.AgentService_ConnectClient
	mu        sync.Mutex
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	connected bool
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
		sender:   &Sender{},
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

	// TODO: add mTLS credentials from CertPath/KeyPath/CAPath.
	conn, err := grpc.DialContext(ctx, c.cfg.ServerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
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
	c.mu.Unlock()

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
	return &pb.AgentInfo{
		Hostname: c.cfg.AgentID, // placeholder — real impl reads hostname
	}
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
	defer c.mu.Unlock()
	c.connected = v
}
