package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ServeOptions holds configuration for ServeWithOptions.
type ServeOptions struct {
	Logger          *slog.Logger
	GracefulTimeout time.Duration
}

// Option is a functional option for ServeWithOptions.
type Option func(*ServeOptions)

// WithLogger sets the logger used by the plugin server.
func WithLogger(l *slog.Logger) Option {
	return func(o *ServeOptions) {
		o.Logger = l
	}
}

// WithGracefulTimeout sets the timeout for graceful shutdown.
func WithGracefulTimeout(d time.Duration) Option {
	return func(o *ServeOptions) {
		o.GracefulTimeout = d
	}
}

// Serve is a convenience wrapper around ServeWithOptions with default options.
func Serve(handler Handler) error {
	return ServeWithOptions(handler)
}

// ServeWithOptions starts the plugin UDS server. It reads the socket path from
// the OPSAGENT_PLUGIN_SOCKET environment variable, initialises the handler, and
// listens for JSON-RPC requests until a SIGTERM or SIGINT is received.
func ServeWithOptions(handler Handler, opts ...Option) error {
	o := &ServeOptions{
		Logger:          slog.Default(),
		GracefulTimeout: 10 * time.Second,
	}
	for _, fn := range opts {
		fn(o)
	}

	socketPath := os.Getenv("OPSAGENT_PLUGIN_SOCKET")
	if socketPath == "" {
		return fmt.Errorf("OPSAGENT_PLUGIN_SOCKET environment variable is not set")
	}

	if err := handler.Init(nil); err != nil {
		return fmt.Errorf("handler init: %w", err)
	}

	// Remove stale socket if present.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
	}
	o.Logger.Info("plugin listening", "socket", socketPath)

	// Signal handling for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	var wg sync.WaitGroup
	acceptDone := make(chan struct{})

	go func() {
		defer close(acceptDone)
		for {
			conn, err := ln.Accept()
			if err != nil {
				// Accept returns an error when the listener is closed.
				o.Logger.Debug("accept stopped", "error", err)
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleConnection(conn, handler, o.Logger)
			}()
		}
	}()

	sig := <-sigCh
	o.Logger.Info("received signal, shutting down", "signal", sig)

	// Stop accepting new connections.
	ln.Close()
	<-acceptDone

	// Graceful shutdown with timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), o.GracefulTimeout)
	defer cancel()

	if err := handler.Shutdown(shutdownCtx); err != nil {
		o.Logger.Error("handler shutdown error", "error", err)
	}

	// Wait for in-flight connections (with timeout handled by context above).
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-shutdownCtx.Done():
		o.Logger.Warn("graceful timeout exceeded, forcing exit")
	}

	os.Remove(socketPath)
	return nil
}

// handleConnection reads newline-delimited JSON-RPC requests from conn and
// dispatches them to the handler.
func handleConnection(conn net.Conn, handler Handler, logger *slog.Logger) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	// Allow up to 1 MiB per line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(conn, 0, -32700, "parse error: "+err.Error())
			continue
		}

		ctx := context.Background()

		switch req.Method {
		case "ping":
			writeResult(conn, req.ID, "pong")

		case "execute_task":
			resp, err := handler.Execute(ctx, &req.Params)
			if err != nil {
				writeError(conn, req.ID, -32000, err.Error())
				continue
			}
			writeResult(conn, req.ID, resp)

		default:
			writeError(conn, req.ID, -32601, "method not found: "+req.Method)
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Debug("connection read error", "error", err)
	}
}

// writeResult writes a successful JSON-RPC response to conn.
func writeResult(conn net.Conn, id int64, result interface{}) {
	resp := rpcResponse{
		ID:     id,
		Result: result,
	}
	writeJSON(conn, resp)
}

// writeError writes an error JSON-RPC response to conn.
func writeError(conn net.Conn, id int64, code int, msg string) {
	resp := rpcResponse{
		ID: id,
		Error: &rpcError{
			Code:    code,
			Message: msg,
		},
	}
	writeJSON(conn, resp)
}

// writeJSON marshals v and writes it as a newline-delimited line to conn.
func writeJSON(conn net.Conn, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	data = append(data, '\n')
	conn.Write(data)
}
