package pluginruntime

import (
	"context"
	"testing"

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
