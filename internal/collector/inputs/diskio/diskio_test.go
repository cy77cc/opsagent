package diskio

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

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
