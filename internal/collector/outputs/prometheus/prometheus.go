package prometheus

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cy77cc/nodeagentx/internal/collector"
)

const (
	defaultPath = "/metrics"
	defaultAddr = ":9100"
)

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func init() {
	collector.RegisterOutput("prometheus", func() collector.Output {
		return &PrometheusOutput{}
	})
}

// PrometheusOutput serves metrics in Prometheus text exposition format.
type PrometheusOutput struct {
	addr   string
	path   string
	mu     sync.RWMutex
	latest map[string]*collector.Metric
	server *http.Server
}

// Init configures the Prometheus output from the provided config map.
func (p *PrometheusOutput) Init(cfg map[string]interface{}) error {
	p.path = defaultPath
	if v, ok := cfg["path"].(string); ok && v != "" {
		p.path = v
	}

	p.addr = defaultAddr
	if v, ok := cfg["addr"].(string); ok && v != "" {
		p.addr = v
	}

	p.latest = make(map[string]*collector.Metric)
	return nil
}

// Start begins the HTTP server for Prometheus scraping.
func (p *PrometheusOutput) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(p.path, p.handleMetrics)

	p.server = &http.Server{
		Addr:              p.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("prometheus output: server error: %v\n", err)
		}
	}()

	return nil
}

// Write stores the latest metrics for scraping.
func (p *PrometheusOutput) Write(metrics []collector.Metric) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range metrics {
		m := &metrics[i]
		p.latest[m.Name()] = m
	}
	return nil
}

// Close shuts down the HTTP server.
func (p *PrometheusOutput) Close() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

// SampleConfig returns a sample configuration for the Prometheus output.
func (p *PrometheusOutput) SampleConfig() string {
	return `
  [outputs.prometheus]
    path = "/metrics"
    addr = ":9100"
`
}

func (p *PrometheusOutput) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprint(w, p.renderPrometheus())
}

func (p *PrometheusOutput) renderPrometheus() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.latest) == 0 {
		return ""
	}

	// Sort names for deterministic output.
	names := make([]string, 0, len(p.latest))
	for name := range p.latest {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	for _, name := range names {
		m := p.latest[name]
		sanitizedName := SanitizeName(m.Name())

		// Determine the metric type string.
		typeStr := "gauge"
		if m.Type() == collector.Counter {
			typeStr = "counter"
		}

		sb.WriteString("# TYPE ")
		sb.WriteString(sanitizedName)
		sb.WriteString(" ")
		sb.WriteString(typeStr)
		sb.WriteString("\n")

		// Build labels.
		tags := m.Tags()
		tagKeys := make([]string, 0, len(tags))
		for k := range tags {
			tagKeys = append(tagKeys, k)
		}
		sort.Strings(tagKeys)

		sb.WriteString(sanitizedName)
		if len(tagKeys) > 0 {
			sb.WriteString("{")
			for i, k := range tagKeys {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString(SanitizeName(k))
				sb.WriteString("=\"")
				sb.WriteString(tags[k])
				sb.WriteString("\"")
			}
			sb.WriteString("}")
		}

		// Write field values (use first numeric field).
		fields := m.Fields()
		value := extractNumericValue(fields)
		sb.WriteString(" ")
		sb.WriteString(fmt.Sprintf("%v", value))

		// Write timestamp in milliseconds.
		sb.WriteString(" ")
		sb.WriteString(fmt.Sprintf("%d", m.Timestamp().UnixMilli()))
		sb.WriteString("\n")
	}

	return sb.String()
}

// SanitizeName replaces invalid characters with underscores and ensures
// the name starts with a letter or underscore.
func SanitizeName(name string) string {
	sanitized := invalidNameChars.ReplaceAllString(name, "_")
	if len(sanitized) > 0 && sanitized[0] >= '0' && sanitized[0] <= '9' {
		sanitized = "_" + sanitized
	}
	return sanitized
}

func extractNumericValue(fields map[string]interface{}) float64 {
	// Try common field names first.
	for _, key := range []string{"value", "count", "gauge"} {
		if v, ok := fields[key]; ok {
			if f, ok := toFloat64(v); ok {
				return f
			}
		}
	}
	// Fall back to first field.
	for _, v := range fields {
		if f, ok := toFloat64(v); ok {
			return f
		}
	}
	return 0
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	default:
		return 0, false
	}
}
