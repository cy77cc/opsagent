# Sub-Plan 1: Proto & gRPC Foundation

> **Parent:** [NodeAgentX Full Implementation Plan](../2026-04-28-nodeagentx-full-implementation.md)

**Goal:** Define the gRPC service contract in protobuf and generate Go code.

**Files:**
- Create: `proto/agent.proto`
- Create: `internal/grpcclient/proto/` (generated)
- Modify: `go.mod`, `go.sum`
- Modify: `Makefile`

---

## Task 1.1: Install protoc and Go gRPC plugins

- [ ] **Step 1: Install protoc compiler**

```bash
apt-get update && apt-get install -y protobuf-compiler
protoc --version  # should show libprotoc 3.x+
```

- [ ] **Step 2: Install Go protobuf and gRPC plugins**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

- [ ] **Step 3: Verify plugins are on PATH**

```bash
which protoc-gen-go
which protoc-gen-go-grpc
```

Expected: Both found in `$GOPATH/bin` or `$HOME/go/bin`.

---

## Task 1.2: Add gRPC dependencies

- [ ] **Step 1: Add dependencies to go.mod**

```bash
cd /root/project/NodeAgentX
go get google.golang.org/grpc@latest
go get google.golang.org/protobuf@latest
go mod tidy
```

- [ ] **Step 2: Verify go.mod contains new dependencies**

```bash
grep -c "google.golang.org/grpc" go.mod
grep -c "google.golang.org/protobuf" go.mod
```

Expected: Both return 1 (direct dependency).

- [ ] **Step 3: Verify build still works**

```bash
go build ./...
```

Expected: Compiles without errors.

---

## Task 1.3: Write the proto file

- [ ] **Step 1: Create proto directory**

```bash
mkdir -p proto
```

- [ ] **Step 2: Write `proto/agent.proto`**

```protobuf
syntax = "proto3";
package nodeagentx;
option go_package = "nodeagentx/internal/grpcclient/proto";

// AgentService is the bidirectional streaming service between Agent and Platform.
service AgentService {
  rpc Connect(stream AgentMessage) returns (stream PlatformMessage);
}

// AgentMessage is sent from Agent to Platform.
message AgentMessage {
  oneof payload {
    AgentRegistration registration = 1;
    Heartbeat heartbeat = 2;
    MetricBatch metrics = 3;
    ExecOutput exec_output = 4;
    ExecResult exec_result = 5;
    Ack ack = 6;
  }
}

// PlatformMessage is sent from Platform to Agent.
message PlatformMessage {
  oneof payload {
    ExecuteCommand exec_command = 1;
    ExecuteScript exec_script = 2;
    CancelJob cancel_job = 3;
    ConfigUpdate config_update = 4;
    Ack ack = 5;
  }
}

// AgentRegistration is sent once when the agent first connects.
message AgentRegistration {
  string agent_id = 1;
  string token = 2;
  AgentInfo agent_info = 3;
  repeated string capabilities = 4;
}

// AgentInfo contains host and agent metadata.
message AgentInfo {
  string hostname = 1;
  string os = 2;
  string arch = 3;
  string version = 4;
  int32 cpu_cores = 5;
  int64 memory_bytes = 6;
  int32 running_tasks = 7;
  int32 max_tasks = 8;
}

// Heartbeat is sent periodically to keep the connection alive.
message Heartbeat {
  string agent_id = 1;
  int64 timestamp_ms = 2;
  string status = 3;  // "ready" | "busy" | "degraded"
  AgentInfo agent_info = 4;
}

// MetricBatch is a batch of metrics sent from Agent to Platform.
message MetricBatch {
  repeated Metric metrics = 1;
}

// Metric represents a single metric data point.
message Metric {
  string name = 1;
  map<string, string> tags = 2;
  repeated Field fields = 3;
  int64 timestamp_ms = 4;
  MetricType type = 5;
}

// Field is a key-value pair in a metric.
message Field {
  string key = 1;
  oneof value {
    double double_value = 2;
    int64 int_value = 3;
    string string_value = 4;
    bool bool_value = 5;
  }
}

// MetricType is the type of a metric.
enum MetricType {
  GAUGE = 0;
  COUNTER = 1;
  HISTOGRAM = 2;
}

// ExecuteCommand requests execution of a single command in a sandbox.
message ExecuteCommand {
  string task_id = 1;
  string command = 2;
  repeated string args = 3;
  map<string, string> env = 4;
  int32 timeout_seconds = 5;
  SandboxConfig sandbox = 6;
}

// ExecuteScript requests execution of a script in a sandbox.
message ExecuteScript {
  string task_id = 1;
  string interpreter = 2;  // "bash", "python3", "perl"
  string script = 3;
  repeated string args = 4;
  map<string, string> env = 5;
  int32 timeout_seconds = 6;
  SandboxConfig sandbox = 7;
}

// SandboxConfig contains sandbox resource limits and policies.
message SandboxConfig {
  int32 memory_mb = 1;
  int32 cpu_quota_pct = 2;
  int32 max_pids = 3;
  string network_mode = 4;  // "none" | "allowlist"
  repeated string allowed_ips = 5;
  int32 max_output_kb = 6;
}

// ExecOutput streams stdout/stderr data during execution.
message ExecOutput {
  string task_id = 1;
  string stream = 2;  // "stdout" | "stderr"
  bytes data = 3;
  int64 timestamp_ms = 4;
}

// ExecResult is the final result of a sandbox execution.
message ExecResult {
  string task_id = 1;
  int32 exit_code = 2;
  int64 duration_ms = 3;
  bool timed_out = 4;
  bool truncated = 5;
  bool killed = 6;
  ExecStats stats = 7;
}

// ExecStats contains resource usage statistics from the sandbox.
message ExecStats {
  int64 peak_memory_bytes = 1;
  int64 cpu_time_user_ms = 2;
  int64 cpu_time_system_ms = 3;
  int32 process_count = 4;
  int64 bytes_written = 5;
  int64 bytes_read = 6;
}

// CancelJob requests cancellation of a running task.
message CancelJob {
  string task_id = 1;
  string reason = 2;
}

// ConfigUpdate pushes a configuration change from Platform to Agent.
message ConfigUpdate {
  bytes config_yaml = 1;
  int64 version = 2;
}

// Ack is a generic acknowledgement message.
message Ack {
  string ref_id = 1;
  bool success = 2;
  string error = 3;
}
```

- [ ] **Step 3: Verify proto syntax**

```bash
protoc --proto_path=proto --descriptor_set_out=/dev/null proto/agent.proto
```

Expected: No errors.

---

## Task 1.4: Generate Go code from proto

- [ ] **Step 1: Create output directory**

```bash
mkdir -p internal/grpcclient/proto
```

- [ ] **Step 2: Add proto-gen target to Makefile**

Append to `Makefile`:

```makefile
PROTO_DIR=proto
PROTO_OUT=internal/grpcclient/proto

proto-gen:
	protoc --go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/agent.proto
```

- [ ] **Step 3: Run code generation**

```bash
make proto-gen
```

Expected: Files created:
- `internal/grpcclient/proto/agent.pb.go`
- `internal/grpcclient/proto/agent_grpc.pb.go`

- [ ] **Step 4: Verify generated files exist**

```bash
ls -la internal/grpcclient/proto/agent.pb.go
ls -la internal/grpcclient/proto/agent_grpc.pb.go
```

- [ ] **Step 5: Verify build with generated code**

```bash
go build ./internal/grpcclient/proto/...
```

Expected: Compiles without errors.

- [ ] **Step 6: Commit**

```bash
git add proto/ internal/grpcclient/proto/ go.mod go.sum Makefile
git commit -m "feat: add protobuf definitions and gRPC code generation"
```
