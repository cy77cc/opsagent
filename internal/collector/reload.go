package collector

import (
	"context"

	"github.com/cy77cc/opsagent/internal/config"
	"github.com/rs/zerolog"
)

// CollectorReloader implements config.Reloader for the collector pipeline.
type CollectorReloader struct {
	scheduler *Scheduler
	logger    zerolog.Logger
}

// NewCollectorReloader creates a CollectorReloader.
func NewCollectorReloader(scheduler *Scheduler, logger zerolog.Logger) *CollectorReloader {
	return &CollectorReloader{scheduler: scheduler, logger: logger}
}

// CanReload returns true if the collector config changed.
func (r *CollectorReloader) CanReload(cs *config.ChangeSet) bool {
	return cs.CollectorChanged
}

// Apply converts config.CollectorConfig to ReloadConfig and calls Scheduler.Reload.
func (r *CollectorReloader) Apply(newCfg *config.Config) error {
	rc := toReloadConfig(newCfg.Collector)
	return r.scheduler.Reload(context.Background(), rc)
}

// Rollback restores the old collector config.
func (r *CollectorReloader) Rollback(oldCfg *config.Config) error {
	rc := toReloadConfig(oldCfg.Collector)
	return r.scheduler.Reload(context.Background(), rc)
}

func toReloadConfig(cc config.CollectorConfig) ReloadConfig {
	rc := ReloadConfig{}
	for _, in := range cc.Inputs {
		rc.Inputs = append(rc.Inputs, PluginConfig{Type: in.Type, Config: in.Config})
	}
	for _, p := range cc.Processors {
		rc.Processors = append(rc.Processors, PluginConfig{Type: p.Type, Config: p.Config})
	}
	for _, a := range cc.Aggregators {
		rc.Aggregators = append(rc.Aggregators, PluginConfig{Type: a.Type, Config: a.Config})
	}
	for _, o := range cc.Outputs {
		rc.Outputs = append(rc.Outputs, PluginConfig{Type: o.Type, Config: o.Config})
	}
	return rc
}
