package config

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"
)

// Reloader is implemented by each subsystem that supports hot-reload.
type Reloader interface {
	CanReload(cs *ChangeSet) bool
	Apply(newCfg *Config) error
	Rollback(oldCfg *Config) error
}

// ConfigReloader orchestrates config hot-reload with atomic rollback.
type ConfigReloader struct {
	current   *Config
	mu        sync.Mutex
	reloaders []Reloader
	logger    zerolog.Logger
}

// NewConfigReloader creates a ConfigReloader with the given initial config and reloaders.
func NewConfigReloader(current *Config, logger zerolog.Logger, reloaders ...Reloader) *ConfigReloader {
	return &ConfigReloader{
		current:   current,
		reloaders: reloaders,
		logger:    logger,
	}
}

// Current returns the current config snapshot.
func (r *ConfigReloader) Current() *Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

// parseYAMLConfig parses YAML bytes into a Config struct using viper
// so that mapstructure tags are respected.
func parseYAMLConfig(data []byte) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return cfg, nil
}

// Apply parses newYAML, diffs against current config, and atomically applies reloadable changes.
func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newCfg, err := parseYAMLConfig(newYAML)
	if err != nil {
		return err
	}

	cs, nonReloadable, err := Diff(r.current, newCfg)
	if err != nil {
		return fmt.Errorf("diff config: %w", err)
	}

	if len(nonReloadable) > 0 {
		fields := make([]string, len(nonReloadable))
		for i, nr := range nonReloadable {
			fields[i] = nr.Field
		}
		return fmt.Errorf("non-reloadable changes rejected (restart required): %v", fields)
	}

	var applied []Reloader
	for _, rel := range r.reloaders {
		if !rel.CanReload(cs) {
			continue
		}
		if err := rel.Apply(newCfg); err != nil {
			// Rollback all previously applied reloaders.
			for i := len(applied) - 1; i >= 0; i-- {
				if rbErr := applied[i].Rollback(r.current); rbErr != nil {
					r.logger.Error().Err(rbErr).Msg("rollback failed during partial apply")
				}
			}
			return fmt.Errorf("apply reloader failed: %w", err)
		}
		applied = append(applied, rel)
	}

	r.current = newCfg
	return nil
}
