# OpsAgent Full Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement two subsystems — Telegraf-style metrics collection pipeline and nsjail-based sandbox execution engine — connected via gRPC bidirectional streaming to OpsPilot platform.

**Architecture:** Single Go binary with three layers: gRPC client (agent-initiated connection, mTLS, bidirectional streaming), collector pipeline (Input→Accumulator→Processor→Aggregator→Output), and sandbox executor (nsjail isolation with namespace+cgroup+seccomp). All existing functionality preserved; new subsystems integrated via `internal/app/agent.go` lifecycle.

**Tech Stack:** Go 1.24, gRPC (google.golang.org/grpc), protobuf, nsjail (external binary), cgroup v2, gopsutil (existing), zerolog (existing), Viper (existing)

**Spec:** `docs/superpowers/specs/2026-04-28-opsagent-full-design.md`

---

## Sub-Plans

Execute in dependency order. Each sub-plan is self-contained with its own TDD cycle.

| # | Sub-Plan | Dependencies | Files |
|---|----------|-------------|-------|
| 1 | [Proto & gRPC Foundation](subplans/01-proto-grpc.md) | None | `proto/`, `internal/grpcclient/proto/`, `go.mod`, `Makefile` |
| 2 | [Collector Pipeline Core](subplans/02-collector-pipeline.md) | Sub-plan 1 (proto types) | `internal/collector/{metric,accumulator,buffer,scheduler,input,output,processor,aggregator}.go` |
| 3 | [Input Plugins](subplans/03-input-plugins.md) | Sub-plan 2 (interfaces) | `internal/collector/inputs/{cpu,memory,disk,net,process}/` |
| 4 | [Output Plugins](subplans/04-output-plugins.md) | Sub-plan 2 (interfaces) | `internal/collector/outputs/{http,prometheus,promrw}/` |
| 5 | [Processor & Aggregator Plugins](subplans/05-processor-aggregator.md) | Sub-plan 2 (interfaces) | `internal/collector/{processors,aggregators}/` |
| 6 | [gRPC Client](subplans/06-grpc-client.md) | Sub-plans 1, 2 | `internal/grpcclient/{client,sender,receiver,cache}.go` |
| 7 | [Sandbox Executor](subplans/07-sandbox-executor.md) | Sub-plan 1 (proto types) | `internal/sandbox/{executor,nsjail,policy,output_streamer,stats,audit,network}.go` |
| 8 | [Config & Agent Wiring](subplans/08-config-wiring.md) | Sub-plans 1-7 | `internal/config/`, `internal/app/`, `configs/` |
| 9 | [Testing & Production Readiness](subplans/09-testing-production.md) | Sub-plan 8 | Tests, Makefile, docs |

---

## Dependency Graph

```
Sub-plan 1: Proto & gRPC Foundation
 │
 ├──→ Sub-plan 2: Collector Pipeline Core
 │     │
 │     ├──→ Sub-plan 3: Input Plugins
 │     ├──→ Sub-plan 4: Output Plugins
 │     └──→ Sub-plan 5: Processor & Aggregator Plugins
 │
 ├──→ Sub-plan 6: gRPC Client
 │
 └──→ Sub-plan 7: Sandbox Executor

Sub-plan 8: Config & Agent Wiring (depends on 1-7)
 │
 └──→ Sub-plan 9: Testing & Production Readiness
```

---

## File Structure

### New Files (All Sub-Plans)

```
proto/agent.proto                                    # Sub-plan 1
internal/grpcclient/proto/agent.pb.go                # Sub-plan 1 (generated)
internal/grpcclient/proto/agent_grpc.pb.go           # Sub-plan 1 (generated)
internal/grpcclient/client.go                        # Sub-plan 6
internal/grpcclient/sender.go                        # Sub-plan 6
internal/grpcclient/receiver.go                      # Sub-plan 6
internal/grpcclient/cache.go                         # Sub-plan 6
internal/collector/metric.go                         # Sub-plan 2
internal/collector/accumulator.go                    # Sub-plan 2
internal/collector/buffer.go                         # Sub-plan 2
internal/collector/scheduler.go                      # Sub-plan 2
internal/collector/input.go                          # Sub-plan 2
internal/collector/output.go                         # Sub-plan 2
internal/collector/processor.go                      # Sub-plan 2
internal/collector/aggregator.go                     # Sub-plan 2
internal/collector/inputs/cpu/cpu.go                 # Sub-plan 3
internal/collector/inputs/memory/memory.go           # Sub-plan 3
internal/collector/inputs/disk/disk.go               # Sub-plan 3
internal/collector/inputs/net/net.go                 # Sub-plan 3
internal/collector/inputs/process/process.go         # Sub-plan 3
internal/collector/outputs/http/http.go              # Sub-plan 4
internal/collector/outputs/prometheus/prometheus.go  # Sub-plan 4
internal/collector/outputs/promrw/promrw.go          # Sub-plan 4
internal/collector/processors/regex/regex.go         # Sub-plan 5
internal/collector/processors/tagger/tagger.go       # Sub-plan 5
internal/collector/aggregators/avg/avg.go            # Sub-plan 5
internal/collector/aggregators/sum/sum.go            # Sub-plan 5
internal/sandbox/executor.go                         # Sub-plan 7
internal/sandbox/nsjail.go                           # Sub-plan 7
internal/sandbox/policy.go                           # Sub-plan 7
internal/sandbox/output_streamer.go                  # Sub-plan 7
internal/sandbox/stats.go                            # Sub-plan 7
internal/sandbox/audit.go                            # Sub-plan 7
internal/sandbox/network.go                          # Sub-plan 7
```

### Modified Files

```
go.mod                                               # Sub-plan 1
Makefile                                             # Sub-plan 1, 9
internal/config/config.go                            # Sub-plan 8
internal/config/config_test.go                       # Sub-plan 8
internal/app/agent.go                                # Sub-plan 8
configs/config.yaml                                  # Sub-plan 8
```
