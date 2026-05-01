# Code Review Fixes Design

**Date:** 2026-04-30
**Scope:** Fix all 10 issues identified in the platform maturity code review

## Problem Statement

The code review of the last 5 commits (platform maturity + custom plugin SDK) identified 10 issues: 5 important and 5 minor. These range from unwired Prometheus counters to missing audit events to broken version propagation. All need to be fixed before production use.

## Issues Summary

| # | Severity | Issue | File(s) |
|---|----------|-------|---------|
| 1 | Important | Prometheus counters registered but never incremented | metrics.go, agent.go |
| 2 | Important | Dead code (`totalFields`) in RunOnce | agent.go:488-491 |
| 3 | Important | Only 8/18 audit event types implemented | agent.go, audit.go |
| 4 | Important | `plugins` command doesn't list custom plugins | commands.go |
| 5 | Important | `validate` command doesn't verify pipeline init | commands.go |
| 6 | Minor | `last_collection` missing from scheduler health | scheduler.go |
| 7 | Minor | Version propagation broken (server always shows "dev") | server.go, handlers.go |
| 8 | Minor | Benchmark uses mocks (acceptable, document trade-off) | benchmark_test.go |
| 9 | Minor | CI integration job doesn't depend on rust | ci.yml |
| 10 | Minor | MetricsRegistry tests already exist (reviewer missed them) | metrics_test.go |

Issue 8 (benchmark mocks) is an acceptable trade-off for CI stability — no code change needed, just a comment. Issue 10 is a false positive — tests already exist.

## Design Decisions

### Decision 1: Audit events from subsystems

**Chosen: Log from the agent layer.**

The agent has access to both the audit logger and all subsystems. Audit calls are added in the agent where it observes state changes. No subsystem interfaces change. For sandbox events, bridge the existing sandbox audit logger to the app-level one via a callback.

**Alternatives rejected:**
- Inject audit logger into subsystems: adds coupling, changes subsystem interfaces
- Event callback pattern: over-engineered for this scope

### Decision 2: Version propagation

**Chosen: Pass via ServerConfig.**

Add `Version` and `GitCommit` fields to `server.Config`. Agent sets them from its own vars at construction. Simple, explicit, no new packages.

**Alternatives rejected:**
- Shared version package: adds a new package for 3 variables
- Duplicate ldflags injection: fragile, two injection points to maintain

### Decision 3: Commands needing subsystem access

**Chosen: Dependency injection via command constructors.**

`newPluginsCommand(gateway, runtime)` and `newValidateCommand(buildScheduler)`. Pass subsystems when constructing commands in `NewRootCommand`. Standard DI, keeps commands testable.

**Alternatives rejected:**
- Lazy access via Agent closures: couples commands to agent

---

## Changes by File

### 1. `internal/app/metrics.go`

**Add convenience methods:**

```go
func (r *MetricsRegistry) IncPipelineErrors(stage, plugin string) {
    r.PipelineErrors.WithLabelValues(stage, plugin).Inc()
}

func (r *MetricsRegistry) IncPluginRequests(plugin, taskType, status string) {
    r.PluginRequests.WithLabelValues(plugin, taskType, status).Inc()
}
```

### 2. `internal/app/agent.go`

**Wire Prometheus counters:**
- Call `a.metricsReg.IncMetricsCollected()` in `handlePipelineMetrics()` after processing
- Call `a.metricsReg.IncGRPCReconnects()` in the gRPC reconnection path
- Call `a.metricsReg.IncPipelineErrors(stage, plugin)` in task error handlers where pipeline stage is known
- Call `a.metricsReg.IncPluginRequests(plugin, taskType, status)` in plugin task handlers
- Call `a.metricsReg.UpdateSystemMetrics(...)` from the system metrics collection path

**Fix dead code (line 488-492):**
```go
fmt.Printf("Collected %d metrics (%d total fields) from pipeline\n", len(metrics), totalFields)
```

**Add missing audit events:**
- `config.loaded` — after `config.Load()` succeeds at startup
- `config.reloaded` — after hot-reload succeeds
- `config.rejected` — after hot-reload fails validation
- `grpc.connected` — after gRPC client reports connected
- `grpc.disconnected` — after gRPC client reports disconnected
- `grpc.reconnecting` — after gRPC client reports reconnecting
- `plugin.started` — after PluginRuntime starts a plugin
- `plugin.stopped` — after PluginRuntime stops a plugin
- `plugin.crashed` — after PluginRuntime detects a crash
- `sandbox.executed` / `sandbox.blocked` — bridge from sandbox audit logger via callback

**Pass version to server config:**
```go
serverCfg.Version = Version
serverCfg.GitCommit = GitCommit
```

**Pass subsystems to command constructors:**
```go
rootCmd.AddCommand(newPluginsCommand(a.gateway, a.pluginRuntime))
rootCmd.AddCommand(newValidateCommand(a.buildCollectorScheduler))
```

### 3. `internal/app/commands.go`

**Enhance `plugins` command:**
- Accept `PluginGateway` and `PluginRuntime` parameters
- After built-in plugins, query gateway for custom plugins (name, version, status, task types)
- Query plugin runtime for health status, display connection info
- If gateway/runtime are nil, skip custom plugins section gracefully

**Enhance `validate` command:**
- Accept a scheduler builder function parameter
- After config loads, attempt to build the scheduler to verify all factories resolve
- Print initialized component names (inputs, processors, aggregators, outputs)
- Don't start any services

### 4. `internal/server/server.go`

**Add version fields to Config:**
```go
type Config struct {
    // ... existing fields
    Version   string
    GitCommit string
}
```

### 5. `internal/server/handlers.go`

**Read version from config:**
- Pass version info through to the health handler (via closure or handler struct)
- Remove reliance on package-level `Version`/`GitCommit` vars for the `/healthz` endpoint

### 6. `internal/collector/scheduler.go`

**Add `last_collection` tracking:**
- Add `lastCollection time.Time` field to `Scheduler` struct
- Update it after each pipeline collection cycle
- Include in `HealthStatus()` details:
```go
Details: map[string]any{
    "inputs_active":    inputCount,
    "last_collection":  s.lastCollection,
}
```

### 7. `internal/collector/scheduler_test.go`

**Add test:**
- Verify `last_collection` is zero before first collection
- Verify `last_collection` is populated after a collection cycle

### 8. `.github/workflows/ci.yml`

**Fix integration job dependency:**
```yaml
needs: [go, rust]
```

### 9. `internal/app/metrics_test.go`

**Add test cases for new convenience methods:**
- Test `IncPipelineErrors` increments with correct labels
- Test `IncPluginRequests` increments with correct labels

### 10. `internal/app/audit_test.go`

**Add tests for new event types:**
- Test that config, grpc, plugin, sandbox events are logged correctly

---

## Non-Changes

- **Benchmark mocks** (`benchmark_test.go`): The use of local mock types instead of real registered inputs is an acceptable trade-off for CI stability. Add a comment documenting this decision.
- **MetricsRegistry tests** (`metrics_test.go`): Already exist with 3 test functions. No action needed.

## Verification

After implementation:
1. `go test -race ./internal/app/` — metrics and audit tests pass
2. `go test -race ./internal/collector/` — scheduler health tests pass
3. `go test -race ./internal/server/` — health endpoint tests pass
4. `go vet ./...` — no issues
5. `go build ./...` — compiles cleanly
6. Verify `/metrics` endpoint shows non-zero counters after running tasks
7. Verify `/healthz` shows correct version and `last_collection`
8. Verify `opsagent plugins` lists custom plugins when gateway is available
9. Verify `opsagent validate` attempts pipeline initialization
