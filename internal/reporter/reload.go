package reporter

import (
	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

// ReporterReloader implements config.Reloader for reporter configuration.
type ReporterReloader struct {
	logger zerolog.Logger
}

// NewReporterReloader creates a ReporterReloader.
func NewReporterReloader(logger zerolog.Logger) *ReporterReloader {
	return &ReporterReloader{logger: logger}
}

// CanReload returns true if reporter config changed.
func (r *ReporterReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.ReporterChanged
}

// Apply logs the reporter config change. The reporter is typically re-created
// at the agent level since it's a simple struct with no mutable state.
func (r *ReporterReloader) Apply(newCfg *config.Config) error {
	r.logger.Info().
		Str("mode", newCfg.Reporter.Mode).
		Int("timeout_seconds", newCfg.Reporter.TimeoutSeconds).
		Msg("reporter config updated")
	return nil
}

// Rollback logs the rollback.
func (r *ReporterReloader) Rollback(oldCfg *config.Config) error {
	r.logger.Info().
		Str("mode", oldCfg.Reporter.Mode).
		Int("timeout_seconds", oldCfg.Reporter.TimeoutSeconds).
		Msg("reporter config rolled back")
	return nil
}
