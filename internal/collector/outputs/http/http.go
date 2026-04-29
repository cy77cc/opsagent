package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

const (
	defaultTimeout       = 10 * time.Second
	defaultBatchSize     = 1000
	defaultRetryCount    = 3
	defaultRetryInterval = 500 * time.Millisecond
)

func init() {
	collector.RegisterOutput("http", func() collector.Output {
		return &HTTPOutput{}
	})
}

// HTTPOutput writes metrics as JSON to an HTTP endpoint.
type HTTPOutput struct {
	url           string
	timeout       time.Duration
	batchSize     int
	retryCount    int
	retryInterval time.Duration
	client        *http.Client
}

type metricJSON struct {
	Name      string                 `json:"name"`
	Tags      map[string]string      `json:"tags"`
	Fields    map[string]interface{} `json:"fields"`
	Timestamp int64                  `json:"timestamp"`
}

type payload struct {
	Metrics []metricJSON `json:"metrics"`
	Count   int          `json:"count"`
}

// Init configures the HTTP output from the provided config map.
func (h *HTTPOutput) Init(cfg map[string]interface{}) error {
	url, ok := cfg["url"].(string)
	if !ok || url == "" {
		return fmt.Errorf("http output: url is required")
	}
	h.url = url

	// Apply defaults, then override with config values.
	h.timeout = defaultTimeout
	if v, ok := cfg["timeout"].(int); ok && v > 0 {
		h.timeout = time.Duration(v) * time.Second
	}

	h.batchSize = defaultBatchSize
	if v, ok := cfg["batch_size"].(int); ok && v > 0 {
		h.batchSize = v
	}

	h.retryCount = defaultRetryCount
	if v, ok := cfg["retry_count"].(int); ok && v >= 0 {
		h.retryCount = v
	}

	h.retryInterval = defaultRetryInterval
	if v, ok := cfg["retry_interval_ms"].(int); ok && v > 0 {
		h.retryInterval = time.Duration(v) * time.Millisecond
	}

	h.client = &http.Client{Timeout: h.timeout}
	return nil
}

// Write sends metrics to the configured HTTP endpoint as JSON.
func (h *HTTPOutput) Write(metrics []collector.Metric) error {
	for i := 0; i < len(metrics); i += h.batchSize {
		end := i + h.batchSize
		if end > len(metrics) {
			end = len(metrics)
		}
		batch := metrics[i:end]

		if err := h.sendBatch(batch); err != nil {
			return fmt.Errorf("http output: failed to send batch: %w", err)
		}
	}
	return nil
}

func (h *HTTPOutput) sendBatch(metrics []collector.Metric) error {
	metricsJSON := make([]metricJSON, len(metrics))
	for i, m := range metrics {
		metricsJSON[i] = metricJSON{
			Name:      m.Name(),
			Tags:      m.Tags(),
			Fields:    m.Fields(),
			Timestamp: m.Timestamp().UnixMilli(),
		}
	}

	p := payload{
		Metrics: metricsJSON,
		Count:   len(metricsJSON),
	}

	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("http output: failed to marshal payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= h.retryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryInterval)
		}

		lastErr = h.doRequest(body)
		if lastErr == nil {
			return nil
		}
	}

	return lastErr
}

func (h *HTTPOutput) doRequest(body []byte) error {
	req, err := http.NewRequest(http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("http output: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("http output: request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 500 {
		return fmt.Errorf("http output: server error: status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http output: client error: status %d", resp.StatusCode)
	}

	return nil
}

// Close is a no-op for the HTTP output.
func (h *HTTPOutput) Close() error {
	return nil
}

// SampleConfig returns a sample configuration for the HTTP output.
func (h *HTTPOutput) SampleConfig() string {
	return `
  [outputs.http]
    url = "http://localhost:8080/metrics"
    timeout = 10
    batch_size = 1000
    retry_count = 3
    retry_interval_ms = 500
`
}
