package sandbox

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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

func TestExecutorConcurrencyLimit(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{MaxConcurrentTasks: 2}
	exec := NewExecutor(cfg, logger)

	if cap(exec.sem) != 2 {
		t.Errorf("semaphore capacity = %d, want 2", cap(exec.sem))
	}
}

func TestExecutorClose(t *testing.T) {
	logger := zerolog.Nop()
	exec := NewExecutor(Config{}, logger)
	if err := exec.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestWriteScriptFile(t *testing.T) {
	cfg := &NsjailConfig{}
	path, err := cfg.WriteScriptFile("test-001", "#!/bin/bash\necho hello")
	if err != nil {
		t.Fatalf("WriteScriptFile() error: %v", err)
	}
	defer os.Remove(path)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(content) != "#!/bin/bash\necho hello" {
		t.Errorf("content = %q, want script content", string(content))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat error: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestAuditLoggerWithFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "audit-*.log")
	if err != nil {
		t.Fatalf("temp file error: %v", err)
	}
	f.Close()

	logger := zerolog.Nop()
	al := NewAuditLogger(logger, f.Name())
	defer al.Close()

	al.LogExecution(AuditEvent{
		TaskID:   "file-test",
		Command:  "echo",
		ExitCode: 0,
		Duration: time.Millisecond,
	})

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected data written to audit log file")
	}
	if !strings.Contains(string(data), "file-test") {
		t.Error("expected task_id in audit log file")
	}
}

func TestAuditLoggerClose(t *testing.T) {
	al := NewAuditLogger(zerolog.Nop(), "")
	if err := al.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}
