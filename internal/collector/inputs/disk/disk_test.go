package disk

import (
	"context"
	"testing"

	"github.com/cy77cc/opsagent/internal/collector"
)

func TestDiskInputInit(t *testing.T) {
	input := &DiskInput{}
	if err := input.Init(map[string]interface{}{}); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.mountPoints) != 0 {
		t.Errorf("mountPoints should be empty by default, got %v", input.mountPoints)
	}
}

func TestDiskInputInitWithMountPoints(t *testing.T) {
	input := &DiskInput{}
	cfg := map[string]interface{}{
		"mount_points": []interface{}{"/", "/home"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if len(input.mountPoints) != 2 {
		t.Errorf("mountPoints len = %d, want 2", len(input.mountPoints))
	}
}

func TestDiskInputInitInvalidMountPoints(t *testing.T) {
	input := &DiskInput{}
	cfg := map[string]interface{}{
		"mount_points": "notalist",
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with invalid mount_points type")
	}
}

func TestDiskInputInitInvalidMountPointItem(t *testing.T) {
	input := &DiskInput{}
	cfg := map[string]interface{}{
		"mount_points": []interface{}{123},
	}
	if err := input.Init(cfg); err == nil {
		t.Fatal("Init() should fail with non-string mount_point item")
	}
}

func TestDiskInputGather(t *testing.T) {
	input := &DiskInput{}
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

	// Find a disk metric
	var diskMetric *collector.Metric
	for _, m := range metrics {
		if m.Name() == "disk" {
			diskMetric = m
			break
		}
	}
	if diskMetric == nil {
		t.Fatal("expected 'disk' metric")
	}

	// Verify tags exist
	tags := diskMetric.Tags()
	if tags["mountpoint"] == "" {
		t.Error("missing 'mountpoint' tag")
	}
	if tags["device"] == "" {
		t.Error("missing 'device' tag")
	}
	if tags["fstype"] == "" {
		t.Error("missing 'fstype' tag")
	}

	// Verify fields
	expectedFields := []string{"total_bytes", "used_bytes", "free_bytes", "used_percent"}
	fields := diskMetric.Fields()
	for _, f := range expectedFields {
		if _, ok := fields[f]; !ok {
			t.Errorf("missing field %q in disk metric", f)
		}
	}

	// Verify types
	if _, ok := fields["total_bytes"].(int64); !ok {
		t.Errorf("total_bytes should be int64, got %T", fields["total_bytes"])
	}
	if _, ok := fields["used_bytes"].(int64); !ok {
		t.Errorf("used_bytes should be int64, got %T", fields["used_bytes"])
	}
	if _, ok := fields["free_bytes"].(int64); !ok {
		t.Errorf("free_bytes should be int64, got %T", fields["free_bytes"])
	}
	if _, ok := fields["used_percent"].(float64); !ok {
		t.Errorf("used_percent should be float64, got %T", fields["used_percent"])
	}
}

func TestDiskInputGatherFilterMountPoints(t *testing.T) {
	input := &DiskInput{}
	// Use a mount point that exists on Linux
	cfg := map[string]interface{}{
		"mount_points": []interface{}{"/"},
	}
	if err := input.Init(cfg); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	acc := collector.NewAccumulator(100)
	if err := input.Gather(context.Background(), acc); err != nil {
		t.Fatalf("Gather() error: %v", err)
	}

	metrics := acc.Collect()
	// Should have exactly 1 metric for "/" (or 0 if not mounted)
	for _, m := range metrics {
		if m.Name() == "disk" {
			tags := m.Tags()
			if tags["mountpoint"] != "/" {
				t.Errorf("unexpected mountpoint: %q, expected '/'", tags["mountpoint"])
			}
		}
	}
}

func TestDiskInputSampleConfig(t *testing.T) {
	input := &DiskInput{}
	sc := input.SampleConfig()
	if sc == "" {
		t.Error("SampleConfig() should not be empty")
	}
}
