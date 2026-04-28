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
	NetworkMode string `json:"network_mode"`
	Policy      Policy `json:"policy"`
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
	if cfg.CgroupBase == "" {
		cfg.CgroupBase = "/sys/fs/cgroup"
	}
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

	return &Executor{
		cfg:    cfg,
		logger: logger.With().Str("component", "sandbox-executor").Logger(),
		audit:  NewAuditLogger(logger),
		net:    NewNetworkManager(cfg.NetworkMode != "disabled"),
	}
}

// ExecuteCommand validates the command against the policy and runs it in the sandbox.
func (e *Executor) ExecuteCommand(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateCommand(req.Command, req.Args); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}

	nsCfg := e.buildNsjailConfig(req)
	args := nsCfg.CommandArgs(req.Command, req.Args)
	return e.run(ctx, req, args, outputSender)
}

// ExecuteScript validates the script against the policy, writes it to a temp file,
// and runs it in the sandbox.
func (e *Executor) ExecuteScript(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateScript(req.Interpreter, req.Script); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}

	nsCfg := e.buildNsjailConfig(req)
	args := nsCfg.ScriptArgs(req.Interpreter, req.Script)
	return e.run(ctx, req, args, outputSender)
}

// run is the core execution path: sets up cgroup, creates streamer, runs nsjail,
// captures output, reads stats, and audit logs.
func (e *Executor) run(ctx context.Context, req ExecRequest, nsjailArgs []string, outputSender OutputSender) (*ExecResult, error) {
	taskID := req.TaskID
	startTime := time.Now()

	// Create cgroup for resource isolation.
	cgroupPath, err := CreateCgroup(e.cfg.CgroupBase, taskID)
	if err != nil {
		return nil, fmt.Errorf("create cgroup: %w", err)
	}
	defer RemoveCgroup(cgroupPath)

	// Set cgroup limits.
	nsCfg := e.buildNsjailConfig(req)
	if err := SetCgroupLimits(cgroupPath, nsCfg.MemoryMB, nsCfg.CPUPercent, nsCfg.MaxPIDs); err != nil {
		return nil, fmt.Errorf("set cgroup limits: %w", err)
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

	// Set environment variables.
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

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
		} else {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				result.ExitCode = exitErr.ExitCode()
			} else {
				result.ExitCode = -1
			}
		}
	}

	// Check for truncation.
	if stdoutBuf.Len() >= maxOutputBytes || stderrBuf.Len() >= maxOutputBytes {
		result.Truncated = true
	}

	// Read cgroup stats.
	if stats, err := ReadCgroupStats(cgroupPath); err == nil {
		result.Stats = *stats
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
			cfg.MemoryMB = req.SandboxCfg.MemoryMB
		}
		if req.SandboxCfg.CPUPercent > 0 {
			cfg.CPUPercent = req.SandboxCfg.CPUPercent
		}
		if req.SandboxCfg.MaxPIDs > 0 {
			cfg.MaxPIDs = req.SandboxCfg.MaxPIDs
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
