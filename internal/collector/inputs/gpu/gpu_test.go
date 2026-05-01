package gpu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cy77cc/opsagent/internal/collector"
)

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
	if !strings.Contains(sc, "nvidia-smi") {
		t.Error("SampleConfig should mention nvidia-smi")
	}
}

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

func TestParseGPUOutputNonNumericFields(t *testing.T) {
	// All numeric columns have non-numeric values ("abc") -- they should be skipped.
	line := "0, NVIDIA GeForce RTX 3090, abc, abc, abc, abc, abc, abc, abc"
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
	// All numeric fields should be skipped due to parse failure.
	numericFields := []string{
		"utilization_gpu", "utilization_memory", "memory_total",
		"memory_used", "temperature", "power_draw", "fan_speed",
	}
	for _, f := range numericFields {
		if _, ok := fields[f]; ok {
			t.Errorf("field %q should be skipped for non-numeric value, but was present", f)
		}
	}
}

func TestParseGPUOutputMixedNAAndNumeric(t *testing.T) {
	line := "1, Tesla V100, 80, [N/A], 16384, [N/A], 72, [N/A], 35"
	tags, fields, err := parseGPUOutput(line)
	if err != nil {
		t.Fatalf("parseGPUOutput() error: %v", err)
	}
	if tags["gpu_index"] != "1" {
		t.Errorf("gpu_index = %q, want %q", tags["gpu_index"], "1")
	}
	if tags["gpu_name"] != "Tesla V100" {
		t.Errorf("gpu_name = %q, want %q", tags["gpu_name"], "Tesla V100")
	}
	// Fields with valid numbers should be present.
	if v, ok := fields["utilization_gpu"]; !ok || v.(float64) != 80 {
		t.Errorf("utilization_gpu = %v, want 80", v)
	}
	if v, ok := fields["memory_total"]; !ok || v.(float64) != 16384 {
		t.Errorf("memory_total = %v, want 16384", v)
	}
	if v, ok := fields["temperature"]; !ok || v.(float64) != 72 {
		t.Errorf("temperature = %v, want 72", v)
	}
	if v, ok := fields["fan_speed"]; !ok || v.(float64) != 35 {
		t.Errorf("fan_speed = %v, want 35", v)
	}
	// N/A fields should be absent.
	if _, ok := fields["utilization_memory"]; ok {
		t.Error("utilization_memory should be skipped for [N/A]")
	}
	if _, ok := fields["memory_used"]; ok {
		t.Error("memory_used should be skipped for [N/A]")
	}
	if _, ok := fields["power_draw"]; ok {
		t.Error("power_draw should be skipped for [N/A]")
	}
}

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

func TestRegisteredInDefaultRegistry(t *testing.T) {
	factory, ok := collector.DefaultRegistry.GetInput("gpu")
	if !ok {
		t.Fatal("gpu input not registered in DefaultRegistry")
	}
	input := factory()
	if input == nil {
		t.Fatal("factory returned nil input")
	}
	if _, ok := input.(*GPUInput); !ok {
		t.Errorf("expected *GPUInput, got %T", input)
	}
}

// createFakeNvidiaSmi writes a shell script that mimics nvidia-smi CSV output.
func createFakeNvidiaSmi(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "nvidia-smi")
	content := "#!/bin/sh\necho '" + output + "'\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0755); err != nil {
		t.Fatalf("failed to create fake nvidia-smi: %v", err)
	}
	return scriptPath
}

func TestGPUInputGatherWithFakeBinary(t *testing.T) {
	csvOutput := "0, NVIDIA GeForce RTX 3090, 45, 30, 24576, 8192, 65, 250, 50\n1, Tesla V100, 80, 50, 16384, 4096, 72, 300, [N/A]"
	binPath := createFakeNvidiaSmi(t, csvOutput)

	input := &GPUInput{
		binPath:   binPath,
		available: true,
		timeout:   5 * time.Second,
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	// Verify first GPU metric.
	m0 := metrics[0]
	if m0.Tags()["gpu_index"] != "0" {
		t.Errorf("first metric gpu_index = %q, want %q", m0.Tags()["gpu_index"], "0")
	}
	if m0.Tags()["gpu_name"] != "NVIDIA GeForce RTX 3090" {
		t.Errorf("first metric gpu_name = %q, want %q", m0.Tags()["gpu_name"], "NVIDIA GeForce RTX 3090")
	}
	if m0.Fields()["utilization_gpu"] != float64(45) {
		t.Errorf("first metric utilization_gpu = %v, want 45", m0.Fields()["utilization_gpu"])
	}

	// Verify second GPU metric.
	m1 := metrics[1]
	if m1.Tags()["gpu_index"] != "1" {
		t.Errorf("second metric gpu_index = %q, want %q", m1.Tags()["gpu_index"], "1")
	}
	if m1.Tags()["gpu_name"] != "Tesla V100" {
		t.Errorf("second metric gpu_name = %q, want %q", m1.Tags()["gpu_name"], "Tesla V100")
	}
	if m1.Fields()["utilization_gpu"] != float64(80) {
		t.Errorf("second metric utilization_gpu = %v, want 80", m1.Fields()["utilization_gpu"])
	}
	// fan_speed should be absent for second GPU (was [N/A]).
	if _, ok := m1.Fields()["fan_speed"]; ok {
		t.Error("second metric fan_speed should be absent (was [N/A])")
	}
}

func TestGPUInputGatherWithFakeBinary_MalformedLineSkipped(t *testing.T) {
	// Include a malformed line (too few fields) -- it should be skipped.
	csvOutput := "0, NVIDIA GeForce RTX 3090, 45, 30, 24576, 8192, 65, 250, 50\nmalformed line\n1, Tesla V100, 80, 50, 16384, 4096, 72, 300, [N/A]"
	binPath := createFakeNvidiaSmi(t, csvOutput)

	input := &GPUInput{
		binPath:   binPath,
		available: true,
		timeout:   5 * time.Second,
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// Only 2 valid lines should produce metrics; malformed line skipped.
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics (malformed line skipped), got %d", len(metrics))
	}
}

func TestGPUInputGatherWithFakeBinary_EmptyOutput(t *testing.T) {
	binPath := createFakeNvidiaSmi(t, "")

	input := &GPUInput{
		binPath:   binPath,
		available: true,
		timeout:   5 * time.Second,
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	if len(metrics) != 0 {
		t.Errorf("expected 0 metrics for empty output, got %d", len(metrics))
	}
}
