package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/rs/zerolog"

	"github.com/cy77cc/opsagent/internal/sandbox"
)

// TestSandboxExecutorSimpleCommand verifies that a simple echo command
// can be executed in the sandbox when running as root with nsjail installed.
func TestSandboxExecutorSimpleCommand(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping sandbox test: not running as root")
	}
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("skipping sandbox test: nsjail not found in PATH")
	}

	cfg := sandbox.Config{
		NsjailPath:  "nsjail",
		WorkDir:     "/tmp",
		MemoryMB:    64,
		CPUPercent:  25,
		MaxPIDs:     16,
		TimeoutSec:  10,
		MaxOutputKB: 64,
		NetworkMode: "disabled",
	}

	logger := zerolog.Nop()
	executor := sandbox.NewExecutor(cfg, logger)

	req := sandbox.ExecRequest{
		TaskID:  "test-echo-001",
		Command: "/bin/echo",
		Args:    []string{"hello", "world"},
	}

	result, err := executor.ExecuteCommand(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("ExecuteCommand failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	if result.TaskID != "test-echo-001" {
		t.Errorf("expected task ID test-echo-001, got %s", result.TaskID)
	}

	t.Logf("sandbox echo command completed: exit_code=%d duration=%v", result.ExitCode, result.Duration)
}

// TestSandboxExecutorBlockedCommand verifies that a command blocked by policy
// is rejected before execution.
func TestSandboxExecutorBlockedCommand(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping sandbox test: not running as root")
	}
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("skipping sandbox test: nsjail not found in PATH")
	}

	cfg := sandbox.Config{
		NsjailPath:  "nsjail",
		WorkDir:     "/tmp",
		MemoryMB:    64,
		CPUPercent:  25,
		MaxPIDs:     16,
		TimeoutSec:  10,
		MaxOutputKB: 64,
		NetworkMode: "disabled",
		Policy: sandbox.Policy{
			BlockedCommands: []string{"rm", "dd", "mkfs"},
		},
	}

	logger := zerolog.Nop()
	executor := sandbox.NewExecutor(cfg, logger)

	req := sandbox.ExecRequest{
		TaskID:  "test-blocked-001",
		Command: "rm",
		Args:    []string{"-rf", "/"},
	}

	result, err := executor.ExecuteCommand(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected policy validation error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for blocked command, got %+v", result)
	}

	t.Logf("blocked command correctly rejected: %v", err)
}

// TestSandboxExecutorPolicyValidation verifies that policy validation
// works for commands that are not in the allowed list.
func TestSandboxExecutorPolicyValidation(t *testing.T) {
	cfg := sandbox.Config{
		Policy: sandbox.Policy{
			AllowedCommands: []string{"echo", "ls", "cat"},
		},
	}

	logger := zerolog.Nop()
	executor := sandbox.NewExecutor(cfg, logger)

	// Command not in allowed list should be rejected.
	req := sandbox.ExecRequest{
		TaskID:  "test-policy-001",
		Command: "curl",
		Args:    []string{"http://example.com"},
	}

	result, err := executor.ExecuteCommand(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected policy error for disallowed command, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result for disallowed command, got %+v", result)
	}

	t.Logf("disallowed command correctly rejected: %v", err)
}
