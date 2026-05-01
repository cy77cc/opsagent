package pluginruntime

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestRuntimeStart_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	r := New(cfg, zerolog.Nop())
	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestRuntimeStart_MissingSocketPath(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: ""}
	r := New(cfg, zerolog.Nop())
	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing socket path")
	}
}

func TestRuntimeStart_MissingRuntimePath(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: true, RuntimePath: ""}
	r := New(cfg, zerolog.Nop())
	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("expected error for missing runtime path when autostart=true")
	}
}

func TestRuntimeStart_NoAutoStart(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: false}
	r := New(cfg, zerolog.Nop())
	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func TestRuntimeStop_NotStarted(t *testing.T) {
	cfg := Config{Enabled: true}
	r := New(cfg, zerolog.Nop())
	err := r.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestHealthStatus_Stopped(t *testing.T) {
	cfg := Config{Enabled: true}
	r := New(cfg, zerolog.Nop())
	status := r.HealthStatus()
	if status.Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", status.Status)
	}
}

func TestHealthStatus_Running(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: false}
	r := New(cfg, zerolog.Nop())
	// Start with AutoStart=false sets started=true without launching a process.
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status := r.HealthStatus()
	if status.Status != "running" {
		t.Errorf("expected status 'running', got %q", status.Status)
	}
}

func TestRuntimeStop_AutoStartDisabled(t *testing.T) {
	cfg := Config{Enabled: true, SocketPath: "/tmp/test.sock", AutoStart: false}
	r := New(cfg, zerolog.Nop())
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err := r.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRuntimeStop_CmdNil(t *testing.T) {
	cfg := Config{Enabled: true, AutoStart: true}
	r := New(cfg, zerolog.Nop())
	// cmd is nil by default, AutoStart=true but cmd=nil => early return.
	err := r.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRuntimeStop_RunningProcess(t *testing.T) {
	// Start a long-running process in its own process group.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}

	cfg := Config{Enabled: true, AutoStart: true}
	r := New(cfg, zerolog.Nop())
	r.started = true
	r.cmd = cmd

	// Stop sends SIGINT to the process. When the process exits due to
	// a signal, cmd.Wait() returns a non-nil ExitError which Stop
	// propagates. This is expected behavior.
	_ = r.Stop(context.Background())

	// After stop, started should be false and cmd should be nil.
	if r.started {
		t.Error("expected started=false after Stop")
	}
	if r.cmd != nil {
		t.Error("expected cmd=nil after Stop")
	}
}

func TestRuntimeStop_ContextCancelled(t *testing.T) {
	// Start `cat` which blocks on stdin. Use a near-zero timeout so the
	// context expires before the process can respond to the signal.
	cmd := exec.Command("cat")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cat: %v", err)
	}

	cfg := Config{Enabled: true, AutoStart: true}
	r := New(cfg, zerolog.Nop())
	r.started = true
	r.cmd = cmd

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	err := r.Stop(ctx)
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	// State should still be cleaned up.
	if r.started {
		t.Error("expected started=false after Stop")
	}
	if r.cmd != nil {
		t.Error("expected cmd=nil after Stop")
	}
}
