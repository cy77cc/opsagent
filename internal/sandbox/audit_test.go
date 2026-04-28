package sandbox

import (
	"bytes"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestAuditLoggerLogExecution(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()
	al := NewAuditLogger(logger)

	al.LogExecution(AuditEvent{
		TaskID:      "task-001",
		TriggeredBy: "agent-1",
		Type:        "command",
		Command:     "echo hello",
		ExitCode:    0,
		Duration:    100 * time.Millisecond,
	})

	output := buf.String()
	if output == "" {
		t.Fatal("expected log output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("task-001")) {
		t.Error("expected task_id in output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("echo hello")) {
		t.Error("expected command in output")
	}
	if !bytes.Contains(buf.Bytes(), []byte("sandbox execution")) {
		t.Error("expected message in output")
	}
}

func TestAuditLoggerLogExecutionWithError(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()
	al := NewAuditLogger(logger)

	al.LogExecution(AuditEvent{
		TaskID:  "task-002",
		Command: "bad-cmd",
		Error:   "command blocked",
	})

	if !bytes.Contains(buf.Bytes(), []byte("command blocked")) {
		t.Error("expected error message in output")
	}
}

func TestAuditLoggerLogExecutionNonZeroExit(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf).With().Timestamp().Logger()
	al := NewAuditLogger(logger)

	al.LogExecution(AuditEvent{
		TaskID:   "task-003",
		Command:  "false",
		ExitCode: 1,
		Duration: 50 * time.Millisecond,
	})

	output := buf.String()
	if output == "" {
		t.Fatal("expected log output for non-zero exit")
	}
}
