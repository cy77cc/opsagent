package reporter

import (
	"context"

	"github.com/rs/zerolog"
)

// Reporter ships or prints metric payloads.
type Reporter interface {
	Report(ctx context.Context, payload any) error
}

// StdoutReporter logs payloads in structured JSON fields.
type StdoutReporter struct {
	logger zerolog.Logger
}

// NewStdoutReporter creates a reporter that writes to logs.
func NewStdoutReporter(logger zerolog.Logger) *StdoutReporter {
	return &StdoutReporter{logger: logger}
}

// Report emits the payload as a structured log event.
func (r *StdoutReporter) Report(_ context.Context, payload any) error {
	r.logger.Info().Interface("payload", payload).Msg("metrics reported")
	return nil
}
