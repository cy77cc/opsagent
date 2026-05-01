package sandbox

import (
	"bytes"
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

func TestCommandOrScript(t *testing.T) {
	tests := []struct {
		name string
		req  ExecRequest
		want string
	}{
		{
			name: "command when Script is empty",
			req:  ExecRequest{TaskID: "t1", Command: "echo"},
			want: "command",
		},
		{
			name: "script when Script is set",
			req:  ExecRequest{TaskID: "t2", Script: "echo hello", Interpreter: "bash"},
			want: "script",
		},
		{
			name: "script takes precedence over command",
			req:  ExecRequest{TaskID: "t3", Command: "echo", Script: "ls", Interpreter: "bash"},
			want: "script",
		},
		{
			name: "command when both empty",
			req:  ExecRequest{TaskID: "t4"},
			want: "command",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := commandOrScript(tc.req)
			if got != tc.want {
				t.Errorf("commandOrScript() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildNsjailConfig_AllOverrides(t *testing.T) {
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
		TaskID: "test-overrides",
		SandboxCfg: &SandboxOverride{
			MemoryMB:    256,
			CPUPercent:  75,
			MaxPIDs:     64,
			NetworkMode: "allowlist",
			AllowedIPs:  []string{"10.0.0.1", "10.0.0.2"},
		},
	}

	nsCfg := executor.buildNsjailConfig(req)
	if nsCfg.MemoryMB != 256 {
		t.Errorf("MemoryMB = %d, want 256", nsCfg.MemoryMB)
	}
	if nsCfg.CPUPercent != 75 {
		t.Errorf("CPUPercent = %d, want 75", nsCfg.CPUPercent)
	}
	if nsCfg.MaxPIDs != 64 {
		t.Errorf("MaxPIDs = %d, want 64", nsCfg.MaxPIDs)
	}
	if nsCfg.NetworkMode != "allowlist" {
		t.Errorf("NetworkMode = %q, want 'allowlist'", nsCfg.NetworkMode)
	}
	if len(nsCfg.AllowedIPs) != 2 {
		t.Errorf("AllowedIPs length = %d, want 2", len(nsCfg.AllowedIPs))
	}
}

func TestBuildNsjailConfig_NilSandboxCfg(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MemoryMB:    128,
		CPUPercent:  50,
		MaxPIDs:     32,
		NetworkMode: "disabled",
		WorkDir:     "/work",
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:      "test-nil-override",
		SandboxCfg:  nil,
	}

	nsCfg := executor.buildNsjailConfig(req)
	// Should use defaults when SandboxCfg is nil.
	if nsCfg.MemoryMB != 128 {
		t.Errorf("MemoryMB = %d, want 128", nsCfg.MemoryMB)
	}
	if nsCfg.CPUPercent != 50 {
		t.Errorf("CPUPercent = %d, want 50", nsCfg.CPUPercent)
	}
	if nsCfg.MaxPIDs != 32 {
		t.Errorf("MaxPIDs = %d, want 32", nsCfg.MaxPIDs)
	}
}

func TestBuildNsjailConfig_PartialOverrides(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MemoryMB:    128,
		CPUPercent:  50,
		MaxPIDs:     32,
		NetworkMode: "disabled",
		WorkDir:     "/work",
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID: "test-partial",
		SandboxCfg: &SandboxOverride{
			// Only override CPUPercent, leave others at zero.
			CPUPercent: 80,
		},
	}

	nsCfg := executor.buildNsjailConfig(req)
	if nsCfg.MemoryMB != 128 {
		t.Errorf("MemoryMB = %d, want 128 (default)", nsCfg.MemoryMB)
	}
	if nsCfg.CPUPercent != 80 {
		t.Errorf("CPUPercent = %d, want 80 (override)", nsCfg.CPUPercent)
	}
	if nsCfg.MaxPIDs != 32 {
		t.Errorf("MaxPIDs = %d, want 32 (default)", nsCfg.MaxPIDs)
	}
	if nsCfg.NetworkMode != "disabled" {
		t.Errorf("NetworkMode = %q, want 'disabled' (default)", nsCfg.NetworkMode)
	}
}

func TestExecuteCommand_ContextCancelled(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MaxConcurrentTasks: 1,
	}
	executor := NewExecutor(cfg, logger)

	// Fill the semaphore so run() blocks on sem acquire and hits ctx.Done().
	executor.sem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := ExecRequest{
		TaskID:  "test-cancel-001",
		Command: "echo",
		Args:    []string{"hello"},
	}

	_, err := executor.ExecuteCommand(ctx, req, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestExecuteScript_ContextCancelled(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MaxConcurrentTasks: 1,
	}
	executor := NewExecutor(cfg, logger)

	// Fill the semaphore so run() blocks on sem acquire and hits ctx.Done().
	executor.sem <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := ExecRequest{
		TaskID:      "test-cancel-script",
		Script:      "echo hello",
		Interpreter: "bash",
	}

	_, err := executor.ExecuteScript(ctx, req, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestExecuteCommand_SemaphoreBlocks(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		MaxConcurrentTasks: 1,
	}
	executor := NewExecutor(cfg, logger)

	// Fill the semaphore.
	executor.sem <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := ExecRequest{
		TaskID:  "test-sem-001",
		Command: "echo",
	}

	_, err := executor.ExecuteCommand(ctx, req, nil)
	if err == nil {
		t.Fatal("expected error when semaphore is full and context times out")
	}
	// Should be DeadlineExceeded since the semaphore blocks until ctx expires.
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
}

func TestExecuteCommand_CgroupPath(t *testing.T) {
	logger := zerolog.Nop()
	// Set CgroupBase so the run path tries to create a cgroup.
	// Use a nonexistent nsjail path so the exec fails after cgroup setup.
	cfg := Config{
		CgroupBase:  t.TempDir(),
		NsjailPath:  "/nonexistent/nsjail-binary",
		MemoryMB:    64,
		CPUPercent:  25,
		MaxPIDs:     16,
		TimeoutSec:  2,
		WorkDir:     "/work",
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:  "test-cgroup-run",
		Command: "echo",
		Args:    []string{"hello"},
	}

	result, err := executor.ExecuteCommand(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The command fails because nsjail doesn't exist, but run() should
	// still return a result (not an error).
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code for missing nsjail")
	}
}

func TestExecuteScript_CgroupPath(t *testing.T) {
	logger := zerolog.Nop()
	cfg := Config{
		CgroupBase:  t.TempDir(),
		NsjailPath:  "/nonexistent/nsjail-binary",
		MemoryMB:    64,
		CPUPercent:  25,
		MaxPIDs:     16,
		TimeoutSec:  2,
		WorkDir:     "/work",
	}
	executor := NewExecutor(cfg, logger)

	req := ExecRequest{
		TaskID:      "test-cgroup-script",
		Script:      "echo hello",
		Interpreter: "bash",
	}

	result, err := executor.ExecuteScript(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code for missing nsjail")
	}
}

func TestAuditLoggerInvalidPath(t *testing.T) {
	// Opening a file under a nonexistent directory should fall through
	// to the stderr-only logger.
	logger := zerolog.Nop()
	al := NewAuditLogger(logger, "/nonexistent/deep/path/audit.log")
	defer al.Close()

	// Should still work (just logs to stderr, not file).
	al.LogExecution(AuditEvent{
		TaskID:   "invalid-path-test",
		Command:  "echo",
		ExitCode: 0,
	})
}

func TestAuditLoggerLogExecutionWithStats(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()
	al := NewAuditLogger(logger, "")
	defer al.Close()

	stats := &Stats{
		PeakMemoryBytes: 1048576,
		CPUTimeUserMs:   300,
		CPUTimeSystemMs: 200,
		ProcessCount:    5,
		BytesWritten:    2048,
		BytesRead:       1024,
	}

	al.LogExecution(AuditEvent{
		TaskID:   "stats-test",
		Command:  "echo",
		ExitCode: 0,
		Stats:    stats,
	})

	if !bytes.Contains(buf.Bytes(), []byte("stats_peak_memory_bytes")) {
		t.Error("expected stats_peak_memory_bytes in output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("stats_process_count")) {
		t.Error("expected stats_process_count in output")
	}
}
