# New Input Plugins Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 5 new input plugins (load, diskio, temp, gpu, connections) to OpsAgent's collector pipeline.

**Architecture:** Each plugin follows the existing `collector.Input` pattern: `init()` registration, `Init(cfg)` for config parsing and availability detection, `Gather(ctx, acc)` for metric collection. Hardware-dependent plugins use `available` field for graceful degradation.

**Tech Stack:** Go, gopsutil/v4, zerolog, nvidia-smi (for GPU)

---

## File Structure

```
internal/collector/inputs/
├── load/
│   ├── load.go              # Load Average plugin
│   └── load_test.go         # Unit tests
├── diskio/
│   ├── diskio.go            # Disk IO plugin
│   └── diskio_test.go       # Unit tests
├── temp/
│   ├── temp.go              # Temperature plugin
│   └── temp_test.go         # Unit tests
├── gpu/
│   ├── gpu.go               # GPU/NVIDIA plugin
│   └── gpu_test.go          # Unit tests (mock nvidia-smi output)
├── connections/
│   ├── connections.go        # Network Connections plugin
│   └── connections_test.go   # Unit tests
internal/app/agent.go         # Add blank imports
configs/config.yaml           # Add example configs
```

---

### Task 1: Load Average Plugin

**Files:**
- Create: `internal/collector/inputs/load/load.go`
- Create: `internal/collector/inputs/load/load_test.go`

- [ ] **Step 1: Write the failing test for Init**

```go
// internal/collector/inputs/load/load_test.go
package load

import "testing"

func TestLoadInputInit(t *testing.T) {
	input := &LoadInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestLoadInputInitIgnoresExtraFields(t *testing.T) {
	input := &LoadInput{}
	cfg := map[string]interface{}{
		"unknown_field": "value",
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() should ignore extra fields, got error: %v", err)
	}
}

func TestLoadInputSampleConfig(t *testing.T) {
	input := &LoadInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/inputs/load/`
Expected: FAIL with "undefined: LoadInput"

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/inputs/load/load.go
package load

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/load"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("load", func() collector.Input {
		return &LoadInput{}
	})
}

// LoadInput gathers system load average metrics.
type LoadInput struct{}

func (l *LoadInput) Init(cfg map[string]interface{}) error {
	return nil
}

func (l *LoadInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	avg, err := load.AvgWithContext(ctx)
	if err != nil {
		return fmt.Errorf("load: failed to get load average: %w", err)
	}

	fields := map[string]interface{}{
		"load1":  avg.Load1,
		"load5":  avg.Load5,
		"load15": avg.Load15,
	}
	acc.AddGauge("load", nil, fields)
	return nil
}

func (l *LoadInput) SampleConfig() string {
	return `
  ## No configuration required for load input.
`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/load/`
Expected: PASS

- [ ] **Step 5: Write the failing test for Gather**

```go
func TestLoadInputGather(t *testing.T) {
	input := &LoadInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("Gather() produced 0 metrics, want at least 1")
	}

	m := metrics[0]
	if m.Name() != "load" {
		t.Errorf("metric name = %q, want %q", m.Name(), "load")
	}

	fields := m.Fields()
	for _, key := range []string{"load1", "load5", "load15"} {
		v, ok := fields[key]
		if !ok {
			t.Errorf("missing field %q", key)
			continue
		}
		if _, ok := v.(float64); !ok {
			t.Errorf("field %q should be float64, got %T", key, v)
		}
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/load/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/inputs/load/
git commit -m "feat(collector): add load average input plugin"
```

---

### Task 2: Disk IO Plugin

**Files:**
- Create: `internal/collector/inputs/diskio/diskio.go`
- Create: `internal/collector/inputs/diskio/diskio_test.go`

- [ ] **Step 1: Write the failing test for Init**

```go
// internal/collector/inputs/diskio/diskio_test.go
package diskio

import "testing"

func TestDiskIOInputInit(t *testing.T) {
	input := &DiskIOInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.devices) != 0 {
		t.Errorf("devices should be empty by default, got %v", input.devices)
	}
}

func TestDiskIOInputInitWithDevices(t *testing.T) {
	input := &DiskIOInput{}
	cfg := map[string]interface{}{
		"devices": []interface{}{"sda", "nvme0n1"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.devices) != 2 {
		t.Errorf("devices len = %d, want 2", len(input.devices))
	}
}

func TestDiskIOInputInitInvalidDevices(t *testing.T) {
	input := &DiskIOInput{}
	cfg := map[string]interface{}{
		"devices": "notalist",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid devices type")
	}
}

func TestDiskIOInputInitInvalidDeviceItem(t *testing.T) {
	input := &DiskIOInput{}
	cfg := map[string]interface{}{
		"devices": []interface{}{123},
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with non-string device item")
	}
}

func TestDiskIOInputSampleConfig(t *testing.T) {
	input := &DiskIOInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/inputs/diskio/`
Expected: FAIL with "undefined: DiskIOInput"

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/inputs/diskio/diskio.go
package diskio

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("diskio", func() collector.Input {
		return &DiskIOInput{}
	})
}

// DiskIOInput gathers disk IO counters.
type DiskIOInput struct {
	devices []string
}

func (d *DiskIOInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["devices"]; ok {
		switch devs := v.(type) {
		case []interface{}:
			for _, item := range devs {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("diskio: devices items must be strings, got %T", item)
				}
				d.devices = append(d.devices, s)
			}
		case []string:
			d.devices = devs
		default:
			return fmt.Errorf("diskio: devices must be a list, got %T", v)
		}
	}
	return nil
}

func (d *DiskIOInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	counters, err := disk.IOCountersWithContext(ctx, d.devices...)
	if err != nil {
		return fmt.Errorf("diskio: failed to get IO counters: %w", err)
	}

	for name, counter := range counters {
		tags := map[string]string{
			"device": name,
		}
		fields := map[string]interface{}{
			"read_bytes":    int64(counter.ReadBytes),
			"write_bytes":   int64(counter.WriteBytes),
			"read_count":    int64(counter.ReadCount),
			"write_count":   int64(counter.WriteCount),
			"read_time_ms":  int64(counter.ReadTime),
			"write_time_ms": int64(counter.WriteTime),
		}
		acc.AddCounter("diskio", tags, fields)
	}

	return nil
}

func (d *DiskIOInput) SampleConfig() string {
	return `
  ## List of devices to filter. Empty means all devices.
  # devices = ["sda", "nvme0n1"]
`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/diskio/`
Expected: PASS

- [ ] **Step 5: Write the failing test for Gather**

```go
func TestDiskIOInputGather(t *testing.T) {
	input := &DiskIOInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) == 0 {
		t.Fatal("Gather() produced 0 metrics, want at least 1")
	}

	m := metrics[0]
	if m.Name() != "diskio" {
		t.Errorf("metric name = %q, want %q", m.Name(), "diskio")
	}

	tags := m.Tags()
	if tags["device"] == "" {
		t.Error("missing 'device' tag")
	}

	expectedFields := []string{"read_bytes", "write_bytes", "read_count", "write_count", "read_time_ms", "write_time_ms"}
	fields := m.Fields()
	for _, f := range expectedFields {
		if _, ok := fields[f]; !ok {
			t.Errorf("missing field %q", f)
		}
	}
}

func TestDiskIOInputGatherFilterDevice(t *testing.T) {
	input := &DiskIOInput{}
	cfg := map[string]interface{}{
		"devices": []interface{}{"nonexistent_device"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	// Should produce 0 metrics for nonexistent device
	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics for nonexistent device, got %d", len(metrics))
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/diskio/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/inputs/diskio/
git commit -m "feat(collector): add disk IO input plugin"
```

---

### Task 3: Temperature Plugin

**Files:**
- Create: `internal/collector/inputs/temp/temp.go`
- Create: `internal/collector/inputs/temp/temp_test.go`

- [ ] **Step 1: Write the failing test for Init**

```go
// internal/collector/inputs/temp/temp_test.go
package temp

import "testing"

func TestTempInputInit(t *testing.T) {
	input := &TempInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
}

func TestTempInputSampleConfig(t *testing.T) {
	input := &TempInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/inputs/temp/`
Expected: FAIL with "undefined: TempInput"

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/inputs/temp/temp.go
package temp

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v4/host"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("temp", func() collector.Input {
		return &TempInput{}
	})
}

// TempInput gathers temperature sensor metrics.
type TempInput struct {
	available bool
}

func (t *TempInput) Init(cfg map[string]interface{}) error {
	// Check availability by trying to read sensors once
	temps, err := host.SensorsTemperaturesWithContext(context.Background())
	if err != nil || len(temps) == 0 {
		t.available = false
		log.Info().Str("plugin", "temp").Msg("temperature sensors unavailable, skipping")
		return nil
	}
	t.available = true
	return nil
}

func (t *TempInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	if !t.available {
		return nil
	}

	temps, err := host.SensorsTemperaturesWithContext(ctx)
	if err != nil {
		return fmt.Errorf("temp: failed to get temperatures: %w", err)
	}

	for _, sensor := range temps {
		tags := map[string]string{
			"sensor": sensor.SensorKey,
		}
		if sensor.Label != "" {
			tags["label"] = sensor.Label
		}
		fields := map[string]interface{}{
			"temperature": sensor.Temperature,
		}
		acc.AddGauge("temp", tags, fields)
	}

	return nil
}

func (t *TempInput) SampleConfig() string {
	return `
  ## No configuration required for temperature input.
  ## Sensors are auto-detected. If unavailable, plugin is silently skipped.
`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/temp/`
Expected: PASS

- [ ] **Step 5: Write the failing test for Gather (unavailable case)**

```go
func TestTempInputGatherUnavailable(t *testing.T) {
	input := &TempInput{available: false}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics when unavailable, got %d", len(metrics))
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/temp/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/inputs/temp/
git commit -m "feat(collector): add temperature input plugin"
```

---

### Task 4: GPU/NVIDIA Plugin

**Files:**
- Create: `internal/collector/inputs/gpu/gpu.go`
- Create: `internal/collector/inputs/gpu/gpu_test.go`

- [ ] **Step 1: Write the failing test for Init**

```go
// internal/collector/inputs/gpu/gpu_test.go
package gpu

import "testing"

func TestGPUInputInit(t *testing.T) {
	input := &GPUInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if input.binPath != "" {
		t.Errorf("binPath should be empty by default, got %q", input.binPath)
	}
}

func TestGPUInputInitWithBinPath(t *testing.T) {
	input := &GPUInput{}
	cfg := map[string]interface{}{
		"bin_path": "/usr/bin/nvidia-smi",
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if input.binPath != "/usr/bin/nvidia-smi" {
		t.Errorf("binPath = %q, want %q", input.binPath, "/usr/bin/nvidia-smi")
	}
}

func TestGPUInputInitInvalidBinPath(t *testing.T) {
	input := &GPUInput{}
	cfg := map[string]interface{}{
		"bin_path": 123,
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid bin_path type")
	}
}

func TestGPUInputSampleConfig(t *testing.T) {
	input := &GPUInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/inputs/gpu/`
Expected: FAIL with "undefined: GPUInput"

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/inputs/gpu/gpu.go
package gpu

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("gpu", func() collector.Input {
		return &GPUInput{}
	})
}

// GPUInput gathers NVIDIA GPU metrics via nvidia-smi.
type GPUInput struct {
	binPath   string
	available bool
	timeout   time.Duration
}

func (g *GPUInput) Init(cfg map[string]interface{}) error {
	g.timeout = 5 * time.Second

	if v, ok := cfg["bin_path"]; ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("gpu: bin_path must be a string, got %T", v)
		}
		g.binPath = s
	}

	// Check if nvidia-smi is available
	binPath := g.binPath
	if binPath == "" {
		binPath = "nvidia-smi"
	}
	if _, err := exec.LookPath(binPath); err != nil {
		g.available = false
		log.Info().Str("plugin", "gpu").Msg("nvidia-smi not found, skipping GPU metrics")
		return nil
	}
	g.available = true
	return nil
}

func (g *GPUInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	if !g.available {
		return nil
	}

	binPath := g.binPath
	if binPath == "" {
		binPath = "nvidia-smi"
	}

	query := "index,name,utilization.gpu,utilization.memory,memory.total,memory.used,temperature.gpu,power.draw,fan.speed"
	args := []string{
		fmt.Sprintf("--query-gpu=%s", query),
		"--format=csv,noheader,nounits",
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gpu: nvidia-smi execution failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		tags, fields, parseErr := parseGPUOutput(line)
		if parseErr != nil {
			log.Warn().Err(parseErr).Str("line", line).Msg("gpu: skipping malformed line")
			continue
		}
		acc.AddGauge("gpu", tags, fields)
	}

	return nil
}

func (g *GPUInput) SampleConfig() string {
	return `
  ## Path to nvidia-smi binary. Default is to find it in PATH.
  # bin_path = "/usr/bin/nvidia-smi"
`
}

// parseGPUOutput parses a single CSV line from nvidia-smi output.
func parseGPUOutput(line string) (map[string]string, map[string]interface{}, error) {
	parts := strings.Split(line, ", ")
	if len(parts) < 9 {
		return nil, nil, fmt.Errorf("unexpected field count: %d, want >= 9", len(parts))
	}

	tags := map[string]string{
		"gpu_index": strings.TrimSpace(parts[0]),
		"gpu_name":  strings.TrimSpace(parts[1]),
	}

	fields := map[string]interface{}{}
	floatFields := map[string]int{
		"utilization_gpu":    2,
		"utilization_memory": 3,
		"memory_total":       4,
		"memory_used":        5,
		"temperature":        6,
		"power_draw":         7,
		"fan_speed":          8,
	}

	for field, idx := range floatFields {
		v := strings.TrimSpace(parts[idx])
		if v == "[N/A]" || v == "" {
			continue
		}
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err != nil {
			continue
		}
		fields[field] = f
	}

	return tags, fields, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/gpu/`
Expected: PASS

- [ ] **Step 5: Write the failing test for parseGPUOutput**

```go
func TestParseGPUOutput(t *testing.T) {
	line := "0, NVIDIA GeForce RTX 3090, 45, 30, 24576, 8192, 65, 250, 50"
	tags, fields, err := parseGPUOutput(line)
	if err != nil {
		t.Fatalf("parseGPUOutput() error: %v", err)
	}

	if tags["gpu_index"] != "0" {
		t.Errorf("gpu_index = %q, want %q", tags["gpu_index"], "0")
	}
	if tags["gpu_name"] != "NVIDIA GeForce RTX 3090" {
		t.Errorf("gpu_name = %q, want %q", tags["gpu_name"], "NVIDIA GeForce RTX 3090")
	}

	expectedFields := map[string]float64{
		"utilization_gpu":    45,
		"utilization_memory": 30,
		"memory_total":       24576,
		"memory_used":        8192,
		"temperature":        65,
		"power_draw":         250,
		"fan_speed":          50,
	}
	for k, expected := range expectedFields {
		v, ok := fields[k]
		if !ok {
			t.Errorf("missing field %q", k)
			continue
		}
		f, ok := v.(float64)
		if !ok {
			t.Errorf("field %q should be float64, got %T", k, v)
			continue
		}
		if f != expected {
			t.Errorf("field %q = %f, want %f", k, f, expected)
		}
	}
}

func TestParseGPUOutputNA(t *testing.T) {
	line := "0, NVIDIA GeForce RTX 3090, [N/A], [N/A], 24576, 8192, [N/A], [N/A], [N/A]"
	tags, fields, err := parseGPUOutput(line)
	if err != nil {
		t.Fatalf("parseGPUOutput() error: %v", err)
	}

	if tags["gpu_index"] != "0" {
		t.Errorf("gpu_index = %q, want %q", tags["gpu_index"], "0")
	}

	// N/A fields should be skipped
	if _, ok := fields["utilization_gpu"]; ok {
		t.Error("utilization_gpu should be skipped for [N/A]")
	}
	if _, ok := fields["memory_total"]; !ok {
		t.Error("memory_total should be present")
	}
}

func TestParseGPUOutputMalformed(t *testing.T) {
	line := "0, NVIDIA GeForce RTX 3090"
	_, _, err := parseGPUOutput(line)
	if err == nil {
		t.Fatal("parseGPUOutput() should fail with malformed line")
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/gpu/`
Expected: PASS

- [ ] **Step 7: Write the failing test for Gather (unavailable case)**

```go
func TestGPUInputGatherUnavailable(t *testing.T) {
	input := &GPUInput{available: false}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics when unavailable, got %d", len(metrics))
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/gpu/`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/collector/inputs/gpu/
git commit -m "feat(collector): add GPU/NVIDIA input plugin"
```

---

### Task 5: Network Connections Plugin

**Files:**
- Create: `internal/collector/inputs/connections/connections.go`
- Create: `internal/collector/inputs/connections/connections_test.go`

- [ ] **Step 1: Write the failing test for Init**

```go
// internal/collector/inputs/connections/connections_test.go
package connections

import "testing"

func TestConnectionsInputInit(t *testing.T) {
	input := &ConnectionsInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.states) != 0 {
		t.Errorf("states should be empty by default, got %v", input.states)
	}
}

func TestConnectionsInputInitWithStates(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{"ESTABLISHED", "LISTEN"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.states) != 2 {
		t.Errorf("states len = %d, want 2", len(input.states))
	}
}

func TestConnectionsInputInitInvalidStates(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": "notalist",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid states type")
	}
}

func TestConnectionsInputInitInvalidStateItem(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{123},
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with non-string state item")
	}
}

func TestConnectionsInputSampleConfig(t *testing.T) {
	input := &ConnectionsInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./internal/collector/inputs/connections/`
Expected: FAIL with "undefined: ConnectionsInput"

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/inputs/connections/connections.go
package connections

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v4/net"

	"github.com/cy77cc/opsagent/internal/collector"
)

func init() {
	collector.RegisterInput("connections", func() collector.Input {
		return &ConnectionsInput{}
	})
}

// ConnectionsInput gathers network connection statistics.
type ConnectionsInput struct {
	states []string
}

func (c *ConnectionsInput) Init(cfg map[string]interface{}) error {
	if v, ok := cfg["states"]; ok {
		switch states := v.(type) {
		case []interface{}:
			for _, item := range states {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("connections: states items must be strings, got %T", item)
				}
				c.states = append(c.states, s)
			}
		case []string:
			c.states = states
		default:
			return fmt.Errorf("connections: states must be a list, got %T", v)
		}
	}
	return nil
}

func (c *ConnectionsInput) Gather(ctx context.Context, acc collector.Accumulator) error {
	conns, err := net.ConnectionsWithContext(ctx, "all")
	if err != nil {
		if os.IsPermission(err) {
			log.Info().Str("plugin", "connections").Msg("permission denied, skipping")
			return nil
		}
		return fmt.Errorf("connections: failed to get connections: %w", err)
	}

	// Count by state and protocol
	counts := make(map[string]map[string]int)
	for _, conn := range conns {
		state := conn.Status
		protocol := "tcp"
		if conn.Type == 2 { // SOCK_DGRAM
			protocol = "udp"
		}

		if counts[protocol] == nil {
			counts[protocol] = make(map[string]int)
		}
		counts[protocol][state]++
	}

	// Filter by configured states if any
	for protocol, stateCounts := range counts {
		for state, count := range stateCounts {
			if len(c.states) > 0 {
				found := false
				for _, s := range c.states {
					if s == state {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			tags := map[string]string{
				"state":    state,
				"protocol": protocol,
			}
			fields := map[string]interface{}{
				"count_by_state": int64(count),
			}
			acc.AddGauge("connections", tags, fields)
		}
	}

	return nil
}

func (c *ConnectionsInput) SampleConfig() string {
	return `
  ## List of connection states to filter. Empty means all states.
  # states = ["ESTABLISHED", "LISTEN", "TIME_WAIT"]
`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/connections/`
Expected: PASS

- [ ] **Step 5: Write the failing test for Gather**

```go
func TestConnectionsInputGather(t *testing.T) {
	input := &ConnectionsInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// May be 0 if no connections, that's ok
	for _, m := range metrics {
		if m.Name() != "connections" {
			t.Errorf("metric name = %q, want %q", m.Name(), "connections")
		}
		tags := m.Tags()
		if tags["state"] == "" {
			t.Error("missing 'state' tag")
		}
		if tags["protocol"] == "" {
			t.Error("missing 'protocol' tag")
		}
		fields := m.Fields()
		if _, ok := fields["count_by_state"]; !ok {
			t.Error("missing 'count_by_state' field")
		}
	}
}

func TestConnectionsInputGatherFilterState(t *testing.T) {
	input := &ConnectionsInput{}
	cfg := map[string]interface{}{
		"states": []interface{}{"ESTABLISHED"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	for _, m := range metrics {
		tags := m.Tags()
		if tags["state"] != "ESTABLISHED" {
			t.Errorf("unexpected state: %q, expected ESTABLISHED", tags["state"])
		}
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -race ./internal/collector/inputs/connections/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/inputs/connections/
git commit -m "feat(collector): add network connections input plugin"
```

---

### Task 6: Register Plugins in agent.go

**Files:**
- Modify: `internal/app/agent.go:28-41`

- [ ] **Step 1: Add blank imports**

Add the following blank imports to `internal/app/agent.go` in the import block after the existing input imports:

```go
_ "github.com/cy77cc/opsagent/internal/collector/inputs/load"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/diskio"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/temp"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/gpu"
_ "github.com/cy77cc/opsagent/internal/collector/inputs/connections"
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/app/agent.go
git commit -m "feat(app): register new input plugins"
```

---

### Task 7: Update Example Config

**Files:**
- Modify: `configs/config.yaml`

- [ ] **Step 1: Add new plugin examples**

Add the following to the `collector.inputs` section in `configs/config.yaml` after the existing inputs:

```yaml
    - type: load
      config: {}
    - type: diskio
      config:
        # devices: ["sda", "nvme0n1"]
    - type: temp
      config: {}
    - type: gpu
      config:
        # bin_path: "/usr/bin/nvidia-smi"
    - type: connections
      config:
        # states: ["ESTABLISHED", "LISTEN"]
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add configs/config.yaml
git commit -m "config: add example configs for new input plugins"
```

---

### Task 8: Run All Tests

**Files:** None (verification only)

- [ ] **Step 1: Run all new plugin tests**

Run:
```bash
go test -race ./internal/collector/inputs/load/
go test -race ./internal/collector/inputs/diskio/
go test -race ./internal/collector/inputs/temp/
go test -race ./internal/collector/inputs/gpu/
go test -race ./internal/collector/inputs/connections/
```
Expected: All PASS

- [ ] **Step 2: Run full test suite**

Run: `go test -race ./...`
Expected: PASS

- [ ] **Step 3: Run vet**

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 4: Final commit with all tests passing**

```bash
git add -A
git commit -m "test: verify all new input plugins pass tests"
```
