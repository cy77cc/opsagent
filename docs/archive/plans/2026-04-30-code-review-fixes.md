# Code Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all 10 issues identified in the platform maturity code review.

**Architecture:** Wire existing but unused metrics, expand audit event coverage from agent layer, fix version propagation via server config, enhance CLI commands with dependency injection, and add `last_collection` tracking to the scheduler.

**Tech Stack:** Go, Prometheus client_golang, Cobra CLI, lumberjack (audit), gRPC

---

## File Structure

| File | Action | Purpose |
|------|--------|---------|
| `internal/app/metrics.go` | Modify | Add `IncPipelineErrors`, `IncPluginRequests` convenience methods |
| `internal/app/metrics_test.go` | Modify | Add tests for new convenience methods |
| `internal/app/agent.go` | Modify | Wire metrics, audit events, version, commands |
| `internal/app/commands.go` | Modify | Enhance `plugins` and `validate` commands with DI |
| `internal/app/interfaces.go` | Modify | Add `OnStateChange` to `GRPCClient` interface |
| `internal/collector/scheduler.go` | Modify | Add `lastCollection` field and tracking |
| `internal/collector/scheduler_test.go` | Modify | Add test for `last_collection` health detail |
| `internal/grpcclient/client.go` | Modify | Add `OnStateChange` callback |
| `internal/server/server.go` | Modify | Add `Version`, `GitCommit` to `Options` |
| `internal/server/handlers.go` | Modify | Read version from server fields |
| `internal/server/handlers_test.go` | Modify | Update test for version via options |
| `.github/workflows/ci.yml` | Modify | Fix integration job dependency |

---

### Task 1: Add metrics convenience methods and tests

**Files:**
- Modify: `internal/app/metrics.go:131`
- Modify: `internal/app/metrics_test.go:107`

- [ ] **Step 1: Add `IncPipelineErrors` and `IncPluginRequests` methods**

In `internal/app/metrics.go`, add after line 131 (after `IncGRPCReconnects`):

```go
// IncPipelineErrors increments the pipeline errors counter with labels.
func (m *MetricsRegistry) IncPipelineErrors(stage, plugin string) {
	m.PipelineErrors.WithLabelValues(stage, plugin).Inc()
}

// IncPluginRequests increments the plugin requests counter with labels.
func (m *MetricsRegistry) IncPluginRequests(plugin, taskType, status string) {
	m.PluginRequests.WithLabelValues(plugin, taskType, status).Inc()
}
```

- [ ] **Step 2: Add tests for new convenience methods**

In `internal/app/metrics_test.go`, add after `TestMetricsCounters` (after line 107):

```go
func TestMetricsPipelineErrors(t *testing.T) {
	reg := NewMetricsRegistry()
	reg.IncPipelineErrors("output", "prometheus")
	reg.IncPipelineErrors("output", "prometheus")

	mfs, err := reg.Registry().Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() == "opsagent_pipeline_errors_total" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				stage := ""
				plugin := ""
				for _, l := range labels {
					if l.GetName() == "stage" {
						stage = l.GetValue()
					}
					if l.GetName() == "plugin" {
						plugin = l.GetValue()
					}
				}
				if stage == "output" && plugin == "prometheus" {
					val := m.GetCounter().GetValue()
					if val != 2 {
						t.Errorf("pipeline_errors[output,prometheus] = %f, want 2", val)
					}
					return
				}
			}
			t.Error("expected pipeline_errors label combination not found")
		}
	}
}

func TestMetricsPluginRequests(t *testing.T) {
	reg := NewMetricsRegistry()
	reg.IncPluginRequests("my-plugin", "log_parse", "success")
	reg.IncPluginRequests("my-plugin", "log_parse", "error")

	mfs, err := reg.Registry().Gather()
	if err != nil {
		t.Fatalf("gather error: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() == "opsagent_plugin_requests_total" {
			successFound := false
			errorFound := false
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				var plugin, taskType, status string
				for _, l := range labels {
					switch l.GetName() {
					case "plugin":
						plugin = l.GetValue()
					case "task_type":
						taskType = l.GetValue()
					case "status":
						status = l.GetValue()
					}
				}
				if plugin == "my-plugin" && taskType == "log_parse" {
					val := m.GetCounter().GetValue()
					if status == "success" && val == 1 {
						successFound = true
					}
					if status == "error" && val == 1 {
						errorFound = true
					}
				}
			}
			if !successFound {
				t.Error("expected plugin_requests[my-plugin,log_parse,success] = 1")
			}
			if !errorFound {
				t.Error("expected plugin_requests[my-plugin,log_parse,error] = 1")
			}
			return
		}
	}
	t.Error("opsagent_plugin_requests_total not found")
}
```

- [ ] **Step 3: Run tests to verify**

Run: `go test -race ./internal/app/ -run TestMetrics -v`
Expected: All 5 metric tests PASS

- [ ] **Step 4: Commit**

```bash
git add internal/app/metrics.go internal/app/metrics_test.go
git commit -m "feat(metrics): add IncPipelineErrors and IncPluginRequests convenience methods"
```

---

### Task 2: Wire Prometheus counters in agent

**Files:**
- Modify: `internal/app/agent.go:500-508` (handlePipelineMetrics)
- Modify: `internal/app/agent.go:510-805` (registerTaskHandlers)
- Modify: `internal/app/agent.go:933-958` (executePluginTask)

- [ ] **Step 1: Wire `IncMetricsCollected` in `handlePipelineMetrics`**

In `internal/app/agent.go`, modify `handlePipelineMetrics` (lines 500-508):

```go
func (a *Agent) handlePipelineMetrics(metrics []*collector.Metric) {
	if len(metrics) == 0 {
		return
	}
	a.metricsReg.IncMetricsCollected()
	// Send metrics via gRPC client.
	a.grpcClient.SendMetrics(metrics)
	a.log.Debug().Int("count", len(metrics)).Msg("pipeline metrics sent via gRPC")
}
```

- [ ] **Step 2: Wire `IncPluginRequests` in plugin task handlers**

In `internal/app/agent.go`, in the plugin task handler loop (around line 622), add `IncPluginRequests` calls. Find the plugin task handler registration block (lines 620-651) and modify:

After the `executePluginTask` call (line 631), add before the error check:
```go
				res, err := a.executePluginTask(ctx, t, taskType)
```

In the error branch (after line 633), add:
```go
					a.metricsReg.IncPluginRequests(taskType, taskType, "error")
```

In the success branch (after line 643), add:
```go
				a.metricsReg.IncPluginRequests(taskType, taskType, "success")
```

Do the same for the gateway plugin task handler (lines 765-793): add `IncPluginRequests` calls in the error and success branches.

- [ ] **Step 3: Wire `IncPipelineErrors` in `gatherOnce` output write failure**

In `internal/collector/scheduler.go`, in `gatherOnce` (line 285), the output write error is already logged. The agent's `handlePipelineMetrics` is a better place since it has access to `metricsReg`. However, `handlePipelineMetrics` doesn't know about output errors. The output errors happen inside the scheduler.

Since the scheduler doesn't have access to `metricsReg`, the cleanest approach is to skip wiring `IncPipelineErrors` for now — the scheduler logs output errors via zerolog. The `PipelineErrors` counter is available for future use when the scheduler gets a metrics callback. Document this in a comment.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent.go
git commit -m "feat(agent): wire IncMetricsCollected and IncPluginRequests counters"
```

---

### Task 3: Fix `totalFields` dead code in RunOnce

**Files:**
- Modify: `internal/app/agent.go:488-492`

- [ ] **Step 1: Use `totalFields` in output**

In `internal/app/agent.go`, replace lines 492:
```go
		fmt.Printf("Collected %d metrics from pipeline\n", len(metrics))
```

With:
```go
		fmt.Printf("Collected %d metrics (%d total fields) from pipeline\n", len(metrics), totalFields)
```

- [ ] **Step 2: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/app/agent.go
git commit -m "fix(agent): use totalFields in RunOnce output"
```

---

### Task 4: Add `last_collection` to scheduler health

**Files:**
- Modify: `internal/collector/scheduler.go:40-53` (Scheduler struct)
- Modify: `internal/collector/scheduler.go:116-129` (HealthStatus)
- Modify: `internal/collector/scheduler.go:242-295` (gatherOnce)
- Modify: `internal/collector/scheduler_test.go`

- [ ] **Step 1: Add `lastCollection` field to Scheduler struct**

In `internal/collector/scheduler.go`, add a field to the `Scheduler` struct (after line 52):
```go
	outCh          chan []*Metric
	lastCollection time.Time
```

- [ ] **Step 2: Update `lastCollection` in `gatherOnce`**

In `internal/collector/scheduler.go`, in `gatherOnce`, after the metrics are sent to the channel (after line 292), add:
```go
	s.mu.Lock()
	s.lastCollection = time.Now()
	s.mu.Unlock()
```

Place this just before the `select` block that sends to `ch` (line 291), or immediately after the channel send succeeds. The cleanest placement is after the output writes and before the channel send:

After line 289 (end of output write block), add:
```go
	s.mu.Lock()
	s.lastCollection = time.Now()
	s.mu.Unlock()
```

- [ ] **Step 3: Include `last_collection` in HealthStatus**

In `internal/collector/scheduler.go`, modify `HealthStatus` (lines 116-129):

```go
func (s *Scheduler) HealthStatus() health.Status {
	s.mu.RLock()
	running := s.running
	inputCount := len(s.inputs)
	lastColl := s.lastCollection
	s.mu.RUnlock()
	status := "stopped"
	if running {
		status = "running"
	}
	details := map[string]any{"inputs_active": inputCount}
	if !lastColl.IsZero() {
		details["last_collection"] = lastColl.UTC().Format(time.RFC3339)
	}
	return health.Status{
		Status:  status,
		Details: details,
	}
}
```

- [ ] **Step 4: Add test for `last_collection`**

In `internal/collector/scheduler_test.go`, add at the end:

```go
func TestSchedulerHealthStatus_LastCollection(t *testing.T) {
	input := newTestInput("cpu", nil, map[string]interface{}{"v": 1.0})
	si := ScheduledInput{Input: input, Interval: 50 * time.Millisecond}

	sched := NewScheduler([]ScheduledInput{si}, nil, nil, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Before start, last_collection should be absent.
	hs := sched.HealthStatus()
	if _, ok := hs.Details["last_collection"]; ok {
		t.Error("expected no last_collection before start")
	}

	ch := sched.Start(ctx)

	// Wait for at least one gather.
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for metrics")
	}

	hs = sched.HealthStatus()
	lc, ok := hs.Details["last_collection"]
	if !ok {
		t.Fatal("expected last_collection after gather")
	}
	if lc == "" {
		t.Error("expected non-empty last_collection")
	}

	sched.Stop()
}
```

- [ ] **Step 5: Run tests**

Run: `go test -race ./internal/collector/ -run TestSchedulerHealthStatus_LastCollection -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/collector/scheduler.go internal/collector/scheduler_test.go
git commit -m "feat(scheduler): add last_collection to health status"
```

---

### Task 5: Fix version propagation via server options

**Files:**
- Modify: `internal/server/server.go:44-49` (Options)
- Modify: `internal/server/server.go:52-65` (Server struct)
- Modify: `internal/server/server.go:68-92` (New)
- Modify: `internal/server/handlers.go:33-80` (handleHealthz)
- Modify: `internal/server/handlers_test.go`
- Modify: `internal/app/agent.go:200-227` (server construction)

- [ ] **Step 1: Add version fields to server Options**

In `internal/server/server.go`, add to `Options` struct (after line 48):
```go
type Options struct {
	Auth           AuthConfig
	Prometheus     PrometheusConfig
	PromRegistry   *prometheus.Registry
	HealthCheckers HealthCheckers
	Version        string
	GitCommit      string
}
```

- [ ] **Step 2: Store version in Server struct**

In `internal/server/server.go`, add to `Server` struct (after line 63):
```go
	healthCheckers HealthCheckers
	version        string
	gitCommit      string
```

- [ ] **Step 3: Set version in `New` constructor**

In `internal/server/server.go`, in `New` function, add after line 80:
```go
	s.version = options.Version
	s.gitCommit = options.GitCommit
```

- [ ] **Step 4: Use server fields in health handler**

In `internal/server/handlers.go`, modify `handleHealthz` (lines 73-76). Replace:
```go
			"version":        Version,
			"git_commit":     GitCommit,
```
With:
```go
			"version":        s.version,
			"git_commit":     s.gitCommit,
```

- [ ] **Step 5: Update health test to use options**

In `internal/server/handlers_test.go`, modify `TestHandleHealthz_Enhanced` (lines 50-76). Replace the version setting approach:

```go
func TestHandleHealthz_Enhanced(t *testing.T) {
	log := zerolog.Nop()
	s := New(":0", log, &executor.Executor{}, task.NewDispatcher(), time.Now(), Options{
		Version:   "1.0.0",
		GitCommit: "abc1234",
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data field")
	}
	if data["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", data["version"])
	}
	if data["git_commit"] != "abc1234" {
		t.Errorf("expected git_commit abc1234, got %v", data["git_commit"])
	}
	if data["status"] == nil {
		t.Error("expected status field")
	}
}
```

- [ ] **Step 6: Pass version from agent to server**

In `internal/app/agent.go`, in the server construction block (around line 209), add version fields to `server.Options`:

```go
			server.Options{
				// ... existing fields ...
				PromRegistry: a.metricsReg.Registry(),
				HealthCheckers: server.HealthCheckers{
					GRPC:      a.grpcClient,
					Scheduler: a.scheduler,
					PluginRT:  a.pluginRuntime,
				},
				Version:   Version,
				GitCommit: GitCommit,
			},
```

- [ ] **Step 7: Run tests**

Run: `go test -race ./internal/server/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/server/server.go internal/server/handlers.go internal/server/handlers_test.go internal/app/agent.go
git commit -m "fix(server): propagate version from agent to health endpoint"
```

---

### Task 6: Add gRPC state change callback and wire audit events

**Files:**
- Modify: `internal/grpcclient/client.go` (add OnStateChange callback)
- Modify: `internal/app/interfaces.go` (update GRPCClient interface)
- Modify: `internal/app/agent.go` (wire gRPC audit events)

- [ ] **Step 1: Add OnStateChange to gRPC client**

In `internal/grpcclient/client.go`, add a callback field to `Client` struct (after line 62):
```go
	connected    bool
	onStateChange func(connected bool)
```

Add a setter method after `IsConnected` (after line 178):
```go
// SetOnStateChange registers a callback for connection state changes.
func (c *Client) SetOnStateChange(fn func(connected bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}
```

In `connect()` (line 266), after `c.connected = true`, add:
```go
	c.mu.Unlock()
	if c.onStateChange != nil {
		c.onStateChange(true)
	}
```

Wait — the current code has `c.mu.Unlock()` at line 267. The callback should fire outside the lock to avoid deadlocks. Let me look at the exact code again.

The current `connect` function (lines 263-267):
```go
	c.mu.Lock()
	c.conn = conn
	c.stream = stream
	c.connected = true
	c.mu.Unlock()
```

Add after line 267 (after `c.mu.Unlock()`):
```go
	if c.onStateChange != nil {
		c.onStateChange(true)
	}
```

In `setConnected` (line 430), modify to fire callback:
```go
func (c *Client) setConnected(v bool) {
	c.mu.Lock()
	wasConnected := c.connected
	c.connected = v
	c.mu.Unlock()
	if v != wasConnected && c.onStateChange != nil {
		c.onStateChange(v)
	}
}
```

- [ ] **Step 2: Add `SetOnStateChange` to GRPCClient interface**

In `internal/app/interfaces.go`, add to the `GRPCClient` interface (after line 23):
```go
	SetOnStateChange(fn func(connected bool))
```

- [ ] **Step 3: Wire gRPC audit events in agent**

In `internal/app/agent.go`, in `NewAgent`, after the gRPC client is created (after line 178), add:
```go
		a.grpcClient.SetOnStateChange(func(connected bool) {
			if connected {
				a.auditLog.Log(AuditEvent{
					EventType: "grpc.connected", Component: "grpcclient",
					Action: "connect", Status: "success",
				})
				a.metricsReg.GRPCConnected.Set(1)
			} else {
				a.auditLog.Log(AuditEvent{
					EventType: "grpc.disconnected", Component: "grpcclient",
					Action: "disconnect", Status: "success",
				})
				a.metricsReg.GRPCConnected.Set(0)
			}
		})
```

For `grpc.reconnecting`, the gRPC client's `connectLoop` retries silently. The `setConnected(false)` already triggers the disconnect audit. Each subsequent successful `connect()` call triggers the connected audit. The reconnecting event can be added in the `connectLoop` before each retry attempt:

In `internal/grpcclient/client.go`, in `connectLoop` (around line 210), after the `reconnect failed` log, add:
```go
				if c.onStateChange != nil && backoff > time.Second {
					// Signal reconnecting state (not connected, not first attempt)
				}
```

Actually, this is getting complex. The simpler approach: the disconnect event covers the "lost connection" case, and the connected event covers the "reconnected" case. The `grpc.reconnecting` event would fire on every failed retry which could be noisy. Instead, add it once when the message loop exits (indicating disconnection) and the connect loop starts retrying.

In `connectLoop`, after `c.messageLoop(ctx)` returns (line 222), the client will try to reconnect. The `setConnected(false)` inside `messageLoop` already fires the disconnect audit. Each successful `connect()` fires the connected audit. For `grpc.reconnecting`, add a single event when entering the retry loop:

In `connectLoop`, after the initial connection attempt fails (line 196), add:
```go
	if err := c.connect(ctx); err != nil {
		c.logger.Error().Err(err).Msg("connection failed")
		if c.onStateChange != nil {
			// Fire reconnecting event (first attempt failed)
		}
	}
```

Actually, this is still messy. Let me simplify: skip `grpc.reconnecting` for now. The `grpc.disconnected` + `grpc.connected` pair already provides the audit trail. Document that `grpc.reconnecting` could be added as a future enhancement if needed.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/grpcclient/ -v && go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/grpcclient/client.go internal/app/interfaces.go internal/app/agent.go
git commit -m "feat(audit): add gRPC connected/disconnected audit events"
```

---

### Task 7: Add config audit events

**Files:**
- Modify: `internal/app/agent.go` (config load, reload, rejected events)

- [ ] **Step 1: Add `config.loaded` audit event**

In `internal/app/agent.go`, in `NewAgent`, after the config is set (after line 97), add:
```go
	a.auditLog.Log(AuditEvent{
		EventType: "config.loaded", Component: "agent",
		Action: "load", Status: "success",
		Details: map[string]interface{}{"config_path": "startup"},
	})
```

Note: `a.auditLog` may be nil at this point (it's created later at line 106). Move the audit log creation before this point, or add the event after the audit logger is created.

The audit logger is created at lines 106-116. Add the `config.loaded` event after line 116:
```go
		a.auditLog.Log(AuditEvent{
			EventType: "config.loaded", Component: "agent",
			Action: "load", Status: "success",
		})
```

- [ ] **Step 2: Add `config.reloaded` and `config.rejected` audit events**

In `internal/app/agent.go`, in `registerGRPCHandlers`, in the config update handler (lines 916-930):

After the successful reload (line 925), add:
```go
			a.auditLog.Log(AuditEvent{
				EventType: "config.reloaded", Component: "agent",
				Action: "hot_reload", Status: "success",
				Details: map[string]interface{}{"version": update.GetVersion()},
			})
```

After the failed reload (line 918), add:
```go
			a.auditLog.Log(AuditEvent{
				EventType: "config.rejected", Component: "agent",
				Action: "hot_reload", Status: "failure",
				Error: err.Error(),
			})
```

Also add to the SIGHUP handler in `commands.go` (lines 79-83). The SIGHUP handler doesn't have access to `auditLog`. Since the agent struct has it, we need to move the SIGHUP handler into the agent or pass the audit logger. The simplest approach: add audit logging in the `commands.go` SIGHUP handler by accessing the agent's audit logger.

Actually, the SIGHUP handler is in `commands.go` line 73-87. It calls `agent.ConfigReloader().Apply()`. The `Apply` method is in `config.ConfigReloader`. The agent doesn't have a hook for "config reloaded via SIGHUP" vs "config reloaded via gRPC".

The cleanest approach: add audit logging in the SIGHUP handler directly. The handler has access to `agent` (via closure). Add a method to Agent:

```go
func (a *Agent) AuditLog() *AuditLogger {
	return a.auditLog
}
```

Then in the SIGHUP handler in `commands.go`:
```go
case sig := <-sigCh:
	if sig == syscall.SIGHUP {
		yaml, readErr := os.ReadFile(configPath)
		if readErr != nil {
			log.Error().Err(readErr).Msg("failed to read config file for SIGHUP reload")
			agent.AuditLog().Log(AuditEvent{
				EventType: "config.rejected", Component: "agent",
				Action: "sighup_reload", Status: "failure",
				Error: readErr.Error(),
			})
			continue
		}
		if applyErr := agent.ConfigReloader().Apply(ctx, yaml); applyErr != nil {
			log.Error().Err(applyErr).Msg("SIGHUP config reload failed")
			agent.AuditLog().Log(AuditEvent{
				EventType: "config.rejected", Component: "agent",
				Action: "sighup_reload", Status: "failure",
				Error: applyErr.Error(),
			})
		} else {
			log.Info().Msg("config reloaded via SIGHUP")
			agent.AuditLog().Log(AuditEvent{
				EventType: "config.reloaded", Component: "agent",
				Action: "sighup_reload", Status: "success",
			})
		}
	}
```

Wait, `commands.go` is in `package app` so it can access `AuditEvent` directly. But `agent.auditLog` is unexported. We need to either export it or add a method. Let me add `AuditLog()` method to Agent.

- [ ] **Step 3: Add `AuditLog()` method to Agent**

In `internal/app/agent.go`, add after `ConfigReloader()` (after line 76):
```go
// AuditLog returns the agent's audit logger.
func (a *Agent) AuditLog() *AuditLogger {
	return a.auditLog
}
```

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent.go internal/app/commands.go
git commit -m "feat(audit): add config loaded/reloaded/rejected audit events"
```

---

### Task 8: Add plugin lifecycle audit events

**Files:**
- Modify: `internal/app/agent.go:322-351` (startSubsystems)
- Modify: `internal/app/agent.go:400-450` (shutdown)

- [ ] **Step 1: Add `plugin.started` audit event**

In `internal/app/agent.go`, in `startSubsystems`, after the plugin runtime starts (after line 326):
```go
	if err := a.pluginRuntime.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start plugin runtime: %w", err)
	}
	a.auditLog.Log(AuditEvent{
		EventType: "plugin.started", Component: "pluginruntime",
		Action: "start", Status: "success",
	})
```

- [ ] **Step 2: Add `plugin.stopped` audit event**

In `internal/app/agent.go`, in `shutdown`, after the plugin runtime stops (after line 429):
```go
	if err := a.pluginRuntime.Stop(stopCtx); err != nil {
		a.log.Error().Err(err).Msg("failed to stop plugin runtime")
	}
	a.auditLog.Log(AuditEvent{
		EventType: "plugin.stopped", Component: "pluginruntime",
		Action: "stop", Status: "success",
	})
```

For `plugin.crashed`, the runtime doesn't expose crash detection to the agent. The `exec.Cmd` runs in the background and if it crashes, the `HealthStatus()` would show "stopped" on the next check. Add a `plugin.crashed` event when the health status transitions from "running" to a non-running state. This requires periodic health checking, which is outside the scope of this fix. Document that `plugin.crashed` can be added when the runtime exposes a crash callback.

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/app/agent.go
git commit -m "feat(audit): add plugin started/stopped audit events"
```

---

### Task 9: Add task.cancelled audit event

**Files:**
- Modify: `internal/app/agent.go:903-913` (cancel handler)

- [ ] **Step 1: Add `task.cancelled` audit event**

In `internal/app/agent.go`, in `registerGRPCHandlers`, in the cancel handler (lines 904-913), add audit logging:

```go
	recv.SetCancelHandler(func(_ context.Context, job *pb.CancelJob) error {
		taskID := job.GetTaskId()
		if cancelFn, ok := a.activeTasks.Load(taskID); ok {
			cancelFn.(context.CancelFunc)()
			a.auditLog.Log(AuditEvent{
				EventType: "task.cancelled", Component: "dispatcher",
				Action: "cancel", Status: "success",
				Details: map[string]interface{}{"task_id": taskID},
			})
			a.log.Info().Str("task_id", taskID).Msg("cancel job executed")
		} else {
			a.log.Warn().Str("task_id", taskID).Msg("cancel job: task not found")
		}
		return nil
	})
```

- [ ] **Step 2: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/app/agent.go
git commit -m "feat(audit): add task.cancelled audit event"
```

---

### Task 10: Enhance `plugins` command with custom plugin listing

**Files:**
- Modify: `internal/app/commands.go:102-151` (plugins command)
- Modify: `internal/app/agent.go` (pass subsystems to command)

- [ ] **Step 1: Add `ListPlugins` and `HealthStatus` methods to PluginGateway interface for commands**

The `PluginGateway` interface already has `ListPlugins() []pluginruntime.PluginInfo` and `HealthStatus() health.Status`. No interface changes needed.

- [ ] **Step 2: Modify `newPluginsCommand` signature**

In `internal/app/commands.go`, change `newPluginsCommand` to accept dependencies:

```go
func newPluginsCommand(gateway PluginGateway, pluginRT PluginRuntime) *cobra.Command {
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

			if gateway != nil {
				plugins := gateway.ListPlugins()
				if len(plugins) > 0 {
					fmt.Println("\nCustom plugins:")
					for _, p := range plugins {
						fmt.Printf("  %s %s [%s] tasks: %s\n", p.Name, p.Version, p.Status, strings.Join(p.TaskTypes, ", "))
					}
				}
			}

			if pluginRT != nil {
				hs := pluginRT.HealthStatus()
				fmt.Printf("\nPlugin runtime: %s\n", hs.Status)
			}

			return nil
		},
	}
}
```

- [ ] **Step 3: Update command construction in `NewRootCommand`**

In `internal/app/commands.go`, `NewRootCommand` doesn't have access to gateway/runtime. Change the signature:

```go
func NewRootCommand(gateway PluginGateway, pluginRT PluginRuntime) *cobra.Command {
```

Update the plugins command construction (line 102):
```go
	pluginsCmd := newPluginsCommand(gateway, pluginRT)
```

- [ ] **Step 4: Update agent to pass subsystems**

In `internal/app/agent.go`, the root command is constructed in `commands.go` via `NewRootCommand()`. The agent's `Run` method calls `NewRootCommand` indirectly through `cmd/agent/main.go`. 

Actually, looking at `cmd/agent/main.go`, the root command is created there. Let me check.

Let me look at `cmd/agent/main.go`:
```go
func main() {
    rootCmd := app.NewRootCommand()
    if err := rootCmd.Execute(); err != nil {
        os.Exit(1)
    }
}
```

So `NewRootCommand()` is called from `main.go` without any subsystem access. The agent is only created inside the `run` subcommand handler. The `plugins` command runs without an agent.

This means we can't pass gateway/runtime to `NewRootCommand` because they don't exist until the `run` command creates the agent.

**Alternative approach:** Make `plugins` command lazy — it creates a minimal agent or directly accesses the gateway/runtime via config. But this is over-engineered.

**Simpler approach:** The `plugins` command can check if the plugin gateway is enabled in the config and attempt to connect to the plugin runtime socket to list plugins. But this requires reading the config.

**Simplest approach that works:** The `plugins` command accepts a `--config` flag and, if provided, tries to connect to the plugin runtime to list custom plugins. If not provided, it only lists built-in plugins.

Actually, looking at the spec again, it says the `plugins` command should list custom plugins from `gateway.ListPlugins()`. But the gateway doesn't exist at CLI parse time — it's created inside the `run` command.

The practical solution: modify `NewRootCommand` to accept optional subsystem closures that are lazily evaluated:

```go
type CommandDeps struct {
	Gateway  func() PluginGateway
	PluginRT func() PluginRuntime
}
```

Or simpler: add the `plugins` command inside the `run` command's agent creation, so it's only available when the agent is running. But that changes the CLI structure.

**Best practical approach:** Add a `--config` flag to the `plugins` command. If provided, build the plugin gateway from config and list custom plugins. If not provided, only list built-in plugins.

```go
func newPluginsCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "List available plugins",
		RunE: func(_ *cobra.Command, _ []string) error {
			reg := collector.DefaultRegistry
			fmt.Println("Built-in plugins:")
			fmt.Printf("  INPUTS:      %s\n", strings.Join(reg.ListInputs(), ", "))
			fmt.Printf("  PROCESSORS:  %s\n", strings.Join(reg.ListProcessors(), ", "))
			fmt.Printf("  AGGREGATORS: %s\n", strings.Join(reg.ListAggregators(), ", "))
			fmt.Printf("  OUTPUTS:     %s\n", strings.Join(reg.ListOutputs(), ", "))

			if configPath != "" {
				cfg, err := config.Load(configPath)
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				if cfg.PluginGateway.Enabled {
					gw := pluginruntime.NewGateway(pluginruntime.GatewayConfig{
						PluginsDir:    cfg.PluginGateway.PluginsDir,
						PluginConfigs: cfg.PluginGateway.PluginConfigs,
					}, zerolog.Nop())
					ctx := context.Background()
					if err := gw.Start(ctx); err != nil {
						fmt.Printf("\nWarning: could not start plugin gateway: %v\n", err)
					} else {
						plugins := gw.ListPlugins()
						if len(plugins) > 0 {
							fmt.Println("\nCustom plugins:")
							for _, p := range plugins {
								fmt.Printf("  %s %s [%s] tasks: %s\n", p.Name, p.Version, p.Status, strings.Join(p.TaskTypes, ", "))
							}
						}
						gw.Stop(ctx)
					}
				}
				if cfg.Plugin.Enabled {
					rt := pluginruntime.New(pluginruntime.Config{
						Enabled:     cfg.Plugin.Enabled,
						SocketPath:  cfg.Plugin.SocketPath,
						RuntimePath: cfg.Plugin.RuntimePath,
					}, zerolog.Nop())
					hs := rt.HealthStatus()
					fmt.Printf("\nPlugin runtime: %s\n", hs.Status)
				}
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "Path to config file (enables custom plugin listing)")
	return cmd
}
```

This is a reasonable approach. It uses `--config` to opt-in to custom plugin listing.

- [ ] **Step 5: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/app/commands.go
git commit -m "feat(commands): enhance plugins command with custom plugin listing"
```

---

### Task 11: Enhance `validate` command with pipeline initialization

**Files:**
- Modify: `internal/app/commands.go:110-133` (validate command)

- [ ] **Step 1: Enhance `newValidateCommand` to try building the scheduler**

In `internal/app/commands.go`, modify `newValidateCommand` to also build the scheduler:

```go
func newValidateCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("config validation failed: %w", err)
			}
			fmt.Println("✓ Config loaded successfully")

			// Try building the scheduler to verify all factories resolve.
			sched, err := buildScheduler(cfg, zerolog.Nop())
			if err != nil {
				return fmt.Errorf("pipeline validation failed: %w", err)
			}
			if sched != nil {
				fmt.Println("✓ All inputs initialized")
				fmt.Println("✓ All processors initialized")
				fmt.Println("✓ All aggregators initialized")
				fmt.Println("✓ All outputs initialized")
			} else {
				fmt.Println("⚠ No inputs configured (scheduler disabled)")
			}

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

Note: `buildScheduler` is in `agent.go` (line 252) and is accessible within `package app`.

- [ ] **Step 2: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/app/commands.go
git commit -m "feat(commands): enhance validate command with pipeline initialization check"
```

---

### Task 12: Bridge sandbox audit events to app-level audit

**Files:**
- Modify: `internal/app/agent.go:182-198` (sandbox executor creation)

- [ ] **Step 1: Add audit callback to sandbox executor creation**

In `internal/app/agent.go`, in the sandbox executor creation block (lines 182-198), the sandbox executor has its own `AuditLogPath` config. We need to bridge its events to the app-level audit logger.

The sandbox `AuditLogger` (in `internal/sandbox/audit.go`) is separate from the app-level one. The sandbox logs events with its own format (`sandbox.AuditEvent` with fields like `TaskID`, `Command`, `ExitCode`, etc.).

To bridge: add a callback on the sandbox executor that also writes to the app-level audit logger. Check if the sandbox executor supports callbacks.

Looking at `internal/sandbox/executor.go`, the executor doesn't have a callback mechanism. The audit logging happens internally via `sandbox.AuditLogger.LogExecution()`.

**Practical approach:** Add `sandbox.executed` and `sandbox.blocked` events at the agent level where sandbox execution results are available. The sandbox exec task handler (lines 654-757) already has the execution result. Add audit events there:

In the sandbox exec task handler, after successful execution (after line 735 and 755), add:
```go
				a.auditLog.Log(AuditEvent{
					EventType: "sandbox.executed", Component: "sandbox",
					Action: "execute", Status: "success",
					Details: map[string]interface{}{"task_id": t.TaskID},
				})
```

For `sandbox.blocked`, the sandbox executor's policy enforcement happens inside the sandbox package. The agent doesn't see blocked commands directly. If the sandbox returns an error due to policy, it's already logged as `task.failed`. Add `sandbox.blocked` when the sandbox returns a policy error:

This is getting complex. The sandbox package handles policy enforcement internally. The agent only sees the result (success or error). We can't distinguish "blocked by policy" from "execution failed" at the agent level without changing the sandbox package.

**Simplified approach:** Add `sandbox.executed` after successful sandbox execution in the task handler. Skip `sandbox.blocked` since it requires sandbox package changes. Document that `sandbox.blocked` can be added when the sandbox exposes policy denial errors distinctly.

- [ ] **Step 2: Add sandbox.executed events**

In `internal/app/agent.go`, in the sandbox exec task handler, after successful command execution (after line 750), add:
```go
			a.auditLog.Log(AuditEvent{
				EventType: "sandbox.executed", Component: "sandbox",
				Action: "execute", Status: "success",
				Details: map[string]interface{}{"task_id": t.TaskID},
			})
```

After successful script execution (after line 735), add:
```go
			a.auditLog.Log(AuditEvent{
				EventType: "sandbox.executed", Component: "sandbox",
				Action: "execute_script", Status: "success",
				Details: map[string]interface{}{"task_id": t.TaskID},
			})
```

- [ ] **Step 3: Run tests**

Run: `go test -race ./internal/app/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/app/agent.go
git commit -m "feat(audit): add sandbox.executed audit event"
```

---

### Task 13: Fix CI integration job dependency

**Files:**
- Modify: `.github/workflows/ci.yml:61`

- [ ] **Step 1: Fix integration job dependency**

In `.github/workflows/ci.yml`, change line 61:
```yaml
    needs: [go]
```
To:
```yaml
    needs: [go, rust]
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "fix(ci): add rust dependency to integration job"
```

---

### Task 14: Final verification

- [ ] **Step 1: Run all tests**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: Clean build

- [ ] **Step 4: Verify audit event coverage**

Count the audit event types in agent.go. Expected: 15+ distinct event types (config.loaded, config.reloaded, config.rejected, task.started, task.completed, task.failed, task.cancelled, grpc.connected, grpc.disconnected, plugin.started, plugin.stopped, sandbox.executed, agent.started, agent.shutting_down, agent.stopped).

- [ ] **Step 5: Verify metrics wiring**

Check that `IncMetricsCollected`, `IncPluginRequests`, `IncTasksCompleted`, `IncTasksFailed` are all called in the agent code.
