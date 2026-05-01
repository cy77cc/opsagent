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

func TestNew_DefaultValues(t *testing.T) {
	e := New(nil, 0, 0)
	if e.defaultTimeout != 10*time.Second {
		t.Errorf("expected default timeout 10s, got %v", e.defaultTimeout)
	}
	if e.maxOutputBytes != 64*1024 {
		t.Errorf("expected default maxOutputBytes %d, got %d", 64*1024, e.maxOutputBytes)
	}
	// Default allowed commands should be populated.
	if len(e.allowed) == 0 {
		t.Error("expected default allowed commands to be populated")
	}
}

func TestExecute_EmptyCommand(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	_, err := e.Execute(context.Background(), Request{Command: ""})
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "command is required") {
		t.Errorf("expected 'command is required' error, got: %v", err)
	}
}

func TestExecute_WhitespaceCommand(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	_, err := e.Execute(context.Background(), Request{Command: "   "})
	if err == nil {
		t.Fatal("expected error for whitespace-only command")
	}
}

func TestExecute_CustomTimeoutSeconds(t *testing.T) {
	e := New([]string{"echo"}, 10*time.Second, 1024)
	res, err := e.Execute(context.Background(), Request{Command: "echo", Args: []string{"ok"}, TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "ok") {
		t.Fatalf("expected stdout to contain 'ok', got %q", res.Stdout)
	}
}

func TestExecute_ExitError(t *testing.T) {
	e := New([]string{"sh"}, 5*time.Second, 1024)
	res, err := e.Execute(context.Background(), Request{Command: "sh", Args: []string{"-c", "exit 42"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", res.ExitCode)
	}
}

func TestLimitedBuffer_WriteMaxZero(t *testing.T) {
	buf := newLimitedBuffer(0)
	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected Write to return 5, got %d", n)
	}
	// With max <= 0, data is discarded, buffer stays empty.
	if buf.String() != "" {
		t.Errorf("expected empty buffer, got %q", buf.String())
	}
}

func TestExecute_RejectsNullByteInArgs(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	_, err := e.Execute(context.Background(), Request{
		Command: "echo",
		Args:    []string{"hello\x00world"},
	})
	if err == nil {
		t.Fatal("expected error for null byte in argument")
	}
	if !strings.Contains(err.Error(), "null byte") {
		t.Errorf("expected 'null byte' in error, got: %v", err)
	}
}

func TestExecute_RejectsOversizedArgs(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	bigArg := strings.Repeat("x", 4097)
	_, err := e.Execute(context.Background(), Request{
		Command: "echo",
		Args:    []string{bigArg},
	})
	if err == nil {
		t.Fatal("expected error for oversized argument")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("expected 'too long' in error, got: %v", err)
	}
}

func TestExecute_AllowsValidArgs(t *testing.T) {
	e := New([]string{"echo"}, 2*time.Second, 1024)
	res, err := e.Execute(context.Background(), Request{
		Command: "echo",
		Args:    []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
}

func TestBuildWhitelist_EmptyAndWhitespaceEntries(t *testing.T) {
	commands := []string{"echo", "", "  ", "\t", "ls"}
	wl := buildWhitelist(commands)
	if _, ok := wl["echo"]; !ok {
		t.Error("expected 'echo' in whitelist")
	}
	if _, ok := wl["ls"]; !ok {
		t.Error("expected 'ls' in whitelist")
	}
	if len(wl) != 2 {
		t.Errorf("expected 2 entries in whitelist, got %d", len(wl))
	}
}
