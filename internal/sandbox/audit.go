package sandbox

import (
	"time"

	"github.com/rs/zerolog"
)

// AuditEvent captures the details of a sandbox execution for audit logging.
type AuditEvent struct {
	TaskID      string        `json:"task_id"`
	Timestamp   time.Time     `json:"timestamp"`
	TriggeredBy string        `json:"triggered_by"`
	Type        string        `json:"type"`
	Command     string        `json:"command"`
	Interpreter string        `json:"interpreter,omitempty"`
	ExitCode    int           `json:"exit_code"`
	Duration    time.Duration `json:"duration"`
	TimedOut    bool          `json:"timed_out"`
	Truncated   bool          `json:"truncated"`
	Killed      bool          `json:"killed"`
	Stats       string        `json:"stats,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// AuditLogger wraps a zerolog.Logger for sandbox audit events.
type AuditLogger struct {
	logger zerolog.Logger
}

// NewAuditLogger creates an AuditLogger backed by the provided zerolog.Logger.
func NewAuditLogger(logger zerolog.Logger) *AuditLogger {
	return &AuditLogger{
		logger: logger.With().Str("component", "sandbox-audit").Logger(),
	}
}

// LogExecution records a sandbox execution event with the appropriate log level.
func (al *AuditLogger) LogExecution(event AuditEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	ev := al.logger.Info()
	if event.Error != "" {
		ev = al.logger.Error()
	} else if event.ExitCode != 0 {
		ev = al.logger.Warn()
	}

	ev.
		Str("task_id", event.TaskID).
		Time("timestamp", event.Timestamp).
		Str("triggered_by", event.TriggeredBy).
		Str("type", event.Type).
		Str("command", event.Command).
		Str("interpreter", event.Interpreter).
		Int("exit_code", event.ExitCode).
		Dur("duration", event.Duration).
		Bool("timed_out", event.TimedOut).
		Bool("truncated", event.Truncated).
		Bool("killed", event.Killed).
		Str("stats", event.Stats).
		Str("error", event.Error).
		Msg("sandbox execution")
}
