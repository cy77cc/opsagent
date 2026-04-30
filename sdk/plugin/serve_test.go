package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testHandler is a minimal Handler implementation for tests.
type testHandler struct {
	initCalled  bool
	shutdownErr error
	taskTypes   []string
	executeFunc func(ctx context.Context, req *TaskRequest) (*TaskResponse, error)
}

func (h *testHandler) Init(cfg map[string]interface{}) error {
	h.initCalled = true
	return nil
}

func (h *testHandler) TaskTypes() []string {
	return h.taskTypes
}

func (h *testHandler) Execute(ctx context.Context, req *TaskRequest) (*TaskResponse, error) {
	if h.executeFunc != nil {
		return h.executeFunc(ctx, req)
	}
	return &TaskResponse{
		TaskID: req.TaskID,
		Status: "ok",
		Data:   "default",
	}, nil
}

func (h *testHandler) Shutdown(ctx context.Context) error {
	return h.shutdownErr
}

func (h *testHandler) HealthCheck(ctx context.Context) error {
	return nil
}

// startTestServer starts ServeWithOptions on a temporary unix socket and returns
// the socket path. The server runs in a background goroutine and will be cleaned
// up when the test finishes.
func startTestServer(t *testing.T, handler Handler, opts ...Option) string {
	t.Helper()

	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	os.Setenv("OPSAGENT_PLUGIN_SOCKET", sock)
	t.Cleanup(func() { os.Unsetenv("OPSAGENT_PLUGIN_SOCKET") })

	go func() {
		ServeWithOptions(handler, opts...)
	}()

	// Wait for the socket to become available.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			conn.Close()
			return sock
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("timed out waiting for plugin socket")
	return ""
}

// dialAndSend connects to the socket, writes a JSON-RPC request, and returns
// the raw response line.
func dialAndSend(t *testing.T, sock string, req rpcRequest) rpcResponse {
	t.Helper()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() {
		t.Fatal("no response received")
	}

	var resp rpcResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestServe_PingPong(t *testing.T) {
	handler := &testHandler{}
	sock := startTestServer(t, handler)

	resp := dialAndSend(t, sock, rpcRequest{
		ID:     1,
		Method: "ping",
	})

	if resp.ID != 1 {
		t.Fatalf("expected id 1, got %d", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	// "pong" is a string, so JSON unmarshal gives us a Go string.
	result, ok := resp.Result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", resp.Result)
	}
	if result != "pong" {
		t.Fatalf("expected pong, got %s", result)
	}
}

func TestServe_ExecuteTask(t *testing.T) {
	handler := &testHandler{
		taskTypes: []string{"echo"},
		executeFunc: func(_ context.Context, req *TaskRequest) (*TaskResponse, error) {
			return &TaskResponse{
				TaskID: req.TaskID,
				Status: "ok",
				Data:   map[string]interface{}{"echo": req.Params["msg"]},
			}, nil
		},
	}
	sock := startTestServer(t, handler)

	resp := dialAndSend(t, sock, rpcRequest{
		ID:     42,
		Method: "execute_task",
		Params: TaskRequest{
			TaskID:   "task-1",
			TaskType: "echo",
			Params:   map[string]interface{}{"msg": "hello"},
		},
	})

	if resp.ID != 42 {
		t.Fatalf("expected id 42, got %d", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Result is a TaskResponse after JSON round-trip.
	resultMap, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}

	if resultMap["task_id"] != "task-1" {
		t.Fatalf("expected task_id task-1, got %v", resultMap["task_id"])
	}
	if resultMap["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resultMap["status"])
	}

	data, ok := resultMap["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data map, got %T", resultMap["data"])
	}
	if data["echo"] != "hello" {
		t.Fatalf("expected echo hello, got %v", data["echo"])
	}
}
