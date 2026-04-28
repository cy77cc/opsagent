package pluginruntime

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
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
