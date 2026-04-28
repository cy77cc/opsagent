package sandbox

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rs/zerolog"
)

func nsjailInstalled() bool {
	_, err := exec.LookPath("nsjail")
	return err == nil
}

func TestExecutorRunCommand(t *testing.T) {
	if !nsjailInstalled() {
		t.Skip("nsjail not installed, skipping integration test")
	}

	logger := zerolog.Nop()
	cfg := Config{
		NsjailPath: "nsjail",
		WorkDir:    "/work",
		CgroupBase: t.TempDir(),
		MemoryMB:   64,
		CPUPercent: 25,
		MaxPIDs:    16,
		TimeoutSec: 5,
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:  "test-cmd-001",
		Command: "echo",
		Args:    []string{"hello"},
	}

	var output []byte
	sender := func(data []byte) { output = append(output, data...) }

	result, err := executor.ExecuteCommand(context.Background(), req, sender)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestExecutorBlockCommand(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		Policy: Policy{
			BlockedCommands: []string{"rm", "shutdown"},
		},
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:  "test-block-001",
		Command: "rm",
		Args:    []string{"-rf", "/"},
	}

	_, err := executor.ExecuteCommand(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error for blocked command")
	}
}

func TestExecutorBlockScript(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		Policy: Policy{
			AllowedInterpreters: []string{"bash"},
			BlockedKeywords:     []string{"rm -rf"},
		},
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:      "test-block-script-001",
		Script:      "rm -rf /",
		Interpreter: "bash",
	}

	_, err := executor.ExecuteScript(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error for blocked script")
	}
}

func TestExecutorBlockInterpreter(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		Policy: Policy{
			AllowedInterpreters: []string{"bash"},
		},
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:      "test-interp-001",
		Script:      "print('hi')",
		Interpreter: "python3",
	}

	_, err := executor.ExecuteScript(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error for disallowed interpreter")
	}
}

func TestExecutorBuildNsjailConfig(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MemoryMB:    128,
		CPUPercent:  50,
		MaxPIDs:     32,
		TimeoutSec:  30,
		NetworkMode: "disabled",
		WorkDir:     "/work",
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID: "test-001",
		SandboxCfg: &SandboxOverride{
			MemoryMB:   256,
			CPUPercent: 75,
		},
	}

	nsCfg := executor.buildNsjailConfig(req)
	if nsCfg.MemoryMB != 256 {
		t.Errorf("expected override MemoryMB=256, got %d", nsCfg.MemoryMB)
	}
	if nsCfg.CPUPercent != 75 {
		t.Errorf("expected override CPUPercent=75, got %d", nsCfg.CPUPercent)
	}
	if nsCfg.MaxPIDs != 32 {
		t.Errorf("expected default MaxPIDs=32, got %d", nsCfg.MaxPIDs)
	}
}

func TestExecutorDefaults(t *testing.T) {
	logger := zerolog.Nop()
	executor := NewExecutor(Config{}, logger)

	if executor.cfg.NsjailPath != "nsjail" {
		t.Errorf("default NsjailPath = %q, want 'nsjail'", executor.cfg.NsjailPath)
	}
	if executor.cfg.MemoryMB != 128 {
		t.Errorf("default MemoryMB = %d, want 128", executor.cfg.MemoryMB)
	}
	if executor.cfg.TimeoutSec != 30 {
		t.Errorf("default TimeoutSec = %d, want 30", executor.cfg.TimeoutSec)
	}
}
