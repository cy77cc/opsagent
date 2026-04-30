//go:build e2e

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/cy77cc/opsagent/internal/app"
	"github.com/cy77cc/opsagent/internal/config"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"

	// Register inputs for the test config.
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/cpu"
)

// ---------------------------------------------------------------------------
// Mock gRPC server (implements pb.AgentServiceServer over real TCP)
// ---------------------------------------------------------------------------

type e2eMockGRPCServer struct {
	pb.UnimplementedAgentServiceServer
	grpcServer *grpc.Server
	addr       string
	// registered receives the agent ID when registration is processed.
	registered chan string
	// metricsRecv counts the number of MetricBatch messages received.
	metricsRecv atomic.Int64
	// configUpd is a channel for sending ConfigUpdate messages to the agent.
	configUpd chan *pb.ConfigUpdate
	// lastStream holds the active bidirectional stream for sending messages.
	lastStream grpc.BidiStreamingServer[pb.AgentMessage, pb.PlatformMessage]
}

func startE2EMockGRPCServer(t *testing.T) *e2eMockGRPCServer {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "failed to listen on random port")

	m := &e2eMockGRPCServer{
		grpcServer: grpc.NewServer(),
		addr:       lis.Addr().String(),
		registered: make(chan string, 10),
		configUpd:  make(chan *pb.ConfigUpdate, 10),
	}
	pb.RegisterAgentServiceServer(m.grpcServer, m)

	go func() {
		if err := m.grpcServer.Serve(lis); err != nil {
			t.Logf("mock gRPC server stopped: %v", err)
		}
	}()

	t.Cleanup(func() { m.grpcServer.Stop() })
	return m
}

func (m *e2eMockGRPCServer) Connect(stream grpc.BidiStreamingServer[pb.AgentMessage, pb.PlatformMessage]) error {
	m.lastStream = stream

	// Receive messages from the client in a goroutine.
	msgCh := make(chan *pb.AgentMessage, 64)
	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	// First message must be registration.
	select {
	case msg := <-msgCh:
		if reg := msg.GetRegistration(); reg != nil {
			m.registered <- reg.GetAgentId()
		}
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("timeout waiting for registration")
	}

	// Main message loop: process client messages and forward config updates.
	for {
		select {
		case msg := <-msgCh:
			if msg.GetMetrics() != nil {
				m.metricsRecv.Add(1)
			}
			// Heartbeats and other messages are acknowledged silently.
		case upd := <-m.configUpd:
			_ = stream.Send(&pb.PlatformMessage{
				Payload: &pb.PlatformMessage_ConfigUpdate{ConfigUpdate: upd},
			})
		case err := <-errCh:
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// findFreePort returns an available TCP port on 127.0.0.1.
// There is a small race between closing the listener and the caller binding.
func findFreePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := lis.Addr().(*net.TCPAddr).Port
	lis.Close()
	return port
}

// loadTestConfig returns a config.Config that points at the given mock gRPC
// server address and uses the specified HTTP listen address.
func loadTestConfig(t *testing.T, grpcAddr, httpAddr string) *config.Config {
	t.Helper()
	return &config.Config{
		Agent: config.AgentConfig{
			ID:                     "e2e-test-agent",
			Name:                   "e2e-test",
			IntervalSeconds:        1,
			ShutdownTimeoutSeconds: 10,
		},
		Server: config.ServerConfig{
			ListenAddr: httpAddr,
		},
		Executor: config.ExecutorConfig{
			TimeoutSeconds:  10,
			AllowedCommands: []string{"echo", "ls", "cat"},
			MaxOutputBytes:  65536,
		},
		Reporter: config.ReporterConfig{
			Mode:            "stdout",
			TimeoutSeconds:  5,
			RetryCount:      3,
			RetryIntervalMS: 500,
		},
		Auth:       config.AuthConfig{Enabled: false},
		Prometheus: config.PrometheusConfig{Enabled: false},
		Plugin:     config.PluginConfig{Enabled: false},
		GRPC: config.GRPCConfig{
			ServerAddr:                grpcAddr,
			EnrollToken:               "test-token",
			HeartbeatIntervalSeconds:  5,
			ReconnectInitialBackoffMS: 500,
			ReconnectMaxBackoffMS:     5000,
		},
		Sandbox: config.SandboxConfig{Enabled: false},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: nil},
			},
		},
		PluginGateway: config.PluginGatewayConfig{Enabled: false},
	}
}

// waitForHTTPReady polls GET /healthz until it returns 200 or the timeout expires.
func waitForHTTPReady(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://%s/healthz", addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("HTTP server at %s did not become ready within %v", addr, timeout)
}

// sandboxAvailable reports whether nsjail is installed.
func sandboxAvailable() bool {
	_, err := exec.LookPath("nsjail")
	return err == nil
}

// sendHTTPTask sends a POST /api/v1/tasks and returns the parsed apiResponse.
func sendHTTPTask(t *testing.T, addr string, taskType string, payload map[string]any) map[string]any {
	t.Helper()

	body := map[string]any{
		"task_id": fmt.Sprintf("e2e-%s-%d", taskType, time.Now().UnixNano()),
		"type":    taskType,
		"payload": payload,
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/tasks", addr),
		"application/json",
		bytes.NewReader(data),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(respBody, &result), "response body: %s", respBody)
	return result
}

// ---------------------------------------------------------------------------
// E2E Test: Full Agent Lifecycle
// ---------------------------------------------------------------------------

func TestAgentFullLifecycle(t *testing.T) {
	// -----------------------------------------------------------------------
	// Step 1: Start mock gRPC server
	// -----------------------------------------------------------------------
	mockGRPC := startE2EMockGRPCServer(t)
	t.Logf("step 1: mock gRPC server listening at %s", mockGRPC.addr)

	// -----------------------------------------------------------------------
	// Step 2: Create and start Agent (in goroutine)
	// -----------------------------------------------------------------------
	httpPort := findFreePort(t)
	httpAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
	cfg := loadTestConfig(t, mockGRPC.addr, httpAddr)

	log := zerolog.New(zerolog.NewTestWriter(t)).With().Timestamp().Logger()
	agent, err := app.NewAgent(cfg, log)
	require.NoError(t, err, "NewAgent failed")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	agentErr := make(chan error, 1)
	go func() {
		agentErr <- agent.Run(ctx)
	}()
	t.Log("step 2: agent started in background goroutine")

	// -----------------------------------------------------------------------
	// Step 3: Verify agent registers with mock server
	// -----------------------------------------------------------------------
	select {
	case agentID := <-mockGRPC.registered:
		assert.Equal(t, "e2e-test-agent", agentID, "registered agent ID mismatch")
		t.Logf("step 3: agent registered with ID=%s", agentID)
	case <-time.After(15 * time.Second):
		t.Fatal("step 3: timeout waiting for agent registration")
	}

	// Wait for HTTP server to be ready.
	waitForHTTPReady(t, httpAddr, 10*time.Second)
	t.Logf("step 3: HTTP server ready at %s", httpAddr)

	// -----------------------------------------------------------------------
	// Step 4: Send exec_command task via HTTP, verify response
	// -----------------------------------------------------------------------
	result := sendHTTPTask(t, httpAddr, "exec_command", map[string]any{
		"command": "echo",
		"args":    []string{"hello-e2e"},
	})

	success, ok := result["success"].(bool)
	require.True(t, ok, "expected success field in response")
	assert.True(t, success, "exec_command task should succeed")
	t.Logf("step 4: exec_command result: %+v", result)

	// -----------------------------------------------------------------------
	// Step 5: If sandbox available, send sandbox_exec task
	// -----------------------------------------------------------------------
	if sandboxAvailable() {
		sandboxResult := sendHTTPTask(t, httpAddr, "sandbox_exec", map[string]any{
			"command": "echo",
			"args":    []string{"sandbox-hello"},
		})
		sbSuccess, _ := sandboxResult["success"].(bool)
		assert.True(t, sbSuccess, "sandbox_exec task should succeed")
		t.Logf("step 5: sandbox_exec result: %+v", sandboxResult)
	} else {
		t.Log("step 5: sandbox not available, skipping sandbox_exec")
	}

	// -----------------------------------------------------------------------
	// Step 6: Send ConfigUpdate via mock server stream
	// -----------------------------------------------------------------------
	mockGRPC.configUpd <- &pb.ConfigUpdate{
		ConfigYaml: []byte("agent:\n  interval_seconds: 2\n"),
		Version:    42,
	}
	t.Log("step 6: ConfigUpdate sent via mock gRPC stream")

	// Give the agent time to process the config update.
	time.Sleep(500 * time.Millisecond)

	// -----------------------------------------------------------------------
	// Step 7: Verify metrics are being collected
	// -----------------------------------------------------------------------
	assert.Eventually(t, func() bool {
		return mockGRPC.metricsRecv.Load() > 0
	}, 15*time.Second, 500*time.Millisecond,
		"mock server should receive metric batches from agent")
	t.Logf("step 7: metrics received by mock server: %d batches", mockGRPC.metricsRecv.Load())

	// -----------------------------------------------------------------------
	// Step 8: Cancel context to trigger shutdown
	// -----------------------------------------------------------------------
	cancel()
	t.Log("step 8: context cancelled, waiting for agent shutdown")

	// -----------------------------------------------------------------------
	// Step 9: Verify agent shuts down gracefully
	// -----------------------------------------------------------------------
	select {
	case err := <-agentErr:
		require.NoError(t, err, "agent.Run should return nil on graceful shutdown")
	case <-time.After(30 * time.Second):
		t.Fatal("step 9: timeout waiting for agent to shut down")
	}

	assert.Eventually(t, func() bool {
		return agent.IsShutdownComplete()
	}, 5*time.Second, 100*time.Millisecond,
		"agent should report shutdown complete")
	t.Log("step 9: agent shut down gracefully")
}
