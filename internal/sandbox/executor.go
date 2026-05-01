package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/rs/zerolog"
)

// Config holds the global sandbox executor configuration.
type Config struct {
	NsjailPath  string `json:"nsjail_path"`
	WorkDir     string `json:"work_dir"`
	CgroupBase  string `json:"cgroup_base"`
	MemoryMB    int    `json:"memory_mb"`
	CPUPercent  int    `json:"cpu_percent"`
	MaxPIDs     int    `json:"max_pids"`
	TimeoutSec  int    `json:"timeout_sec"`
	MaxOutputKB int    `json:"max_output_kb"`
	NetworkMode        string `json:"network_mode"`
	Policy             Policy `json:"policy"`
	MaxConcurrentTasks int    `json:"max_concurrent_tasks"`
	AuditLogPath       string `json:"audit_log_path"`
}

// ExecRequest is a sandbox execution request.
type ExecRequest struct {
	TaskID      string            `json:"task_id"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Script      string            `json:"script,omitempty"`
	Interpreter string            `json:"interpreter,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Timeout     time.Duration     `json:"timeout,omitempty"`
	SandboxCfg  *SandboxOverride  `json:"sandbox_cfg,omitempty"`
}

// SandboxOverride allows per-request overrides of sandbox resource limits.
type SandboxOverride struct {
	MemoryMB    int      `json:"memory_mb"`
	CPUPercent  int      `json:"cpu_percent"`
	MaxPIDs     int      `json:"max_pids"`
	NetworkMode string   `json:"network_mode"`
	AllowedIPs  []string `json:"allowed_ips"`
	MaxOutputKB int      `json:"max_output_kb"`
}

// ExecResult is the result of a sandbox execution.
type ExecResult struct {
	TaskID    string        `json:"task_id"`
	ExitCode  int           `json:"exit_code"`
	Duration  time.Duration `json:"duration"`
	TimedOut  bool          `json:"timed_out"`
	Truncated bool          `json:"truncated"`
	Killed    bool          `json:"killed"`
	Stats     Stats         `json:"stats"`
}

// Executor orchestrates sandboxed command and script execution.
type Executor struct {
	cfg    Config
	logger zerolog.Logger
	audit  *AuditLogger
	net    *NetworkManager
	sem    chan struct{}
}

// NewExecutor creates an Executor with the given configuration.
func NewExecutor(cfg Config, logger zerolog.Logger) *Executor {
	// Set defaults.
	if cfg.NsjailPath == "" {
		cfg.NsjailPath = "nsjail"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/work"
	}
	// CgroupBase is intentionally left empty by default, making cgroup features opt-in.
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = 128
	}
	if cfg.CPUPercent <= 0 {
		cfg.CPUPercent = 50
	}
	if cfg.MaxPIDs <= 0 {
		cfg.MaxPIDs = 32
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.MaxOutputKB <= 0 {
		cfg.MaxOutputKB = 512
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "disabled"
	}

	maxConcurrent := cfg.MaxConcurrentTasks
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}

	return &Executor{
		cfg:    cfg,
		logger: logger.With().Str("component", "sandbox-executor").Logger(),
		audit:  NewAuditLogger(logger, cfg.AuditLogPath),
		net:    NewNetworkManager(cfg.NetworkMode != "disabled"),
		sem:    make(chan struct{}, maxConcurrent),
	}
}

// Close releases resources held by the executor (e.g. audit log file handles).
func (e *Executor) Close() error {
	return e.audit.Close()
}

// ExecuteCommand validates the command against the policy and runs it in the sandbox.
func (e *Executor) ExecuteCommand(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateCommand(req.Command, req.Args); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}

	nsCfg := e.buildNsjailConfig(req)
	args := nsCfg.CommandArgs(req.TaskID, req.Command, req.Args)
	return e.run(ctx, req, nsCfg, args, outputSender)
}

// ExecuteScript validates the script against the policy, writes it to a temp file,
// and runs it in the sandbox.
func (e *Executor) ExecuteScript(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateScript(req.Interpreter, req.Script); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}

	nsCfg := e.buildNsjailConfig(req)
	scriptPath, err := nsCfg.WriteScriptFile(req.TaskID, req.Script)
	if err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}
	defer os.Remove(scriptPath)

	args, err := nsCfg.ScriptArgs(req.TaskID, req.Interpreter, scriptPath)
	if err != nil {
		return nil, fmt.Errorf("script args: %w", err)
	}
	return e.run(ctx, req, nsCfg, args, outputSender)
}

// run is the core execution path: sets up cgroup, creates streamer, runs nsjail,
// captures output, reads stats, and audit logs.
func (e *Executor) run(ctx context.Context, req ExecRequest, nsCfg NsjailConfig, nsjailArgs []string, outputSender OutputSender) (*ExecResult, error) {
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	taskID := req.TaskID
	startTime := time.Now()

	// Create cgroup for resource isolation (opt-in via CgroupBase).
	var cgroupPath string
	var err error
	if e.cfg.CgroupBase != "" {
		cgroupPath, err = CreateCgroup(e.cfg.CgroupBase, taskID)
		if err != nil {
			return nil, fmt.Errorf("create cgroup: %w", err)
		}
		defer func() {
			KillCgroupProcesses(cgroupPath)
			RemoveCgroup(cgroupPath)
		}()

		// Set cgroup limits.
		if err := SetCgroupLimits(cgroupPath, nsCfg.MemoryMB, nsCfg.CPUPercent, nsCfg.MaxPIDs); err != nil {
			return nil, fmt.Errorf("set cgroup limits: %w", err)
		}
	}

	// Set up network isolation if allowlist mode.
	if nsCfg.NetworkMode == "allowlist" && req.SandboxCfg != nil {
		if err := e.net.SetupAllowlistNetwork(taskID, req.SandboxCfg.AllowedIPs); err != nil {
			e.logger.Warn().Err(err).Str("task_id", taskID).Msg("failed to setup allowlist network")
		}
		defer e.net.CleanupNetwork(taskID)
	}

	// Determine timeout.
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = time.Duration(e.cfg.TimeoutSec) * time.Second
	}

	// Create context with timeout.
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Set up output streaming.
	maxOutputBytes := e.cfg.MaxOutputKB * 1024
	if req.SandboxCfg != nil && req.SandboxCfg.MaxOutputKB > 0 {
		maxOutputBytes = req.SandboxCfg.MaxOutputKB * 1024
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutStreamer := NewOutputStreamer(taskID, "stdout", maxOutputBytes/2, 100*time.Millisecond, func(data []byte) {
		stdoutBuf.Write(data)
		if outputSender != nil {
			outputSender(data)
		}
	})
	stderrStreamer := NewOutputStreamer(taskID, "stderr", maxOutputBytes/2, 100*time.Millisecond, func(data []byte) {
		stderrBuf.Write(data)
	})

	// Build the nsjail command.
	cmd := exec.CommandContext(execCtx, e.cfg.NsjailPath, nsjailArgs...)

	// Set environment variables — use a minimal allowlist rather than os.Environ()
	// to avoid leaking host secrets and block library injection attacks.
	cmd.Env = buildSandboxEnv(req.Env)

	// Pipe output.
	cmd.Stdout = stdoutStreamer
	cmd.Stderr = stderrStreamer

	// Run.
	err = cmd.Run()
	duration := time.Since(startTime)

	// Stop streamers and flush remaining.
	stdoutStreamer.Stop()
	stderrStreamer.Stop()

	result := &ExecResult{
		TaskID:   taskID,
		ExitCode: 0,
		Duration: duration,
	}

	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
			result.ExitCode = -1
			result.Killed = true
			// Kill cgroup processes on timeout.
			if cgroupPath != "" {
				KillCgroupProcesses(cgroupPath)
			}
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
			}
		}
	}

	// Check for truncation — each streamer gets maxOutputBytes/2.
	streamLimit := maxOutputBytes / 2
	if stdoutBuf.Len() >= streamLimit || stderrBuf.Len() >= streamLimit {
		result.Truncated = true
	}

	// Read cgroup stats.
	var statsPtr *Stats
	if cgroupPath != "" {
		if stats, err := ReadCgroupStats(cgroupPath); err == nil {
			result.Stats = *stats
			statsPtr = stats
		}
	}

	// Audit log.
	e.audit.LogExecution(AuditEvent{
		TaskID:      taskID,
		TriggeredBy: "executor",
		Type:        commandOrScript(req),
		Command:     req.Command,
		Interpreter: req.Interpreter,
		ExitCode:    result.ExitCode,
		Duration:    result.Duration,
		TimedOut:    result.TimedOut,
		Truncated:   result.Truncated,
		Killed:      result.Killed,
		Stats:       statsPtr,
	})

	return result, nil
}

// buildNsjailConfig creates an NsjailConfig from the executor config and per-request overrides.
func (e *Executor) buildNsjailConfig(req ExecRequest) NsjailConfig {
	cfg := NsjailConfig{
		TimeLimit:   e.cfg.TimeoutSec,
		MemoryMB:    e.cfg.MemoryMB,
		CPUPercent:  e.cfg.CPUPercent,
		MaxPIDs:     e.cfg.MaxPIDs,
		NetworkMode: e.cfg.NetworkMode,
		WorkDir:     e.cfg.WorkDir,
	}

	if req.SandboxCfg != nil {
		if req.SandboxCfg.MemoryMB > 0 {
			cfg.MemoryMB = min(req.SandboxCfg.MemoryMB, 1024)
		}
		if req.SandboxCfg.CPUPercent > 0 {
			cfg.CPUPercent = min(req.SandboxCfg.CPUPercent, 100)
		}
		if req.SandboxCfg.MaxPIDs > 0 {
			cfg.MaxPIDs = min(req.SandboxCfg.MaxPIDs, 256)
		}
		if req.SandboxCfg.NetworkMode != "" {
			cfg.NetworkMode = req.SandboxCfg.NetworkMode
		}
		if len(req.SandboxCfg.AllowedIPs) > 0 {
			cfg.AllowedIPs = req.SandboxCfg.AllowedIPs
		}
	}

	return cfg
}

// commandOrScript returns "command" or "script" based on the request.
func commandOrScript(req ExecRequest) string {
	if req.Script != "" {
		return "script"
	}
	return "command"
}

// buildSandboxEnv constructs a minimal environment for the sandboxed process.
// It starts with a safe allowlist (PATH, HOME, LANG) and merges in request-specified
// variables, blocking dangerous ones like LD_PRELOAD, LD_LIBRARY_PATH, and
// DYLD_INSERT_LIBRARIES that could be used to inject code into the child process.
func buildSandboxEnv(reqEnv map[string]string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"LANG=C",
	}
	for k, v := range reqEnv {
		if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "DYLD_INSERT_LIBRARIES" {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
