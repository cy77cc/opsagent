package reporter

import (
	"testing"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

func TestReporterReloader_CanReload(t *testing.T) {
	r := NewReporterReloader(zerolog.Nop())
	cs := &config.ChangeSet{ReporterChanged: true}
	if !r.CanReload(cs) {
		t.Error("expected CanReload = true when ReporterChanged")
	}
	cs2 := &config.ChangeSet{ReporterChanged: false}
	if r.CanReload(cs2) {
		t.Error("expected CanReload = false when ReporterChanged is false")
	}
}

func TestReporterReloader_Apply(t *testing.T) {
	r := NewReporterReloader(zerolog.Nop())
	cfg := &config.Config{
		Reporter: config.ReporterConfig{Mode: "stdout", TimeoutSeconds: 10},
	}
	if err := r.Apply(cfg); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}

func TestReporterReloader_Rollback(t *testing.T) {
	r := NewReporterReloader(zerolog.Nop())
	cfg := &config.Config{
		Reporter: config.ReporterConfig{Mode: "stdout", TimeoutSeconds: 5},
	}
	if err := r.Rollback(cfg); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
}
