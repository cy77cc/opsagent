package gpu

import (
	"context"
	"testing"

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
