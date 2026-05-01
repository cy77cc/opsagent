package pluginruntime

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestExecuteTaskSuccess(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", RequestTimeout: 2 * time.Second, MaxResultBytes: 1024, ChunkSizeBytes: 128, MaxConcurrentTasks: 1}, zerolog.Nop())
	r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		payload := base64.StdEncoding.EncodeToString([]byte("hello world"))
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{ID: "t1", Result: &TaskResponse{TaskID: "t1", Status: "ok", Chunks: []Chunk{{Seq: 1, EOF: true, DataB64: payload}}}})
	}()

	res, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: "plugin_text_process", Payload: map[string]any{"text": "hello"}})
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("unexpected status: %s", res.Status)
	}
	<-done
}

func TestExecuteTaskSizeLimit(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", RequestTimeout: 2 * time.Second, MaxResultBytes: 1024, ChunkSizeBytes: 128, MaxConcurrentTasks: 1}, zerolog.Nop())
	r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		_, _ = bufio.NewReader(serverConn).ReadBytes('\n')
		big := make([]byte, 2048)
		payload := base64.StdEncoding.EncodeToString(big)
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{ID: "t2", Result: &TaskResponse{TaskID: "t2", Status: "ok", Chunks: []Chunk{{Seq: 1, EOF: true, DataB64: payload}}}})
	}()

	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t2", Type: "plugin_text_process", Payload: map[string]any{"text": "hello"}})
	if err == nil {
		t.Fatalf("expected size limit error")
	}
}

func TestExecuteTask_DisabledRuntime(t *testing.T) {
	r := New(Config{Enabled: false, SocketPath: "/tmp/plugin.sock"}, zerolog.Nop())
	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: "audit"})
	if err == nil {
		t.Fatal("expected error for disabled runtime")
	}
}

func TestExecuteTask_EmptyTaskID(t *testing.T) {
	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", MaxConcurrentTasks: 1}, zerolog.Nop())
	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "", Type: "audit"})
	if err == nil {
		t.Fatal("expected error for empty TaskID")
	}
}

func TestExecuteTask_EmptyType(t *testing.T) {
	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", MaxConcurrentTasks: 1}, zerolog.Nop())
	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: ""})
	if err == nil {
		t.Fatal("expected error for empty Type")
	}
}

func TestExecuteTask_RPCError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", RequestTimeout: 2 * time.Second, MaxConcurrentTasks: 1}, zerolog.Nop())
	r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{
			ID:    "t1",
			Error: &rpcError{Code: -32600, Message: "invalid request"},
		})
	}()

	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: "audit"})
	if err == nil {
		t.Fatal("expected RPC error")
	}
}

func TestExecuteTask_NilResult(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", RequestTimeout: 2 * time.Second, MaxConcurrentTasks: 1}, zerolog.Nop())
	r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{ID: "t1"})
	}()

	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: "audit"})
	if err == nil {
		t.Fatal("expected nil result error")
	}
}

func TestValidateChunkSize_MaxBytesZero(t *testing.T) {
	resp := &TaskResponse{
		TaskID: "t1",
		Status: "ok",
		Chunks: []Chunk{{Seq: 1, EOF: true, DataB64: base64.StdEncoding.EncodeToString(make([]byte, 1024))}},
	}
	if err := validateChunkSize(resp, 0); err != nil {
		t.Fatalf("expected nil for maxBytes=0, got %v", err)
	}
}

func TestValidateChunkSize_MaxBytesNegative(t *testing.T) {
	resp := &TaskResponse{
		TaskID: "t1",
		Status: "ok",
		Chunks: []Chunk{{Seq: 1, EOF: true, DataB64: base64.StdEncoding.EncodeToString(make([]byte, 1024))}},
	}
	if err := validateChunkSize(resp, -1); err != nil {
		t.Fatalf("expected nil for maxBytes=-1, got %v", err)
	}
}

func TestValidateChunkSize_Success(t *testing.T) {
	small := base64.StdEncoding.EncodeToString([]byte("small data"))
	resp := &TaskResponse{
		TaskID: "t1",
		Status: "ok",
		Chunks: []Chunk{{Seq: 1, EOF: true, DataB64: small}},
	}
	if err := validateChunkSize(resp, 1024); err != nil {
		t.Fatalf("expected nil for small chunk, got %v", err)
	}
}

func TestExecuteTask_DialError(t *testing.T) {
	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", MaxConcurrentTasks: 1}, zerolog.Nop())
	r.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}
	_, err := r.ExecuteTask(context.Background(), TaskRequest{TaskID: "t1", Type: "audit"})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestExecuteTask_ContextCancelledDuringSemAcquire(t *testing.T) {
	r := New(Config{Enabled: true, SocketPath: "/tmp/plugin.sock", MaxConcurrentTasks: 1}, zerolog.Nop())
	// Fill the semaphore.
	r.sem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := r.ExecuteTask(ctx, TaskRequest{TaskID: "t1", Type: "audit"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected Canceled, got %v", err)
	}
}
