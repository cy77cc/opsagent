package tagger

import (
	"github.com/cy77cc/nodeagentx/internal/collector"
)

// Condition defines a conditional tag addition rule.
type Condition struct {
	Tag      string `mapstructure:"tag"`
	Value    string `mapstructure:"value"`
	WhenName string `mapstructure:"when_name"`
}

// Config holds the tagger processor configuration.
type Config struct {
	Tags       map[string]string `mapstructure:"tags"`
	Conditions []Condition       `mapstructure:"conditions"`
}

// Processor adds static and conditional tags to metrics.
type Processor struct {
	staticTags map[string]string
	conditions []Condition
}

// New creates a new tagger Processor from the given config.
func New(cfg Config) *Processor {
	staticTags := make(map[string]string, len(cfg.Tags))
	for k, v := range cfg.Tags {
		staticTags[k] = v
	}
	conditions := make([]Condition, len(cfg.Conditions))
	copy(conditions, cfg.Conditions)
	return &Processor{
		staticTags: staticTags,
		conditions: conditions,
	}
}

// Apply adds static tags to all metrics and conditional tags when metric name matches.
func (p *Processor) Apply(in []*collector.Metric) []*collector.Metric {
	for _, m := range in {
		// Add static tags to every metric.
		for k, v := range p.staticTags {
			m.AddTag(k, v)
		}
		// Add conditional tags when metric name matches.
		for _, cond := range p.conditions {
			if m.Name() == cond.WhenName {
				m.AddTag(cond.Tag, cond.Value)
			}
		}
	}
	return in
}

// SampleConfig returns a sample TOML configuration.
func (p *Processor) SampleConfig() string {
	return `
[tags]
  env = "production"
  region = "us-east-1"

[[conditions]]
  tag = "critical"
  value = "true"
  when_name = "disk_usage"
`
}

func init() {
	collector.RegisterProcessor("tagger", func() collector.Processor {
		return New(Config{})
	})
}
