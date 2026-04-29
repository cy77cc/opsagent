package tagger

import (
	"fmt"

	"github.com/cy77cc/opsagent/internal/collector"
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

// Init parses configuration from a map (e.g. from YAML unmarshaling).
// Expects "tags" as a map[string]interface{} and "conditions" as []interface{}
// where each entry is a map with "tag", "value", and "when_name" fields.
func (p *Processor) Init(cfg map[string]interface{}) error {
	// Parse static tags.
	if rawTags, ok := cfg["tags"]; ok {
		tagsMap, ok := rawTags.(map[string]interface{})
		if !ok {
			return fmt.Errorf("tagger: \"tags\" must be a map, got %T", rawTags)
		}
		p.staticTags = make(map[string]string, len(tagsMap))
		for k, v := range tagsMap {
			p.staticTags[k] = fmt.Sprintf("%v", v)
		}
	}

	// Parse conditions.
	if rawConds, ok := cfg["conditions"]; ok {
		condList, ok := rawConds.([]interface{})
		if !ok {
			return fmt.Errorf("tagger: \"conditions\" must be a list, got %T", rawConds)
		}
		p.conditions = make([]Condition, 0, len(condList))
		for i, entry := range condList {
			condMap, ok := entry.(map[string]interface{})
			if !ok {
				return fmt.Errorf("tagger: condition entry %d must be a map, got %T", i, entry)
			}
			tag, _ := condMap["tag"].(string)
			value, _ := condMap["value"].(string)
			whenName, _ := condMap["when_name"].(string)
			p.conditions = append(p.conditions, Condition{
				Tag:      tag,
				Value:    value,
				WhenName: whenName,
			})
		}
	}
	return nil
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
		return &Processor{}
	})
}
