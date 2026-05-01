package sandbox

import (
	"io"
	"os"
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
	Stats       *Stats        `json:"stats,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// AuditLogger wraps a zerolog.Logger for sandbox audit events.
type AuditLogger struct {
	logger zerolog.Logger
	closer io.Closer
}

// NewAuditLogger creates an AuditLogger backed by the provided zerolog.Logger.
// If logPath is non-empty, audit events are also written to that file.
func NewAuditLogger(logger zerolog.Logger, logPath string) *AuditLogger {
	base := logger.With().Str("component", "sandbox-audit")

	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			multi := zerolog.MultiLevelWriter(logger, f)
			return &AuditLogger{
				logger: zerolog.New(multi).With().Str("component", "sandbox-audit").Logger(),
				closer: f,
			}
		}
		// Fall through to stderr-only on error.
		logger.Warn().Err(err).Str("path", logPath).Msg("failed to open audit log file")
	}

	return &AuditLogger{
		logger: base.Logger(),
	}
}

// Close closes the underlying file writer, if any.
func (al *AuditLogger) Close() error {
	if al.closer != nil {
		return al.closer.Close()
	}
	return nil
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
		Str("error", event.Error)

	// Log stats fields individually if available.
	if event.Stats != nil {
		ev = ev.
			Int64("stats_peak_memory_bytes", event.Stats.PeakMemoryBytes).
			Int64("stats_cpu_time_user_ms", event.Stats.CPUTimeUserMs).
			Int64("stats_cpu_time_system_ms", event.Stats.CPUTimeSystemMs).
			Int32("stats_process_count", event.Stats.ProcessCount).
			Int64("stats_bytes_written", event.Stats.BytesWritten).
			Int64("stats_bytes_read", event.Stats.BytesRead)
	}

	ev.Msg("sandbox execution")
}
