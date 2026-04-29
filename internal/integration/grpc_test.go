package integration

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/cy77cc/nodeagentx/internal/grpcclient/proto"
)

const bufSize = 1024 * 1024

// mockAgentService implements pb.AgentServiceServer and records Connect calls.
type mockAgentService struct {
	pb.UnimplementedAgentServiceServer
	mu         sync.Mutex
	connectCnt int
	lastStream grpc.BidiStreamingServer[pb.AgentMessage, pb.PlatformMessage]
}

func (m *mockAgentService) Connect(stream grpc.BidiStreamingServer[pb.AgentMessage, pb.PlatformMessage]) error {
	m.mu.Lock()
	m.connectCnt++
	m.lastStream = stream
	m.mu.Unlock()

	// Keep the stream alive until the client disconnects.
	// Read messages until the stream ends.
	for {
		_, err := stream.Recv()
		if err != nil {
			return err
		}
	}
}

func (m *mockAgentService) connectCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectCnt
}

// startMockServer starts an in-memory gRPC server using bufconn and returns
// the listener and a dialer function for creating client connections.
func startMockServer(t *testing.T, svc *mockAgentService) *bufconn.Listener {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	pb.RegisterAgentServiceServer(s, svc)

	go func() {
		if err := s.Serve(lis); err != nil {
			// Server.Serve returns after listener is closed.
			t.Logf("server stopped: %v", err)
		}
	}()

	t.Cleanup(func() {
		s.Stop()
	})

	return lis
}

// bufDialer returns a function that dials the bufconn listener.
func bufDialer(lis *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
}

// TestGRPCClientConnect verifies that a gRPC Client can connect to an
// in-memory server and that the server's Connect method is called.
func TestGRPCClientConnect(t *testing.T) {
	svc := &mockAgentService{}
	lis := startMockServer(t, svc)

	// Create a gRPC client connection using bufconn.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufnet",
		grpc.WithContextDialer(bufDialer(lis)),
		grpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}
	defer conn.Close()

	// Create the AgentService client and call Connect.
	client := pb.NewAgentServiceClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Send a registration message to establish the stream.
	regMsg := &pb.AgentMessage{
		Payload: &pb.AgentMessage_Registration{
			Registration: &pb.AgentRegistration{
				AgentId: "test-agent-001",
				Token:   "test-token",
				AgentInfo: &pb.AgentInfo{
					Hostname: "test-host",
				},
				Capabilities: []string{"metrics", "exec"},
			},
		},
	}
	if err := stream.Send(regMsg); err != nil {
		t.Fatalf("failed to send registration: %v", err)
	}

	// Give the server a moment to process.
	time.Sleep(100 * time.Millisecond)

	// Verify the server's Connect was called.
	if cnt := svc.connectCount(); cnt != 1 {
		t.Errorf("expected 1 Connect call, got %d", cnt)
	}

	// Close the send side to signal we're done.
	stream.CloseSend()

	t.Logf("gRPC client integration test passed: Connect was called %d time(s)", svc.connectCount())
}
