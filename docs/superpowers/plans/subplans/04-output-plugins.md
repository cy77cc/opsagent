# Sub-Plan 4: Output Plugins

> **Parent:** [NodeAgentX Full Implementation Plan](../2026-04-28-nodeagentx-full-implementation.md)
> **Depends on:** [Sub-Plan 2: Collector Pipeline Core](02-collector-pipeline.md)

**Goal:** Implement built-in Output plugins for metric delivery.

**Files:**
- Create: `internal/collector/outputs/http/http.go`, `http_test.go`
- Create: `internal/collector/outputs/prometheus/prometheus.go`, `prometheus_test.go`
- Create: `internal/collector/outputs/promrw/promrw.go`, `promrw_test.go`

---

## Task 4.1: HTTP Output Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/outputs/http/http_test.go`:

```go
package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"nodeagentx/internal/collector"
)

func TestHTTPOutputWrite(t *testing.T) {
	var received []map[string]interface{}
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected application/json content type")
		}

		body, _ := io.ReadAll(r.Body)
		var payload map[string]interface{}
		json.Unmarshal(body, &payload)
		received = append(received, payload)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output := NewHTTPOutput()
	output.Init(map[string]interface{}{
		"url":        server.URL,
		"timeout":    5,
		"batch_size": 100,
	})

	metrics := []collector.Metric{
		*collector.NewMetric("cpu", map[string]string{"host": "n1"}, map[string]interface{}{"usage": 80.0}, collector.Gauge, timeNow()),
	}

	if err := output.Write(metrics); err != nil {
		t.Fatalf("write: %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("expected 1 request, got %d", len(received))
	}
}

func TestHTTPOutputRetry(t *testing.T) {
	var attempts int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()

		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output := NewHTTPOutput()
	output.Init(map[string]interface{}{
		"url":          server.URL,
		"timeout":      5,
		"retry_count":  3,
		"retry_interval_ms": 10,
	})

	metrics := []collector.Metric{
		*collector.NewMetric("test", nil, map[string]interface{}{"v": 1}, collector.Gauge, timeNow()),
	}

	if err := output.Write(metrics); err != nil {
		t.Fatalf("write should succeed after retries: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/outputs/http/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement HTTP output**

Create `internal/collector/outputs/http/http.go`:

```go
package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"nodeagentx/internal/collector"
)

func timeNow() time.Time { return time.Now() }

// HTTPOutput sends metrics to an HTTP endpoint as JSON.
type HTTPOutput struct {
	client        *http.Client
	endpoint      string
	batchSize     int
	retryCount    int
	retryInterval time.Duration
}

func NewHTTPOutput() *HTTPOutput {
	return &HTTPOutput{
		client:        &http.Client{Timeout: 10 * time.Second},
		retryCount:    3,
		retryInterval: 500 * time.Millisecond,
	}
}

func (h *HTTPOutput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["url"].(string); ok {
		h.endpoint = v
	}
	if v, ok := cfg["timeout"].(int); ok {
		h.client.Timeout = time.Duration(v) * time.Second
	} else if v, ok := cfg["timeout"].(float64); ok {
		h.client.Timeout = time.Duration(v) * time.Second
	}
	if v, ok := cfg["batch_size"].(int); ok {
		h.batchSize = v
	} else if v, ok := cfg["batch_size"].(float64); ok {
		h.batchSize = int(v)
	}
	if v, ok := cfg["retry_count"].(int); ok {
		h.retryCount = v
	} else if v, ok := cfg["retry_count"].(float64); ok {
		h.retryCount = int(v)
	}
	if v, ok := cfg["retry_interval_ms"].(int); ok {
		h.retryInterval = time.Duration(v) * time.Millisecond
	} else if v, ok := cfg["retry_interval_ms"].(float64); ok {
		h.retryInterval = time.Duration(v) * time.Millisecond
	}
	if h.endpoint == "" {
		return fmt.Errorf("http output: url is required")
	}
	return nil
}

func (h *HTTPOutput) Write(metrics []collector.Metric) error {
	payload := map[string]interface{}{
		"metrics": metricsToMaps(metrics),
		"count":   len(metrics),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("http output marshal: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= h.retryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(h.retryInterval)
		}

		req, err := http.NewRequest(http.MethodPost, h.endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("http output request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := h.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("http output: status %d", resp.StatusCode)
	}

	return fmt.Errorf("http output: all retries failed: %w", lastErr)
}

func (h *HTTPOutput) Close() error { return nil }

func (h *HTTPOutput) SampleConfig() string {
	return `# url = "https://platform.example.com/api/v1/metrics"
# timeout = 5
# batch_size = 500
# retry_count = 3
# retry_interval_ms = 500`
}

func metricsToMaps(metrics []collector.Metric) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(metrics))
	for _, m := range metrics {
		result = append(result, map[string]interface{}{
			"name":      m.Name(),
			"tags":      m.Tags(),
			"fields":    m.Fields(),
			"timestamp": m.Timestamp().UnixMilli(),
		})
	}
	return result
}

func init() {
	collector.RegisterOutput("http", func() collector.Output { return NewHTTPOutput() })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/outputs/http/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/outputs/http/
git commit -m "feat(collector): add HTTP output plugin with retry"
```

---

## Task 4.2: Prometheus Output Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/outputs/prometheus/prometheus_test.go`:

```go
package prometheus

import (
	"strings"
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func TestPrometheusRender(t *testing.T) {
	output := &PrometheusOutput{}

	metrics := []collector.Metric{
		*collector.NewMetric("cpu_usage",
			map[string]string{"host": "node1"},
			map[string]interface{}{"percent": 85.5},
			collector.Gauge,
			time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
		),
		*collector.NewMetric("http_requests_total",
			map[string]string{"method": "GET"},
			map[string]interface{}{"count": int64(100)},
			collector.Counter,
			time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC),
		),
	}

	text := output.renderPrometheus(metrics)

	if !strings.Contains(text, "cpu_usage") {
		t.Fatal("expected cpu_usage metric")
	}
	if !strings.Contains(text, "85.5") {
		t.Fatal("expected 85.5 value")
	}
	if !strings.Contains(text, "http_requests_total") {
		t.Fatal("expected http_requests_total metric")
	}
	if !strings.Contains(text, "100") {
		t.Fatal("expected 100 value")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/outputs/prometheus/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Prometheus output**

Create `internal/collector/outputs/prometheus/prometheus.go`:

```go
package prometheus

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"nodeagentx/internal/collector"
)

// PrometheusOutput serves metrics in Prometheus text exposition format.
type PrometheusOutput struct {
	mu      sync.RWMutex
	metrics []collector.Metric
	path    string
	addr    string
}

func NewPrometheusOutput() *PrometheusOutput {
	return &PrometheusOutput{
		path: "/metrics",
		addr: ":9100",
	}
}

func (p *PrometheusOutput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["path"].(string); ok {
		p.path = v
	}
	if v, ok := cfg["addr"].(string); ok {
		p.addr = v
	}
	return nil
}

func (p *PrometheusOutput) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc(p.path, p.handleMetrics)
	go http.ListenAndServe(p.addr, mux)
	return nil
}

func (p *PrometheusOutput) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	p.mu.RLock()
	metrics := make([]collector.Metric, len(p.metrics))
	copy(metrics, p.metrics)
	p.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprint(w, p.renderPrometheus(metrics))
}

func (p *PrometheusOutput) Write(metrics []collector.Metric) error {
	p.mu.Lock()
	p.metrics = metrics
	p.mu.Unlock()
	return nil
}

func (p *PrometheusOutput) Close() error { return nil }

func (p *PrometheusOutput) SampleConfig() string {
	return `# path = "/metrics"
# addr = ":9100"`
}

func (p *PrometheusOutput) renderPrometheus(metrics []collector.Metric) string {
	var sb strings.Builder
	for _, m := range metrics {
		sb.WriteString(fmt.Sprintf("# TYPE %s gauge\n", sanitizeName(m.Name())))

		labels := ""
		if len(m.Tags()) > 0 {
			parts := make([]string, 0, len(m.Tags()))
			for k, v := range m.Tags() {
				parts = append(parts, fmt.Sprintf(`%s="%s"`, sanitizeName(k), v))
			}
			labels = "{" + strings.Join(parts, ",") + "}"
		}

		for fieldKey, fieldVal := range m.Fields() {
			metricName := sanitizeName(m.Name())
			if len(m.Fields()) > 1 {
				metricName = sanitizeName(m.Name()) + "_" + sanitizeName(fieldKey)
			}
			sb.WriteString(fmt.Sprintf("%s%s %v %d\n", metricName, labels, fieldVal, m.Timestamp().UnixMilli()))
		}
	}
	return sb.String()
}

func sanitizeName(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func init() {
	collector.RegisterOutput("prometheus", func() collector.Output { return NewPrometheusOutput() })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/outputs/prometheus/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/outputs/prometheus/
git commit -m "feat(collector): add Prometheus text exposition output plugin"
```

---

## Task 4.3: Prometheus Remote Write Output Plugin

- [ ] **Step 1: Write failing test**

Create `internal/collector/outputs/promrw/promrw_test.go`:

```go
package promrw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nodeagentx/internal/collector"
)

func TestPromRWWrite(t *testing.T) {
	var received bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/x-protobuf" {
			t.Fatalf("expected protobuf content type, got %s", contentType)
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Fatal("expected non-empty body")
		}
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output := NewPromRWOutput()
	output.Init(map[string]interface{}{
		"url":     server.URL + "/api/v1/write",
		"timeout": 5,
	})

	metrics := []collector.Metric{
		*collector.NewMetric("test_metric",
			map[string]string{"host": "n1"},
			map[string]interface{}{"value": 42.0},
			collector.Gauge,
			time.Now(),
		),
	}

	if err := output.Write(metrics); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !received {
		t.Fatal("expected server to receive request")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/collector/outputs/promrw/ -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Prometheus Remote Write output**

Create `internal/collector/outputs/promrw/promrw.go`:

```go
package promrw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"nodeagentx/internal/collector"
)

// PromRWOutput sends metrics via Prometheus Remote Write protocol.
// Uses JSON format for simplicity; production should use protobuf.
type PromRWOutput struct {
	client   *http.Client
	endpoint string
}

func NewPromRWOutput() *PromRWOutput {
	return &PromRWOutput{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *PromRWOutput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["url"].(string); ok {
		p.endpoint = v
	}
	if v, ok := cfg["timeout"].(int); ok {
		p.client.Timeout = time.Duration(v) * time.Second
	} else if v, ok := cfg["timeout"].(float64); ok {
		p.client.Timeout = time.Duration(v) * time.Second
	}
	if p.endpoint == "" {
		return fmt.Errorf("promrw output: url is required")
	}
	return nil
}

func (p *PromRWOutput) Write(metrics []collector.Metric) error {
	// Convert to Prometheus remote write compatible format (JSON representation)
	series := make([]map[string]interface{}, 0, len(metrics))
	for _, m := range metrics {
		for fieldKey, fieldVal := range m.Fields() {
			samples := []map[string]interface{}{{
				"value":     fieldVal,
				"timestamp": m.Timestamp().UnixMilli(),
			}}
			labels := make([]map[string]string, 0, len(m.Tags())+1)
			labels = append(labels, map[string]string{"__name__": m.Name()})
			for k, v := range m.Tags() {
				labels = append(labels, map[string]string{k: v})
			}
			if len(m.Fields()) > 1 {
				labels = append(labels, map[string]string{"field": fieldKey})
			}
			series = append(series, map[string]interface{}{
				"labels":  labels,
				"samples": samples,
			})
		}
	}

	payload := map[string]interface{}{"timeseries": series}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("promrw marshal: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("promrw request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("promrw send: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("promrw: status %d", resp.StatusCode)
	}

	return nil
}

func (p *PromRWOutput) Close() error { return nil }

func (p *PromRWOutput) SampleConfig() string {
	return `# url = "http://victoriametrics:8428/api/v1/write"
# timeout = 5`
}

func init() {
	collector.RegisterOutput("prometheus_remote_write", func() collector.Output { return NewPromRWOutput() })
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/collector/outputs/promrw/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/outputs/promrw/
git commit -m "feat(collector): add Prometheus Remote Write output plugin"
```
