package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/health"
	"github.com/rs/zerolog"
)

// Config controls plugin runtime process and RPC limits.
type Config struct {
	Enabled            bool
	RuntimePath        string
	SocketPath         string
	AutoStart          bool
	StartupTimeout     time.Duration
	RequestTimeout     time.Duration
	MaxConcurrentTasks int
	MaxResultBytes     int
	ChunkSizeBytes     int
	SandboxProfile     string
}

// Runtime manages runtime process lifecycle and task RPC calls.
type Runtime struct {
	cfg    Config
	logger zerolog.Logger

	mu      sync.Mutex
	started bool
	cmd     *exec.Cmd
	sem     chan struct{}
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
}

// New creates a runtime manager.
func New(cfg Config, logger zerolog.Logger) *Runtime {
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 5 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.MaxConcurrentTasks <= 0 {
		cfg.MaxConcurrentTasks = 4
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = 8 * 1024 * 1024
	}
	if cfg.ChunkSizeBytes <= 0 {
		cfg.ChunkSizeBytes = 256 * 1024
	}
	return &Runtime{
		cfg:    cfg,
		logger: logger,
		sem:    make(chan struct{}, cfg.MaxConcurrentTasks),
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

// Start launches runtime process if configured with autostart.
func (r *Runtime) Start(ctx context.Context) error {
	if !r.cfg.Enabled {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	if r.cfg.SocketPath == "" {
		return fmt.Errorf("plugin socket path is required")
	}
	if !r.cfg.AutoStart {
		r.started = true
		return nil
	}
	if r.cfg.RuntimePath == "" {
		return fmt.Errorf("plugin runtime path is required when autostart=true")
	}

	if err := os.MkdirAll(filepath.Dir(r.cfg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("create plugin socket directory: %w", err)
	}
	_ = os.Remove(r.cfg.SocketPath)

	cmd := exec.CommandContext(ctx, r.cfg.RuntimePath, "--socket", r.cfg.SocketPath)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start plugin runtime: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), r.cfg.StartupTimeout)
	defer cancel()
	if err := waitForSocket(waitCtx, r.cfg.SocketPath); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("wait plugin runtime socket: %w", err)
	}

	r.cmd = cmd
	r.started = true
	r.logger.Info().Str("socket", r.cfg.SocketPath).Str("runtime_path", r.cfg.RuntimePath).Msg("plugin runtime started")
	return nil
}

// Stop terminates runtime process if it was started by agent.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	cmd := r.cmd
	auto := r.cfg.AutoStart
	r.started = false
	r.cmd = nil
	r.mu.Unlock()

	if !auto || cmd == nil || cmd.Process == nil {
		return nil
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		if !errors.Is(err, os.ErrProcessDone) {
			_ = cmd.Process.Kill()
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}

	return nil
}

// HealthStatus reports the plugin runtime's running state.
func (r *Runtime) HealthStatus() health.Status {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	status := "stopped"
	if started {
		status = "running"
	}
	return health.Status{Status: status}
}

func waitForSocket(ctx context.Context, socketPath string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
