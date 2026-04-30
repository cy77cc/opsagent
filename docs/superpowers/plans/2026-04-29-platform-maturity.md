# Platform Maturity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Elevate OpsAgent from "feature complete" to "production ready" with self-metrics, audit logging, health checks, CLI tooling, and comprehensive testing.

**Architecture:** Replace the hand-rolled Prometheus text renderer with `prometheus/client_golang` on a custom Registry. Add structured audit logging via dependency injection. Extend subsystem interfaces with `HealthStatus()` for rich health checks. Add CLI subcommands for validation, dry-run, and plugin listing.

**Tech Stack:** Go, prometheus/client_golang, lumberjack (log rotation), cobra (CLI), testify (E2E assertions), golangci-lint, cargo clippy

---

### Task 1: Add Dependencies

**Files:**
- Modify: `go.mod`
- Modify: `Makefile`

- [ ] **Step 1: Add prometheus/client_golang**

```bash
cd /root/project/opsagent
go get github.com/prometheus/client_golang@latest
```

- [ ] **Step 2: Add lumberjack for log rotation**

```bash
go get gopkg.in/natefinish/lumberjack.v2@latest
```

- [ ] **Step 3: Add testify for E2E assertions**

```bash
go get github.com/stretchr/testify@latest
```

- [ ] **Step 4: Add prometheus common for expfmt**

```bash
go get github.com/prometheus/common@latest
```

- [ ] **Step 5: Tidy and verify**

```bash
go mod tidy
go build ./...
```

Expected: Build succeeds with no errors.

- [ ] **Step 6: Update Makefile ldflags and add targets**

Edit `Makefile` to update the `build` target with version ldflags and add `bench` and `e2e` targets:

```makefile
build:
	go build -ldflags="-s -w \
		-X github.com/cy77cc/opsagent/internal/app.Version=$(VERSION) \
		-X github.com/cy77cc/opsagent/internal/app.GitCommit=$(shell git rev-parse --short HEAD) \
		-X github.com/cy77cc/opsagent/internal/app.BuildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bin/$(APP_NAME) ./cmd/agent

bench:
	go test -bench=. -benchmem -count=3 ./internal/collector/

e2e:
	go test -tags=e2e -v -race -count=1 -timeout 120s ./internal/integration/
```

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum Makefile
git commit -m "chore: add prometheus/client_golang, lumberjack, testify deps and update Makefile"
```

---

### Task 2: Prometheus Metrics with client_golang

**Files:**
- Create: `internal/app/metrics.go`
- Create: `internal/app/metrics_test.go`
- Modify: `internal/server/prometheus.go`
- Modify: `internal/server/prometheus_test.go`
- Modify: `internal/server/server.go`

- [ ] **Step 1: Write metrics registration test**

Create `internal/app/metrics_test.go`:

```go
package app

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewMetricsRegistry(t *testing.T) {
	reg := NewMetricsRegistry()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	// Verify all metrics are registered by gathering.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	names := make(map[string]bool)
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}

	expected := []string{
		"opsagent_uptime_seconds",
		"opsagent_grpc_connected",
		"opsagent_tasks_running",
		"opsagent_tasks_completed_total",
		"opsagent_tasks_failed_total",
		"opsagent_metrics_collected_total",
		"opsagent_pipeline_errors_total",
		"opsagent_plugin_requests_total",
		"opsagent_grpc_reconnects_total",
		"opsagent_cpu_usage_percent",
		"opsagent_memory_usage_percent",
		"opsagent_disk_usage_percent",
		"opsagent_load1",
		"opsagent_load5",
		"opsagent_load15",
		"opsagent_network_bytes_sent_total",
		"opsagent_network_bytes_recv_total",
	}

	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected metric %q to be registered", name)
		}
	}
}

func TestMetricsUpdateSystemGauges(t *testing.T) {
	reg := NewMetricsRegistry()

	// Update system metrics.
	reg.UpdateSystemMetrics(45.5, 72.3, 80.1, 1.5, 2.0, 3.0)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "opsagent_cpu_usage_percent":
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 45.5 {
				t.Errorf("cpu_usage = %f, want 45.5", val)
			}
		case "opsagent_memory_usage_percent":
			val := mf.GetMetric()[0].GetGauge().GetValue()
			if val != 72.3 {
				t.Errorf("memory_usage = %f, want 72.3", val)
			}
		}
	}
}

func TestMetricsCounters(t *testing.T) {
	reg := NewMetricsRegistry()

	// Increment counters.
	reg.IncTasksCompleted()
	reg.IncTasksCompleted()
	reg.IncTasksFailed("exec_command", "timeout")
	reg.IncMetricsCollected()
	reg.IncGRPCReconnects()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		switch mf.GetName() {
		case "opsagent_tasks_completed_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 2 {
				t.Errorf("tasks_completed = %f, want 2", val)
			}
		case "opsagent_tasks_failed_total":
			if len(mf.GetMetric()) != 1 {
				t.Errorf("expected 1 label combination for tasks_failed, got %d", len(mf.GetMetric()))
			}
		case "opsagent_metrics_collected_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 1 {
				t.Errorf("metrics_collected = %f, want 1", val)
			}
		case "opsagent_grpc_reconnects_total":
			val := mf.GetMetric()[0].GetCounter().GetValue()
			if val != 1 {
				t.Errorf("grpc_reconnects = %f, want 1", val)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/app/ -run TestNewMetricsRegistry -v
```

Expected: FAIL — `NewMetricsRegistry` undefined.

- [ ] **Step 3: Implement MetricsRegistry**

Create `internal/app/metrics.go`:

```go
package app

import "github.com/prometheus/client_golang/prometheus"

// MetricsRegistry holds all agent Prometheus metrics on an isolated registry.
type MetricsRegistry struct {
	registry *prometheus.Registry

	// Agent self-metrics
	Uptime          prometheus.Gauge
	GRPCConnected   prometheus.Gauge
	TasksRunning    prometheus.Gauge
	TasksCompleted  prometheus.Counter
	TasksFailed     *prometheus.CounterVec
	MetricsCollected prometheus.Counter
	PipelineErrors  *prometheus.CounterVec
	PluginRequests  *prometheus.CounterVec
	GRPCReconnects  prometheus.Counter

	// System metrics
	CPUUsage    prometheus.Gauge
	MemoryUsage prometheus.Gauge
	DiskUsage   prometheus.Gauge
	Load1       prometheus.Gauge
	Load5       prometheus.Gauge
	Load15      prometheus.Gauge
	NetSent     prometheus.Counter
	NetRecv     prometheus.Counter
}

// NewMetricsRegistry creates a MetricsRegistry with all metrics registered.
func NewMetricsRegistry() *MetricsRegistry {
	reg := prometheus.NewRegistry()

	m := &MetricsRegistry{
		registry: reg,
		Uptime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_uptime_seconds",
			Help: "Agent uptime in seconds",
		}),
		GRPCConnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_grpc_connected",
			Help: "Whether gRPC connection is active (1=connected, 0=disconnected)",
		}),
		TasksRunning: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_tasks_running",
			Help: "Number of currently running tasks",
		}),
		TasksCompleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "opsagent_tasks_completed_total",
			Help: "Total completed tasks",
		}),
		TasksFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "opsagent_tasks_failed_total",
			Help: "Total failed tasks",
		}, []string{"task_type", "error_code"}),
		MetricsCollected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "opsagent_metrics_collected_total",
			Help: "Total metrics collected by pipeline",
		}),
		PipelineErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "opsagent_pipeline_errors_total",
			Help: "Total pipeline processing errors",
		}, []string{"stage", "plugin"}),
		PluginRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "opsagent_plugin_requests_total",
			Help: "Total plugin runtime requests",
		}, []string{"plugin", "task_type", "status"}),
		GRPCReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "opsagent_grpc_reconnects_total",
			Help: "Total gRPC reconnection attempts",
		}),
		CPUUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_cpu_usage_percent",
			Help: "CPU usage percent",
		}),
		MemoryUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_memory_usage_percent",
			Help: "Memory usage percent",
		}),
		DiskUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_disk_usage_percent",
			Help: "Disk usage percent",
		}),
		Load1: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_load1",
			Help: "Host load average over 1 minute",
		}),
		Load5: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_load5",
			Help: "Host load average over 5 minutes",
		}),
		Load15: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "opsagent_load15",
			Help: "Host load average over 15 minutes",
		}),
		NetSent: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "opsagent_network_bytes_sent_total",
			Help: "Total bytes sent",
		}),
		NetRecv: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "opsagent_network_bytes_recv_total",
			Help: "Total bytes received",
		}),
	}

	reg.MustRegister(
		m.Uptime, m.GRPCConnected, m.TasksRunning,
		m.TasksCompleted, m.TasksFailed, m.MetricsCollected,
		m.PipelineErrors, m.PluginRequests, m.GRPCReconnects,
		m.CPUUsage, m.MemoryUsage, m.DiskUsage,
		m.Load1, m.Load5, m.Load15,
		m.NetSent, m.NetRecv,
	)

	return m
}

// Registry returns the prometheus.Registry for use by HTTP handlers.
func (m *MetricsRegistry) Registry() *prometheus.Registry {
	return m.registry
}

// UpdateSystemMetrics sets the system metric gauges from a collector snapshot.
func (m *MetricsRegistry) UpdateSystemMetrics(cpu, mem, disk, load1, load5, load15 float64) {
	m.CPUUsage.Set(cpu)
	m.MemoryUsage.Set(mem)
	m.DiskUsage.Set(disk)
	m.Load1.Set(load1)
	m.Load5.Set(load5)
	m.Load15.Set(load15)
}

// IncTasksCompleted increments the tasks completed counter.
func (m *MetricsRegistry) IncTasksCompleted() {
	m.TasksCompleted.Inc()
}

// IncTasksFailed increments the tasks failed counter with labels.
func (m *MetricsRegistry) IncTasksFailed(taskType, errorCode string) {
	m.TasksFailed.WithLabelValues(taskType, errorCode).Inc()
}

// IncMetricsCollected increments the metrics collected counter.
func (m *MetricsRegistry) IncMetricsCollected() {
	m.MetricsCollected.Inc()
}

// IncGRPCReconnects increments the gRPC reconnects counter.
func (m *MetricsRegistry) IncGRPCReconnects() {
	m.GRPCReconnects.Inc()
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race ./internal/app/ -run TestMetrics -v
```

Expected: PASS.

- [ ] **Step 5: Write server prometheus handler test**

Add to `internal/server/prometheus_test.go`:

```go
func TestHandlePrometheusMetrics_WithRegistry(t *testing.T) {
	reg := prometheus.NewRegistry()
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "test_metric",
		Help: "A test metric",
	})
	reg.MustRegister(gauge)
	gauge.Set(42)

	s := &Server{
		logger:       zerolog.Nop(),
		promRegistry: reg,
		options:      Options{Prometheus: PrometheusConfig{Enabled: true}},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	s.handlePrometheusMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "test_metric 42") {
		t.Errorf("expected test_metric 42 in output, got:\n%s", body)
	}
	if !strings.Contains(body, "# HELP test_metric A test metric") {
		t.Errorf("expected HELP text in output")
	}
}
```

- [ ] **Step 6: Run server test to verify it fails**

```bash
go test -race ./internal/server/ -run TestHandlePrometheusMetrics_WithRegistry -v
```

Expected: FAIL — `promRegistry` field doesn't exist.

- [ ] **Step 7: Update Server struct and rewrite prometheus handler**

Add `promRegistry` to `internal/server/server.go`:

```go
// Add to Server struct:
promRegistry *prometheus.Registry
```

Add to `Options`:

```go
type Options struct {
	Auth        AuthConfig
	Prometheus  PrometheusConfig
	PromRegistry *prometheus.Registry
}
```

In `New()`, after creating the server, set:

```go
s.promRegistry = options.PromRegistry
```

Rewrite `internal/server/prometheus.go`:

```go
package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	if s.promRegistry == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	gathered, err := s.promRegistry.Gather()
	if err != nil {
		s.logger.Error().Err(err).Msg("failed to gather prometheus metrics")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", string(expfmt.FmtText))
	for _, mf := range gathered {
		if _, err := expfmt.MetricFamilyToText(w, mf); err != nil {
			s.logger.Error().Err(err).Str("metric", mf.GetName()).Msg("failed to write metric")
			return
		}
	}
}
```

- [ ] **Step 8: Run server tests to verify all pass**

```bash
go test -race ./internal/server/ -v
```

Expected: PASS (all existing tests + new test).

- [ ] **Step 9: Commit**

```bash
git add internal/app/metrics.go internal/app/metrics_test.go internal/server/prometheus.go internal/server/prometheus_test.go internal/server/server.go
git commit -m "feat(metrics): replace hand-rolled prometheus with client_golang registry"
```

---

### Task 3: Health Check Interface Extensions

**Files:**
- Modify: `internal/app/interfaces.go`
- Modify: `internal/grpcclient/client.go`
- Modify: `internal/collector/scheduler.go`
- Modify: `internal/pluginruntime/runtime.go`
- Modify: `internal/pluginruntime/gateway.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/handlers_test.go`

- [ ] **Step 1: Write SubsystemStatus and HealthStatuser test**

Add to `internal/app/interfaces_test.go` (create file):

```go
package app

import (
	"testing"
)

func TestSubsystemStatus(t *testing.T) {
	s := SubsystemStatus{
		Status: "running",
		Details: map[string]any{
			"inputs_active": 5,
		},
	}
	if s.Status != "running" {
		t.Errorf("expected running, got %s", s.Status)
	}
	if s.Details["inputs_active"] != 5 {
		t.Errorf("expected 5 inputs_active")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/app/ -run TestSubsystemStatus -v
```

Expected: FAIL — `SubsystemStatus` undefined.

- [ ] **Step 3: Define SubsystemStatus and extend interfaces**

Edit `internal/app/interfaces.go`:

```go
// SubsystemStatus describes the health state of a subsystem.
type SubsystemStatus struct {
	Status  string         `json:"status"` // running, connected, stopped, error
	Details map[string]any `json:"details,omitempty"`
}

// HealthStatuser is implemented by subsystems that can report health.
type HealthStatuser interface {
	HealthStatus() SubsystemStatus
}
```

Add `HealthStatus() SubsystemStatus` to `GRPCClient`, `Scheduler`, `PluginRuntime`, and `PluginGateway` interfaces.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race ./internal/app/ -run TestSubsystemStatus -v
```

Expected: PASS.

- [ ] **Step 5: Implement HealthStatus on grpcclient.Client**

Add to `internal/grpcclient/client.go`:

```go
import "github.com/cy77cc/opsagent/internal/app"

// HealthStatus returns the gRPC client's connection health.
func (c *Client) HealthStatus() app.SubsystemStatus {
	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()

	status := "disconnected"
	if connected {
		status = "connected"
	}
	return app.SubsystemStatus{
		Status:  status,
		Details: map[string]any{},
	}
}
```

Wait — this creates a circular import (grpcclient imports app). Need to avoid that.

- [ ] **Step 5 (revised): Move SubsystemStatus to a shared package**

Create `internal/health/health.go`:

```go
package health

// Status describes the health state of a subsystem.
type Status struct {
	Status  string         `json:"status"` // running, connected, stopped, error
	Details map[string]any `json:"details,omitempty"`
}

// Statuser is implemented by subsystems that can report health.
type Statuser interface {
	HealthStatus() Status
}
```

Update `internal/app/interfaces.go` to import and use `health.Status`:

```go
import "github.com/cy77cc/opsagent/internal/health"

type HealthStatuser = health.Statuser

// Add HealthStatus() to each interface using health.Status:
type GRPCClient interface {
	// ... existing methods ...
	HealthStatus() health.Status
}

type Scheduler interface {
	// ... existing methods ...
	HealthStatus() health.Status
}

type PluginRuntime interface {
	// ... existing methods ...
	HealthStatus() health.Status
}

type PluginGateway interface {
	// ... existing methods ...
	HealthStatus() health.Status
}
```

- [ ] **Step 6: Implement HealthStatus on all subsystems**

**grpcclient/client.go** — add:

```go
import "github.com/cy77cc/opsagent/internal/health"

func (c *Client) HealthStatus() health.Status {
	c.mu.Lock()
	connected := c.connected
	c.mu.Unlock()
	status := "disconnected"
	if connected {
		status = "connected"
	}
	return health.Status{Status: status}
}
```

**collector/scheduler.go** — add:

```go
import "github.com/cy77cc/opsagent/internal/health"

func (s *Scheduler) HealthStatus() health.Status {
	s.mu.Lock()
	running := s.running
	inputCount := len(s.inputs)
	s.mu.Unlock()
	status := "stopped"
	if running {
		status = "running"
	}
	return health.Status{
		Status: status,
		Details: map[string]any{
			"inputs_active": inputCount,
		},
	}
}
```

**pluginruntime/runtime.go** — add:

```go
import "github.com/cy77cc/opsagent/internal/health"

func (r *Runtime) HealthStatus() health.Status {
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	status := "stopped"
	if started {
		status = "running"
	}
	return health.Status{Status: status}
}
```

**pluginruntime/gateway.go** — add:

```go
import "github.com/cy77cc/opsagent/internal/health"

func (g *Gateway) HealthStatus() health.Status {
	g.mu.Lock()
	started := g.started
	names := make([]string, 0, len(g.plugins))
	for name := range g.plugins {
		names = append(names, name)
	}
	g.mu.Unlock()
	status := "stopped"
	if started {
		status = "running"
	}
	return health.Status{
		Status: status,
		Details: map[string]any{
			"plugins_loaded": names,
		},
	}
}
```

- [ ] **Step 7: Write enhanced healthz handler test**

Add to `internal/server/handlers_test.go`:

```go
func TestHandleHealthz_Enhanced(t *testing.T) {
	s := newTestServer(t)

	// Set version info.
	Version = "1.0.0"
	GitCommit = "abc1234"

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	s.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", resp["version"])
	}
	if resp["status"] == nil {
		t.Error("expected status field")
	}
}
```

- [ ] **Step 8: Implement enhanced healthz handler**

Edit `internal/server/handlers.go` — replace `handleHealthz`:

```go
import "github.com/cy77cc/opsagent/internal/health"

// healthCheckers holds subsystem health providers.
type healthCheckers struct {
	grpc      health.Statuser
	scheduler health.Statuser
	pluginRT  health.Statuser
	gateway   health.Statuser
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	subsystems := make(map[string]any)
	overallStatus := "healthy"

	type entry struct {
		name    string
		checker health.Statuser
		isCore  bool
	}
	entries := []entry{
		{"grpc", s.healthCheckers.grpc, true},
		{"scheduler", s.healthCheckers.scheduler, true},
		{"plugin_runtime", s.healthCheckers.pluginRT, false},
		{"sandbox", nil, false},
	}

	for _, e := range entries {
		if e.checker == nil {
			subsystems[e.name] = map[string]any{"status": "unavailable"}
			if e.isCore {
				overallStatus = "unhealthy"
			}
			continue
		}
		st := e.checker.HealthStatus()
		subsystems[e.name] = st
		if st.Status == "error" || st.Status == "stopped" || st.Status == "disconnected" {
			if e.isCore {
				overallStatus = "unhealthy"
			} else if overallStatus == "healthy" {
				overallStatus = "degraded"
			}
		}
	}

	uptime := time.Since(s.startedAt).Seconds()
	writeJSON(w, http.StatusOK, apiResponse{
		Success: true,
		Data: map[string]any{
			"status":        overallStatus,
			"version":       Version,
			"git_commit":    GitCommit,
			"uptime_seconds": int(uptime),
			"subsystems":    subsystems,
		},
	})
}
```

Add `Version` and `GitCommit` vars and `healthCheckers` field to Server. Update `New()` to accept health checkers in Options.

- [ ] **Step 9: Run all server tests**

```bash
go test -race ./internal/server/ -v
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/health/ internal/app/interfaces.go internal/grpcclient/client.go internal/collector/scheduler.go internal/pluginruntime/runtime.go internal/pluginruntime/gateway.go internal/server/handlers.go internal/server/handlers_test.go internal/server/server.go
git commit -m "feat(health): add HealthStatus to subsystems and enhance /healthz endpoint"
```

---

### Task 4: Audit Logger

**Files:**
- Create: `internal/app/audit.go`
- Create: `internal/app/audit_test.go`

- [ ] **Step 1: Write audit logger test**

Create `internal/app/audit_test.go`:

```go
package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditLogger_Log(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	al, err := NewAuditLogger(path, 10, 3)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer al.Close()

	al.Log(AuditEvent{
		EventType: "task.completed",
		Component: "dispatcher",
		Action:    "exec_command",
		Status:    "success",
		Details:   map[string]interface{}{"task_id": "t-1"},
	})

	al.Log(AuditEvent{
		EventType: "grpc.connected",
		Component: "grpcclient",
		Action:    "connect",
		Status:    "success",
	})

	al.Close()

	// Read and verify.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()

	var events []AuditEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		events = append(events, ev)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "task.completed" {
		t.Errorf("event[0].EventType = %q, want task.completed", events[0].EventType)
	}
	if events[0].Details["task_id"] != "t-1" {
		t.Errorf("event[0].Details[task_id] = %v, want t-1", events[0].Details["task_id"])
	}
	if events[1].EventType != "grpc.connected" {
		t.Errorf("event[1].EventType = %q, want grpc.connected", events[1].EventType)
	}

	// Verify timestamp is set.
	if events[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestAuditLogger_Disabled(t *testing.T) {
	// A nil AuditLogger should not panic.
	var al *AuditLogger
	al.Log(AuditEvent{EventType: "test"})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./internal/app/ -run TestAuditLogger -v
```

Expected: FAIL — `AuditLogger` undefined.

- [ ] **Step 3: Implement AuditLogger**

Create `internal/app/audit.go`:

```go
package app

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"gopkg.in/natefinish/lumberjack.v2"
)

// AuditEvent represents a structured audit log entry.
type AuditEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	EventType string                 `json:"event_type"`
	Component string                 `json:"component"`
	Action    string                 `json:"action"`
	Status    string                 `json:"status"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// AuditLogger writes structured audit events to a JSON-lines file with rotation.
type AuditLogger struct {
	mu     sync.Mutex
	logger *lumberjack.Logger
	enc    *json.Encoder
	file   *os.File
}

// NewAuditLogger creates an AuditLogger writing to path with rotation.
func NewAuditLogger(path string, maxSizeMB, maxBackups int) (*AuditLogger, error) {
	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		Compress:   true,
	}

	// lumberjack implements io.WriteCloser, we can use it directly with json.Encoder.
	return &AuditLogger{
		logger: lj,
	}, nil
}

// Log writes an audit event. Timestamp is set automatically if zero.
// No-op if the receiver is nil (disabled audit).
func (a *AuditLogger) Log(event AuditEvent) {
	if a == nil {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	data = append(data, '\n')
	a.logger.Write(data)
}

// Close flushes and closes the audit log file.
func (a *AuditLogger) Close() error {
	if a == nil {
		return nil
	}
	return a.logger.Close()
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test -race ./internal/app/ -run TestAuditLogger -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/audit.go internal/app/audit_test.go
git commit -m "feat(audit): add structured JSON-lines audit logger with rotation"
```

---

### Task 5: Wire Audit + Metrics into Agent

**Files:**
- Modify: `internal/app/agent.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add AuditLog config to AgentConfig**

Edit `internal/config/config.go` — add to `AgentConfig`:

```go
type AgentConfig struct {
	ID                     string           `mapstructure:"id"`
	Name                   string           `mapstructure:"name"`
	IntervalSeconds        int              `mapstructure:"interval_seconds"`
	ShutdownTimeoutSeconds int              `mapstructure:"shutdown_timeout_seconds"`
	AuditLog               AuditLogConfig   `mapstructure:"audit_log"`
}

type AuditLogConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	Path       string `mapstructure:"path"`
	MaxSizeMB  int    `mapstructure:"max_size_mb"`
	MaxBackups int    `mapstructure:"max_backups"`
}
```

Add defaults in `Load()`:

```go
v.SetDefault("agent.audit_log.enabled", false)
v.SetDefault("agent.audit_log.path", "/var/log/opsagent/audit.jsonl")
v.SetDefault("agent.audit_log.max_size_mb", 100)
v.SetDefault("agent.audit_log.max_backups", 5)
```

- [ ] **Step 2: Add version vars and IsShutdownComplete to agent.go**

Add to `internal/app/agent.go`:

```go
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)
```

Add `shutdownComplete` field to Agent struct and `IsShutdownComplete()` method:

```go
// In Agent struct:
shutdownComplete chan struct{}

// In NewAgent:
a.shutdownComplete = make(chan struct{})

// Method:
func (a *Agent) IsShutdownComplete() bool {
	select {
	case <-a.shutdownComplete:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 3: Wire MetricsRegistry into Agent**

Add `metricsReg *MetricsRegistry` to Agent struct. Create it in `NewAgent`:

```go
a.metricsReg = NewMetricsRegistry()
```

Pass `metricsReg.Registry()` to server Options:

```go
server.Options{
	// ... existing fields ...
	PromRegistry: a.metricsReg.Registry(),
}
```

- [ ] **Step 4: Wire AuditLogger into Agent**

Add `auditLog *AuditLogger` to Agent struct. Create in `NewAgent`:

```go
if cfg.Agent.AuditLog.Enabled {
	a.auditLog, err = NewAuditLogger(
		cfg.Agent.AuditLog.Path,
		cfg.Agent.AuditLog.MaxSizeMB,
		cfg.Agent.AuditLog.MaxBackups,
	)
	if err != nil {
		return nil, fmt.Errorf("create audit logger: %w", err)
	}
}
```

- [ ] **Step 5: Add audit calls to task handlers**

In `registerTaskHandlers`, wrap existing handler closures with audit logging. For example, for `TypeExecCommand`:

```go
dispatcher.Register(task.TypeExecCommand, func(ctx context.Context, t task.AgentTask) (any, error) {
	if a.shuttingDown.Load() {
		a.auditLog.Log(AuditEvent{
			EventType: "task.failed",
			Component: "dispatcher",
			Action:    "exec_command",
			Status:    "failure",
			Details:   map[string]interface{}{"task_id": t.TaskID},
			Error:     "agent is shutting down",
		})
		return nil, fmt.Errorf("agent is shutting down")
	}

	a.auditLog.Log(AuditEvent{
		EventType: "task.started",
		Component: "dispatcher",
		Action:    "exec_command",
		Status:    "success",
		Details:   map[string]interface{}{"task_id": t.TaskID},
	})
	a.metricsReg.TasksRunning.Inc()
	defer a.metricsReg.TasksRunning.Dec()

	// ... existing exec logic ...

	if err != nil {
		a.metricsReg.IncTasksFailed("exec_command", "error")
		a.auditLog.Log(AuditEvent{
			EventType: "task.failed",
			Component: "dispatcher",
			Action:    "exec_command",
			Status:    "failure",
			Details:   map[string]interface{}{"task_id": t.TaskID},
			Error:     err.Error(),
		})
		return nil, err
	}

	a.metricsReg.IncTasksCompleted()
	a.auditLog.Log(AuditEvent{
		EventType: "task.completed",
		Component: "dispatcher",
		Action:    "exec_command",
		Status:    "success",
		Details:   map[string]interface{}{"task_id": t.TaskID},
	})
	return res, nil
})
```

Apply similar audit + metrics wrapping to: `TypeHealthCheck`, plugin task handlers, sandbox exec handler, and gateway task handler.

- [ ] **Step 6: Add audit to agent lifecycle**

In `Run()`, after `startSubsystems` succeeds:

```go
a.auditLog.Log(AuditEvent{
	EventType: "agent.started",
	Component: "agent",
	Action:    "start",
	Status:    "success",
})
```

In `shutdown()`, at the beginning:

```go
a.auditLog.Log(AuditEvent{
	EventType: "agent.shutting_down",
	Component: "agent",
	Action:    "shutdown",
	Status:    "success",
})
```

At the end of `shutdown()`:

```go
close(a.shutdownComplete)
a.auditLog.Close()
a.auditLog.Log(AuditEvent{
	EventType: "agent.stopped",
	Component: "agent",
	Action:    "stop",
	Status:    "success",
})
```

Note: log the "stopped" event before closing the logger. Reorder so Log comes before Close.

- [ ] **Step 7: Add RunOnce for dry-run**

Add to `internal/app/agent.go`:

```go
// RunOnce starts the scheduler, collects one batch of metrics, and shuts down.
// Used for dry-run mode.
func (a *Agent) RunOnce(ctx context.Context) error {
	if a.scheduler == nil {
		return fmt.Errorf("no scheduler configured")
	}
	ch := a.scheduler.Start(ctx)
	select {
	case metrics, ok := <-ch:
		if !ok {
			return fmt.Errorf("pipeline channel closed before collecting metrics")
		}
		a.handlePipelineMetrics(metrics)
		// Print summary.
		totalFields := 0
		for _, m := range metrics {
			totalFields += len(m.Fields())
		}
		fmt.Printf("Collected %d metrics from pipeline\n", len(metrics))
	case <-ctx.Done():
		return ctx.Err()
	}
	a.scheduler.Stop()
	return nil
}
```

- [ ] **Step 8: Build and verify**

```bash
go build ./...
go test -race ./internal/app/ -v
```

Expected: Build and tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/app/agent.go internal/config/config.go
git commit -m "feat(agent): wire metrics, audit, health, version, and RunOnce into Agent"
```

---

### Task 6: CLI Subcommands

**Files:**
- Modify: `cmd/agent/main.go`

- [ ] **Step 1: Write validate command test**

Create `cmd/agent/main_test.go`:

```go
package main

import (
	"testing"

	"github.com/cy77cc/opsagent/internal/config"
)

func TestValidateCommand(t *testing.T) {
	// Test with a valid config file.
	cmd := newValidateCommand()
	cmd.SetArgs([]string{"--config", "../../configs/config.yaml"})

	// Capture output.
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("validate command failed: %v", err)
	}
}

func TestPluginsCommand(t *testing.T) {
	cmd := newPluginsCommand()
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("plugins command failed: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test -race ./cmd/agent/ -run TestValidateCommand -v
```

Expected: FAIL — `newValidateCommand` undefined.

- [ ] **Step 3: Implement validate command**

In `cmd/agent/main.go`, add:

```go
func newValidateCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				fmt.Printf("✗ Config validation failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Config loaded successfully")

			// Try initializing collector pipeline.
			_, err = buildCollectorScheduler(cfg, zerolog.Nop())
			if err != nil {
				fmt.Printf("✗ Collector init failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Collector pipeline initialized")

			fmt.Println("\nResolved config:")
			fmt.Printf("  agent.id: %q\n", cfg.Agent.ID)
			fmt.Printf("  agent.interval_seconds: %d\n", cfg.Agent.IntervalSeconds)
			fmt.Printf("  server.listen_addr: %q\n", cfg.Server.ListenAddr)
			fmt.Printf("  grpc.server_addr: %q\n", cfg.GRPC.ServerAddr)
			fmt.Printf("  plugin.enabled: %v\n", cfg.Plugin.Enabled)
			fmt.Printf("  sandbox.enabled: %v\n", cfg.Sandbox.Enabled)

			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "./configs/config.yaml", "Path to config file")
	return cmd
}
```

Extract `buildCollectorScheduler` from agent.go's `buildScheduler` (or make it a package-level function in app).

- [ ] **Step 4: Implement plugins command**

```go
func newPluginsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "List available plugins",
		RunE: func(_ *cobra.Command, _ []string) error {
			reg := collector.DefaultRegistry

			fmt.Println("Built-in plugins:")
			fmt.Printf("  INPUTS:      %s\n", strings.Join(reg.ListInputs(), ", "))
			fmt.Printf("  PROCESSORS:  %s\n", strings.Join(reg.ListProcessors(), ", "))
			fmt.Printf("  AGGREGATORS: %s\n", strings.Join(reg.ListAggregators(), ", "))
			fmt.Printf("  OUTPUTS:     %s\n", strings.Join(reg.ListOutputs(), ", "))

			return nil
		},
	}
}
```

- [ ] **Step 5: Enhance version command**

Update existing versionCmd:

```go
versionCmd := &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("opsagent %s (commit: %s, built: %s)\n", Version, GitCommit, BuildTime)
	},
}
```

- [ ] **Step 6: Add --dry-run flag to run command**

```go
var dryRun bool
runCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run one collection cycle and exit")

// In RunE, after creating agent:
if dryRun {
	return agent.RunOnce(ctx)
}
```

- [ ] **Step 7: Register subcommands**

```go
rootCmd.AddCommand(versionCmd)
rootCmd.AddCommand(runCmd)
rootCmd.AddCommand(newValidateCommand())
rootCmd.AddCommand(newPluginsCommand())
```

- [ ] **Step 8: Run tests**

```bash
go test -race ./cmd/agent/ -v
go build ./cmd/agent
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/agent/main.go cmd/agent/main_test.go
git commit -m "feat(cli): add validate, plugins subcommands and --dry-run flag"
```

---

### Task 7: CI/CD Enhancement

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add coverage gate to Go job**

Edit `.github/workflows/ci.yml` — replace the Test step:

```yaml
      - name: Test with coverage
        run: |
          go test -race -coverprofile=coverage.out -covermode=atomic ./...
          COVERAGE=$(go tool cover -func=coverage.out | tail -1 | awk '{print $3}' | sed 's/%//')
          echo "Coverage: ${COVERAGE}%"
          if (( $(echo "$COVERAGE < 80" | bc -l) )); then
            echo "::error::Coverage ${COVERAGE}% is below 80% minimum"
            exit 1
          fi
```

- [ ] **Step 2: Add Rust CI job**

Add after the `go` job:

```yaml
  rust:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@stable
        with:
          components: clippy
      - name: Build
        run: cd rust-runtime && cargo build --release
      - name: Test
        run: cd rust-runtime && cargo test
      - name: Clippy
        run: cd rust-runtime && cargo clippy -- -D warnings
      - name: Audit
        run: cd rust-runtime && cargo audit
```

- [ ] **Step 3: Add integration test job**

```yaml
  integration:
    needs: [go]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.1'
      - name: Integration tests
        run: go test ./internal/integration/... -v -race -count=1 -timeout 120s
```

- [ ] **Step 4: Verify YAML syntax**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" 2>/dev/null || echo "install pyyaml to validate"
```

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add 80% coverage gate, Rust CI job, and integration test job"
```

---

### Task 8: E2E Test

**Files:**
- Create: `internal/integration/e2e_test.go`

- [ ] **Step 1: Write E2E test scaffold**

Create `internal/integration/e2e_test.go`:

```go
//go:build e2e

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/cy77cc/opsagent/internal/app"
	"github.com/cy77cc/opsagent/internal/config"
	pb "github.com/cy77cc/opsagent/internal/grpcclient/proto"
)

// mockGRPCServer implements a minimal gRPC server for E2E testing.
type mockGRPCServer struct {
	pb.UnimplementedAgentServiceServer
	grpcServer  *grpc.Server
	addr        string
	registered  chan string
	metricsRecv int
	configUpd   chan *pb.ConfigUpdate
}

func startMockGRPCServer(t *testing.T) *mockGRPCServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	m := &mockGRPCServer{
		grpcServer: grpc.NewServer(),
		addr:       lis.Addr().String(),
		registered: make(chan string, 10),
		configUpd:  make(chan *pb.ConfigUpdate, 10),
	}
	pb.RegisterAgentServiceServer(m.grpcServer, m)

	go func() {
		if err := m.grpcServer.Serve(lis); err != nil {
			// Server stopped.
		}
	}()
	t.Cleanup(func() { m.grpcServer.Stop() })

	return m
}

func (m *mockGRPCServer) Address() string { return m.addr }

func (m *mockGRPCServer) Connect(stream pb.AgentService_ConnectServer) error {
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	if reg := msg.GetRegistration(); reg != nil {
		m.registered <- reg.GetAgentId()
	}

	// Keep stream alive and handle config updates.
	for {
		select {
		case upd := <-m.configUpd:
			if err := stream.Send(&pb.ServerMessage{
				Payload: &pb.ServerMessage_ConfigUpdate{ConfigUpdate: upd},
			}); err != nil {
				return err
			}
		default:
			msg, err := stream.Recv()
			if err != nil {
				return err
			}
			if msg.GetMetricBatch() != nil {
				m.metricsRecv++
			}
		}
	}
}

func (m *mockGRPCServer) HasRegisteredAgent(id string) bool {
	select {
	case agentID := <-m.registered:
		m.registered <- agentID // put back
		return agentID == id
	default:
		return false
	}
}

func (m *mockGRPCServer) MetricsReceived() int { return m.metricsRecv }

func (m *mockGRPCServer) SendConfigUpdate(upd *pb.ConfigUpdate) {
	m.configUpd <- upd
}

func loadTestConfig(t *testing.T, grpcAddr string) *config.Config {
	t.Helper()
	return &config.Config{
		Agent: config.AgentConfig{
			ID:                     "test-agent",
			Name:                   "test",
			IntervalSeconds:        2,
			ShutdownTimeoutSeconds: 10,
		},
		Server: config.ServerConfig{
			ListenAddr: "127.0.0.1:0",
		},
		Executor: config.ExecutorConfig{
			TimeoutSeconds:  10,
			AllowedCommands: []string{"echo", "ls", "cat"},
			MaxOutputBytes:  65536,
		},
		Reporter: config.ReporterConfig{
			Mode:            "stdout",
			TimeoutSeconds:  5,
			RetryCount:      1,
			RetryIntervalMS: 100,
		},
		GRPC: config.GRPCConfig{
			ServerAddr:               grpcAddr,
			HeartbeatIntervalSeconds: 5,
			ReconnectInitialBackoffMS: 500,
			ReconnectMaxBackoffMS:     5000,
		},
		Prometheus: config.PrometheusConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Collector: config.CollectorConfig{
			Inputs: []config.PluginInstanceConfig{
				{Type: "cpu", Config: map[string]interface{}{}},
			},
		},
	}
}

func waitForReady(t *testing.T, listenAddr string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for server ready")
		default:
		}
		resp, err := http.Get(fmt.Sprintf("http://%s/healthz", listenAddr))
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func sendHTTPTask(t *testing.T, listenAddr, taskType string, payload map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"type":    taskType,
		"task_id": fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
		"payload": payload,
	})
	resp, err := http.Post(
		fmt.Sprintf("http://%s/api/v1/tasks", listenAddr),
		"application/json",
		bytes.NewReader(body),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

func TestAgentFullLifecycle(t *testing.T) {
	// 1. Start mock gRPC server
	mockServer := startMockGRPCServer(t)

	// 2. Start agent with test config
	cfg := loadTestConfig(t, mockServer.Address())
	log := zerolog.Nop()
	agent, err := app.NewAgent(cfg, log)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = agent.Run(ctx) }()

	// Wait for HTTP server to be ready.
	// Note: we need the actual listen address. Since we used ":0", we need to
	// get the assigned port. This requires a small refactor to expose it.
	// For now, use a retry loop on a known port or read from agent.
	time.Sleep(2 * time.Second) // Allow startup

	// 3. Verify agent registered with mock server
	assert.Eventually(t, func() bool {
		return mockServer.HasRegisteredAgent("test-agent")
	}, 10*time.Second, 500*time.Millisecond, "agent should register with gRPC server")

	// 4-9 would continue here but require the listen address.
	// The full implementation needs the Server to expose its actual listen address
	// when configured with ":0". This is addressed by adding a ListenAddr() method.

	cancel()
}
```

- [ ] **Step 2: Run E2E test to verify it compiles**

```bash
go test -tags=e2e -c ./internal/integration/ -o /dev/null
```

Expected: Compiles (may fail at runtime without full setup).

- [ ] **Step 3: Add ListenAddr() to server**

In `internal/server/server.go`:

```go
// ListenAddr returns the actual address the server is listening on.
func (s *Server) ListenAddr() string {
	if s.httpServer != nil {
		return s.httpServer.Addr
	}
	return ""
}
```

Update `HTTPServer` interface in `internal/app/interfaces.go`:

```go
type HTTPServer interface {
	Start() error
	Shutdown(ctx context.Context) error
	SetLatestMetric(metric *collector.MetricPayload)
	LatestMetricExists() bool
	ListenAddr() string
}
```

- [ ] **Step 4: Complete E2E test with all 9 steps**

Update the E2E test to use the actual listen address and implement steps 4-9. Add `sandboxAvailable()` helper:

```go
func sandboxAvailable() bool {
	_, err := os.Stat("/usr/bin/nsjail")
	return err == nil
}
```

The full test completes steps: register → exec → sandbox → config update → metrics → shutdown → verify stopped.

- [ ] **Step 5: Run E2E test**

```bash
go test -tags=e2e -v -race -count=1 -timeout 120s ./internal/integration/ -run TestAgentFullLifecycle
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/integration/e2e_test.go internal/server/server.go internal/app/interfaces.go
git commit -m "test(e2e): add full 9-step agent lifecycle E2E test"
```

---

### Task 9: Benchmark Tests

**Files:**
- Create: `internal/collector/benchmark_test.go`

- [ ] **Step 1: Write benchmark tests**

Create `internal/collector/benchmark_test.go`:

```go
package collector

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	// Register inputs for benchmarks.
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/cpu"
	_ "github.com/cy77cc/opsagent/internal/collector/inputs/memory"
	_ "github.com/cy77cc/opsagent/internal/collector/processors/tagger"
)

func BenchmarkMetricCollection(b *testing.B) {
	cpuFactory, ok := DefaultRegistry.GetInput("cpu")
	if !ok {
		b.Fatal("cpu input not registered")
	}
	memFactory, ok := DefaultRegistry.GetInput("memory")
	if !ok {
		b.Fatal("memory input not registered")
	}

	cpu := cpuFactory()
	if err := cpu.Init(nil); err != nil {
		b.Fatalf("cpu init: %v", err)
	}
	mem := memFactory()
	if err := mem.Init(nil); err != nil {
		b.Fatalf("memory init: %v", err)
	}

	scheduled := []ScheduledInput{
		{Input: cpu, Interval: 10 * time.Millisecond},
		{Input: mem, Interval: 10 * time.Millisecond},
	}

	procFactory, _ := DefaultRegistry.GetProcessor("tagger")
	proc := procFactory()
	proc.Init(map[string]interface{}{"tags": map[string]interface{}{"bench": "true"}})

	sched := NewScheduler(scheduled, []Processor{proc}, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := sched.Start(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		<-ch
	}
	b.StopTimer()
	sched.Stop()
}

func BenchmarkPipelineProcessing(b *testing.B) {
	procFactory, ok := DefaultRegistry.GetProcessor("tagger")
	if !ok {
		b.Fatal("tagger not registered")
	}

	proc := procFactory()
	proc.Init(map[string]interface{}{"tags": map[string]interface{}{"bench": "true"}})

	metrics := make([]*Metric, 100)
	for i := range metrics {
		metrics[i] = NewMetric("test_metric", map[string]interface{}{"value": float64(i)}, nil, time.Now())
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		proc.Apply(metrics)
	}
}
```

- [ ] **Step 2: Run benchmarks**

```bash
go test -bench=. -benchmem -count=1 ./internal/collector/
```

Expected: Benchmark results with ns/op, B/op, allocs/op.

- [ ] **Step 3: Commit**

```bash
git add internal/collector/benchmark_test.go
git commit -m "test(bench): add metric collection and pipeline processing benchmarks"
```
