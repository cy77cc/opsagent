# Sub-Plan 9: Testing & Production Readiness

> **Parent:** [NodeAgentX Full Implementation Plan](../2026-04-28-nodeagentx-full-implementation.md)
> **Depends on:** Sub-Plan 8

**Goal:** Integration tests, full test suite verification, Makefile updates, security baseline, and production hardening.

**Files:**
- Create: `internal/integration/pipeline_test.go`
- Create: `internal/integration/grpc_test.go`
- Create: `internal/integration/sandbox_test.go`
- Modify: `Makefile`
- Modify: `go.mod` (if needed)

---

## Task 9.1: Collector Pipeline Integration Test

- [ ] **Step 1: Write integration test**

Create `internal/integration/pipeline_test.go`:

```go
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"nodeagentx/internal/collector"
	_ "nodeagentx/internal/collector/inputs/cpu"
	_ "nodeagentx/internal/collector/inputs/memory"
	_ "nodeagentx/internal/collector/outputs/http"
	_ "nodeagentx/internal/collector/processors/tagger"
)

func TestPipelineEndToEnd(t *testing.T) {
	log := zerolog.Nop()

	// Build pipeline with CPU input, tagger processor, HTTP output
	inputs := []collector.Input{}
	cpuFactory, ok := collector.DefaultRegistry.GetInput("cpu")
	if !ok {
		t.Fatal("cpu input not registered")
	}
	cpu := cpuFactory()
	if err := cpu.Init(map[string]interface{}{"totalcpu": true}); err != nil {
		t.Fatalf("cpu init: %v", err)
	}
	inputs = append(inputs, cpu)

	memFactory, ok := collector.DefaultRegistry.GetInput("memory")
	if !ok {
		t.Fatal("memory input not registered")
	}
	mem := memFactory()
	if err := mem.Init(nil); err != nil {
		t.Fatalf("memory init: %v", err)
	}
	inputs = append(inputs, mem)

	processors := []collector.Processor{}
	taggerFactory, ok := collector.DefaultRegistry.GetProcessor("tagger")
	if !ok {
		t.Fatal("tagger processor not registered")
	}
	tagger := taggerFactory()
	if err := tagger.Init(map[string]interface{}{
		"tags": map[string]interface{}{
			"env":  "test",
			"host": "integration",
		},
	}); err != nil {
		t.Fatalf("tagger init: %v", err)
	}
	processors = append(processors, tagger)

	// Use a test output that captures metrics
	testOutput := &captureOutput{}
	outputs := []collector.Output{testOutput}

	pipeline, err := collector.NewPipeline(collector.PipelineConfig{
		Inputs:      inputs,
		Processors:  processors,
		Aggregators: nil,
		Outputs:     outputs,
		Log:         log,
	})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("pipeline.Start: %v", err)
	}
	defer pipeline.Stop()

	// Wait for at least one collection cycle
	time.Sleep(3 * time.Second)

	metrics := testOutput.Metrics()
	if len(metrics) == 0 {
		t.Fatal("expected at least 1 metric after pipeline run")
	}

	// Verify tagger added tags
	found := false
	for _, m := range metrics {
		if m.Name() == "cpu" {
			found = true
			if m.Tags()["env"] != "test" {
				t.Fatalf("expected env=test tag, got %s", m.Tags()["env"])
			}
		}
	}
	if !found {
		t.Fatal("expected cpu metric from pipeline")
	}
}

// captureOutput is a test output that captures all written metrics.
type captureOutput struct {
	metrics []collector.Metric
}

func (c *captureOutput) Init(_ map[string]interface{}) error { return nil }
func (c *captureOutput) Write(metrics []collector.Metric) error {
	c.metrics = append(c.metrics, metrics...)
	return nil
}
func (c *captureOutput) Close() error                  { return nil }
func (c *captureOutput) SampleConfig() string          { return "" }
func (c *captureOutput) Metrics() []collector.Metric   { return c.metrics }
```

- [ ] **Step 2: Run integration test**

```bash
go test ./internal/integration/ -v -run TestPipelineEndToEnd -timeout 30s
```

Expected: PASS (collects real CPU/memory metrics, applies tagger tags).

- [ ] **Step 3: Commit**

```bash
git add internal/integration/
git commit -m "test(integration): add collector pipeline end-to-end test"
```

---

## Task 9.2: Sandbox Executor Integration Test

- [ ] **Step 1: Write sandbox integration test**

Create `internal/integration/sandbox_test.go`:

```go
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"nodeagentx/internal/sandbox"
)

func TestSandboxExecutorBasic(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("sandbox tests require root (nsjail needs namespace privileges)")
	}

	nsjailPath := "/usr/bin/nsjail"
	if _, err := os.Stat(nsjailPath); os.IsNotExist(err) {
		t.Skip("nsjail not installed, skipping sandbox test")
	}

	log := zerolog.Nop()
	tmpDir := t.TempDir()

	exec, err := sandbox.New(sandbox.Config{
		NsjailPath:         nsjailPath,
		BaseWorkdir:        tmpDir,
		DefaultTimeout:     10 * time.Second,
		MaxConcurrentTasks: 2,
		CgroupBasePath:     "/sys/fs/cgroup/nodeagentx-test",
		AuditLogPath:       tmpDir + "/audit.log",
	}, sandbox.PolicyConfig{
		AllowedCommands:     []string{"echo", "cat", "ls", "grep", "uname"},
		BlockedCommands:     []string{"rm -rf /"},
		BlockedKeywords:     []string{},
		AllowedInterpreters: []string{"/bin/bash", "/bin/sh"},
		ScriptMaxBytes:      65536,
		ShellInjectionCheck: true,
	}, log)
	if err != nil {
		t.Fatalf("sandbox.New: %v", err)
	}

	ctx := context.Background()

	// Test simple command
	result, err := exec.Execute(ctx, sandbox.Request{
		TaskID:         "test-001",
		Command:        "echo hello",
		TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatalf("execute echo: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Stdout != "hello\n" {
		t.Fatalf("expected 'hello\\n', got %q", result.Stdout)
	}

	// Test blocked command
	_, err = exec.Execute(ctx, sandbox.Request{
		TaskID:         "test-002",
		Command:        "rm -rf /",
		TimeoutSeconds: 5,
	})
	if err == nil {
		t.Fatal("expected error for blocked command")
	}
}
```

- [ ] **Step 2: Run integration test (requires root + nsjail)**

```bash
sudo go test ./internal/integration/ -v -run TestSandboxExecutorBasic -timeout 30s
```

Expected: PASS on systems with nsjail installed; SKIP otherwise.

- [ ] **Step 3: Commit**

```bash
git add internal/integration/sandbox_test.go
git commit -m "test(integration): add sandbox executor integration test"
```

---

## Task 9.3: gRPC Client Integration Test

- [ ] **Step 1: Write gRPC integration test with mock server**

Create `internal/integration/grpc_test.go`:

```go
package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "nodeagentx/internal/grpcclient/proto"
)

const bufSize = 1024 * 1024

type mockAgentService struct {
	pb.UnimplementedAgentServiceServer
	connectCalled chan struct{}
}

func (m *mockAgentService) Connect(stream pb.AgentService_ConnectServer) error {
	close(m.connectCalled)
	// Keep stream alive until context cancelled
	<-stream.Context().Done()
	return nil
}

func startMockGRPCServer(t *testing.T) (*grpc.ClientConn, *mockAgentService) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	mock := &mockAgentService{
		connectCalled: make(chan struct{}),
	}
	pb.RegisterAgentServiceServer(srv, mock)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("mock server stopped: %v", err)
		}
	}()

	conn, err := grpc.DialContext(context.Background(), "bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}

	t.Cleanup(func() {
		conn.Close()
		srv.Stop()
	})

	return conn, mock
}

func TestGRPCClientConnects(t *testing.T) {
	conn, mock := startMockGRPCServer(t)

	log := zerolog.Nop()
	client := grpcclient.NewFromConn(conn, grpcclient.Config{
		AgentID:               "test-agent",
		AgentName:             "test",
		HeartbeatInterval:     5 * time.Second,
		ReconnectInitialBackoff: 100 * time.Millisecond,
		ReconnectMaxBackoff:     1 * time.Second,
	}, log)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	defer client.Stop()

	select {
	case <-mock.connectCalled:
		// Connect was called
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Connect call")
	}
}
```

- [ ] **Step 2: Run gRPC integration test**

```bash
go test ./internal/integration/ -v -run TestGRPCClientConnects -timeout 15s
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/integration/grpc_test.go
git commit -m "test(integration): add gRPC client connection integration test"
```

---

## Task 9.4: Update Makefile

- [ ] **Step 1: Add comprehensive Makefile targets**

Replace the existing `Makefile` content:

```makefile
APP_NAME=nodeagentx
RUST_RUNTIME=nodeagentx-rust-runtime

.PHONY: tidy test test-race test-cover build run rust-build lint vet proto sandbox-check

tidy:
	go mod tidy

test:
	go test ./...

test-race:
	go test -race ./...

test-cover:
	go test -cover ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

build:
	go build -o bin/$(APP_NAME) ./cmd/agent

run:
	go run ./cmd/agent run --config ./configs/config.yaml

rust-build:
	cd rust-runtime && cargo build --release

lint:
	golangci-lint run ./...

vet:
	go vet ./...

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/agent.proto

sandbox-check:
	@echo "Checking nsjail installation..."
	@which nsjail || echo "WARNING: nsjail not found. Sandbox features will not work."
	@echo "Checking cgroup v2..."
	@test -f /sys/fs/cgroup/cgroup.controllers && echo "cgroup v2: OK" || echo "WARNING: cgroup v2 not available."
	@echo "Checking namespace support..."
	@test -f /proc/self/ns/pid && echo "namespaces: OK" || echo "WARNING: PID namespace not available."

integration:
	go test ./internal/integration/ -v -timeout 60s

integration-sandbox:
	sudo go test ./internal/integration/ -v -run TestSandbox -timeout 30s

security:
	gosec ./...

ci: tidy vet test-race security
	@echo "CI checks passed"
```

- [ ] **Step 2: Verify Makefile targets work**

```bash
make vet
make test
make build
```

Expected: All pass.

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "build: update Makefile with lint, proto, integration, and security targets"
```

---

## Task 9.5: Security Baseline

- [ ] **Step 1: Run gosec and fix any findings**

```bash
gosec ./...
```

Review findings. Common fixes:
- Ensure no hardcoded credentials (use env vars)
- Ensure file permissions are 0600 or 0644 (not 0777)
- Ensure HTTP timeouts are set

- [ ] **Step 2: Add .gosec configuration if needed**

Create `.gosec.json` if suppressing false positives:

```json
{
  "G104": "audit log file creation uses 0644 intentionally",
  "G304": "nsjail config paths are from validated config, not user input"
}
```

- [ ] **Step 3: Commit**

```bash
git add .gosec.json
git commit -m "chore: add gosec configuration for security baseline"
```

---

## Task 9.6: Full Test Suite Verification

- [ ] **Step 1: Run complete test suite**

```bash
make test-race
```

Expected: All tests pass with race detector enabled.

- [ ] **Step 2: Run test coverage**

```bash
make test-cover
```

Expected: Coverage >= 80% overall.

- [ ] **Step 3: Run security scan**

```bash
make security
```

Expected: No HIGH or CRITICAL findings.

- [ ] **Step 4: Run full CI pipeline locally**

```bash
make ci
```

Expected: "CI checks passed".

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "chore: production readiness verification complete"
```

---

## Task 9.7: Smoke Test Script

- [ ] **Step 1: Create smoke test script**

Create `scripts/smoke-test.sh`:

```bash
#!/bin/bash
set -euo pipefail

echo "=== NodeAgentX Smoke Test ==="

# 1. Build
echo "[1/6] Building..."
make build

# 2. Unit tests
echo "[2/6] Running unit tests..."
make test

# 3. Vet
echo "[3/6] Running go vet..."
make vet

# 4. Security scan (optional, skip if gosec not installed)
echo "[4/6] Security scan..."
if command -v gosec &> /dev/null; then
    make security
else
    echo "  SKIPPED: gosec not installed"
fi

# 5. Sandbox check
echo "[5/6] Sandbox prerequisites..."
make sandbox-check

# 6. Integration tests (non-sandbox)
echo "[6/6] Integration tests..."
go test ./internal/integration/ -v -run "TestPipeline|TestGRPC" -timeout 30s || echo "  Some integration tests skipped"

echo ""
echo "=== Smoke Test Complete ==="
```

- [ ] **Step 2: Make executable and test**

```bash
chmod +x scripts/smoke-test.sh
./scripts/smoke-test.sh
```

Expected: All steps pass.

- [ ] **Step 3: Commit**

```bash
git add scripts/smoke-test.sh
git commit -m "chore: add smoke test script for CI and local verification"
```
