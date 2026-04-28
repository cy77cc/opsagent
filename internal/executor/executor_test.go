package executor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecutor_DisallowCommand(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	_, err := e.Execute(context.Background(), Request{Command: "uname"})
	if err == nil {
		t.Fatalf("expected error for disallowed command")
	}
}

func TestExecutor_ExecEcho(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	res, err := e.Execute(context.Background(), Request{Command: "echo", Args: []string{"hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("expected stdout to contain hello, got %q", res.Stdout)
	}
}

func TestExecutor_Timeout(t *testing.T) {
	e := New([]string{"sleep"}, 500*time.Millisecond, 1024)
	res, err := e.Execute(context.Background(), Request{Command: "sleep", Args: []string{"2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != -1 {
		t.Fatalf("expected timeout exit code -1, got %d", res.ExitCode)
	}
}

func TestExecutor_OutputTruncated(t *testing.T) {
	e := New([]string{"yes"}, 500*time.Millisecond, 128)
	res, err := e.Execute(context.Background(), Request{Command: "yes", Args: []string{"x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Stdout, "[truncated]") {
		t.Fatalf("expected stdout truncation marker, got %q", res.Stdout)
	}
}
