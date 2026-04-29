package promrw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

const (
	defaultTimeout = 10 * time.Second
)

func init() {
	collector.RegisterOutput("prometheus_remote_write", func() collector.Output {
		return &PromRWOutput{}
	})
}

// PromRWOutput writes metrics in Prometheus remote write JSON format.
type PromRWOutput struct {
	url     string
	timeout time.Duration
	client  *http.Client
}

type label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type sample struct {
	Value     float64 `json:"value"`
	Timestamp int64   `json:"timestamp"`
}

type timeseries struct {
	Labels  []label  `json:"labels"`
	Samples []sample `json:"samples"`
}

type remoteWritePayload struct {
	TimeSeries []timeseries `json:"timeseries"`
}

// Init configures the Prometheus Remote Write output from the provided config map.
func (p *PromRWOutput) Init(cfg map[string]interface{}) error {
	url, ok := cfg["url"].(string)
	if !ok || url == "" {
		return fmt.Errorf("prometheus_remote_write: url is required")
	}
	p.url = url

	p.timeout = defaultTimeout
	if v, ok := cfg["timeout"].(int); ok && v > 0 {
		p.timeout = time.Duration(v) * time.Second
	}

	p.client = &http.Client{Timeout: p.timeout}
	return nil
}

// Write converts metrics to Prometheus remote write JSON format and POSTs them.
func (p *PromRWOutput) Write(metrics []collector.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	payload := p.buildPayload(metrics)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("prometheus_remote_write: failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("prometheus_remote_write: failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus_remote_write: request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("prometheus_remote_write: server returned status %d", resp.StatusCode)
	}

	return nil
}

func (p *PromRWOutput) buildPayload(metrics []collector.Metric) remoteWritePayload {
	series := make([]timeseries, len(metrics))
	for i, m := range metrics {
		// Build labels with __name__ first, then sorted tag keys.
		tags := m.Tags()
		tagKeys := make([]string, 0, len(tags))
		for k := range tags {
			tagKeys = append(tagKeys, k)
		}
		sort.Strings(tagKeys)

		labels := make([]label, 0, len(tags)+1)
		labels = append(labels, label{Name: "__name__", Value: m.Name()})
		for _, k := range tagKeys {
			labels = append(labels, label{Name: k, Value: tags[k]})
		}

		// Extract numeric value from fields.
		value := extractNumericValue(m.Fields())

		series[i] = timeseries{
			Labels: labels,
			Samples: []sample{
				{
					Value:     value,
					Timestamp: m.Timestamp().UnixMilli(),
				},
			},
		}
	}

	return remoteWritePayload{TimeSeries: series}
}

// Close is a no-op for the Prometheus Remote Write output.
func (p *PromRWOutput) Close() error {
	return nil
}

// SampleConfig returns a sample configuration for the Prometheus Remote Write output.
func (p *PromRWOutput) SampleConfig() string {
	return `
  [outputs.prometheus_remote_write]
    url = "http://localhost:9090/api/v1/write"
    timeout = 10
`
}

func extractNumericValue(fields map[string]interface{}) float64 {
	for _, key := range []string{"value", "count", "gauge"} {
		if v, ok := fields[key]; ok {
			if f, ok := toFloat64(v); ok {
				return f
			}
		}
	}
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
