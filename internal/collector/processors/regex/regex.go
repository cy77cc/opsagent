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
		// Return a default-configured processor; real usage would load config.
		p, _ := New(Config{})
		return p
	})
}
