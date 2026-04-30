package app

import (
	"encoding/json"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// AuditEvent represents a structured audit log entry.
type AuditEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	EventType string                 `json:"event_type"`
	Component string                 `json:"component"`
	Action    string                 `json:"action"`
	Status    string                 `json:"status"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// AuditLogger writes structured audit events to a JSON-lines file with rotation.
type AuditLogger struct {
	mu     sync.Mutex
	logger *lumberjack.Logger
}

// NewAuditLogger creates an AuditLogger writing to path with rotation.
func NewAuditLogger(path string, maxSizeMB, maxBackups int) (*AuditLogger, error) {
	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		Compress:   true,
	}
	return &AuditLogger{logger: lj}, nil
}

// Log writes an audit event. Timestamp is set automatically if zero.
// No-op if the receiver is nil (disabled audit).
func (a *AuditLogger) Log(event AuditEvent) {
	if a == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')
	a.logger.Write(data)
}

// Close flushes and closes the audit log file.
func (a *AuditLogger) Close() error {
	if a == nil {
		return nil
	}
	return a.logger.Close()
}
