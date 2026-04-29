package regex

import (
	"fmt"
	"regexp"

	"github.com/cy77cc/opsagent/internal/collector"
)

// Rule defines a single tag regex replacement rule.
type Rule struct {
	Key         string `mapstructure:"key"`
	Pattern     string `mapstructure:"pattern"`
	Replacement string `mapstructure:"replacement"`
	compiled    *regexp.Regexp
}

// Config holds the regex processor configuration.
type Config struct {
	Tags []Rule `mapstructure:"tags"`
}

// Processor applies regex replacements to metric tag values.
type Processor struct {
	rules []Rule
}

// New creates a new regex Processor from the given config.
func New(cfg Config) (*Processor, error) {
	for i, r := range cfg.Tags {
		if r.Key == "" {
			return nil, fmt.Errorf("regex rule %d: key must not be empty", i)
		}
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("regex rule %d: invalid pattern %q: %w", i, r.Pattern, err)
		}
		cfg.Tags[i].compiled = re
	}
	return &Processor{rules: cfg.Tags}, nil
}

// Init parses configuration from a map (e.g. from YAML unmarshaling) and
// compiles regex patterns. Each entry in "tags" is expected to be a map
// with "key", "pattern", and "replacement" fields.
func (p *Processor) Init(cfg map[string]interface{}) error {
	raw, ok := cfg["tags"]
	if !ok {
		return nil
	}
	tagList, ok := raw.([]interface{})
	if !ok {
		return fmt.Errorf("regex: \"tags\" must be a list, got %T", raw)
	}

	rules := make([]Rule, 0, len(tagList))
	for i, entry := range tagList {
		tagMap, ok := entry.(map[string]interface{})
		if !ok {
			return fmt.Errorf("regex: tag entry %d must be a map, got %T", i, entry)
		}
		key, _ := tagMap["key"].(string)
		if key == "" {
			return fmt.Errorf("regex: tag entry %d: key must not be empty", i)
		}
		pattern, _ := tagMap["pattern"].(string)
		replacement, _ := tagMap["replacement"].(string)

		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("regex: tag entry %d: invalid pattern %q: %w", i, pattern, err)
		}
		rules = append(rules, Rule{
			Key:         key,
			Pattern:     pattern,
			Replacement: replacement,
			compiled:    re,
		})
	}
	p.rules = rules
	return nil
}

// Apply applies regex replacements to metric tag values.
func (p *Processor) Apply(in []*collector.Metric) []*collector.Metric {
	for _, m := range in {
		for _, rule := range p.rules {
			tags := m.Tags()
			val, ok := tags[rule.Key]
			if !ok {
				continue
			}
			replaced := rule.compiled.ReplaceAllString(val, rule.Replacement)
			m.AddTag(rule.Key, replaced)
		}
	}
	return in
}

// SampleConfig returns a sample TOML configuration.
func (p *Processor) SampleConfig() string {
	return `
[[tags]]
  key = "hostname"
  pattern = "\\d+"
  replacement = "REDACTED"
`
}

func init() {
	collector.RegisterProcessor("regex", func() collector.Processor {
		return &Processor{}
	})
}
