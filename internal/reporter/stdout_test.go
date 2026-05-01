package reporter

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

func TestNewStdoutReporter(t *testing.T) {
	r := NewStdoutReporter(zerolog.Nop())
	if r == nil {
		t.Fatal("expected non-nil reporter")
	}
}

func TestStdoutReporter_Report(t *testing.T) {
	r := NewStdoutReporter(zerolog.Nop())
	err := r.Report(context.Background(), map[string]any{"cpu": 42.5, "mem": "8Gi"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestStdoutReporter_ReportNilPayload(t *testing.T) {
	r := NewStdoutReporter(zerolog.Nop())
	err := r.Report(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error for nil payload, got %v", err)
	}
}

func TestStdoutReporter_ImplementsReporter(t *testing.T) {
	var _ Reporter = (*StdoutReporter)(nil)
}
