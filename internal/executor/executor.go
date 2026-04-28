package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Request is an exec request payload.
type Request struct {
	Command        string   `json:"command"`
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

// Result is an exec response payload.
type Result struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMS int64  `json:"duration_ms"`
}

// Executor runs restricted commands safely.
type Executor struct {
	allowed        map[string]struct{}
	defaultTimeout time.Duration
	maxOutputBytes int
}

// New creates an executor with whitelist and runtime bounds.
func New(allowedCommands []string, defaultTimeout time.Duration, maxOutputBytes int) *Executor {
	if len(allowedCommands) == 0 {
		allowedCommands = DefaultAllowedCommands
	}
	if defaultTimeout <= 0 {
		defaultTimeout = 10 * time.Second
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 * 1024
	}

	return &Executor{
		allowed:        buildWhitelist(allowedCommands),
		defaultTimeout: defaultTimeout,
		maxOutputBytes: maxOutputBytes,
	}
}

// Execute validates and executes one command under timeout and output bounds.
func (e *Executor) Execute(ctx context.Context, req Request) (*Result, error) {
	cmdName := strings.TrimSpace(req.Command)
	if cmdName == "" {
		return nil, fmt.Errorf("command is required")
	}
	if _, ok := e.allowed[cmdName]; !ok {
		return nil, fmt.Errorf("command %q is not allowed", cmdName)
	}

	timeout := e.defaultTimeout
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, cmdName, req.Args...)

	stdoutBuf := newLimitedBuffer(e.maxOutputBytes)
	stderrBuf := newLimitedBuffer(e.maxOutputBytes)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	started := time.Now()
	err := cmd.Run()
	duration := time.Since(started)

	result := &Result{
		ExitCode:   0,
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMS: duration.Milliseconds(),
	}

	if stdoutBuf.Truncated() {
		result.Stdout += "\n[truncated]"
	}
	if stderrBuf.Truncated() {
		result.Stderr += "\n[truncated]"
	}

	if err == nil {
		return result, nil
	}

	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		result.ExitCode = -1
		if result.Stderr == "" {
			result.Stderr = "command timed out"
		}
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	return nil, fmt.Errorf("execute command %q: %w", cmdName, err)
}

type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newLimitedBuffer(max int) *limitedBuffer {
	return &limitedBuffer{max: max}
}

func (b *limitedBuffer) Write(p []byte) (n int, err error) {
	if b.max <= 0 {
		return len(p), nil
	}

	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		toWrite := p
		if len(toWrite) > remaining {
			toWrite = toWrite[:remaining]
			b.truncated = true
		}
		_, _ = b.buf.Write(toWrite)
	} else {
		b.truncated = true
	}

	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func (b *limitedBuffer) Truncated() bool {
	return b.truncated
}
