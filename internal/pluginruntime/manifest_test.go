package pluginruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseManifest_Valid(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
description: "A test plugin"
author: "test@example.com"
runtime: process
binary_path: ./my-plugin
task_types:
  - audit
  - report
limits:
  max_memory_mb: 256
  timeout_seconds: 30
`
	manifest, err := ParseManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Name != "test-plugin" {
		t.Errorf("name = %q, want %q", manifest.Name, "test-plugin")
	}
	if manifest.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", manifest.Version, "1.0.0")
	}
	if len(manifest.TaskTypes) != 2 {
		t.Errorf("task_types len = %d, want 2", len(manifest.TaskTypes))
	}
	if manifest.Limits == nil {
		t.Fatal("limits should not be nil")
	}
	if manifest.Limits.MaxMemoryMB != 256 {
		t.Errorf("max_memory_mb = %d, want 256", manifest.Limits.MaxMemoryMB)
	}
}

func TestParseManifest_MissingName(t *testing.T) {
	yaml := `
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestParseManifest_MissingBinaryPath(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing binary_path")
	}
}

func TestParseManifest_MissingTaskTypes(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing task_types")
	}
}

func TestParseManifest_InvalidVersion(t *testing.T) {
	yaml := `
name: test-plugin
version: "not-semver"
binary_path: ./my-plugin
task_types:
  - audit
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestParseManifest_Defaults(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	manifest, err := ParseManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Runtime != "process" {
		t.Errorf("default runtime = %q, want %q", manifest.Runtime, "process")
	}
}

func TestValidateManifest_OS(t *testing.T) {
	yaml := `
name: test-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
requirements:
  os:
    - darwin
`
	_, err := ParseManifest([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
}

func TestLoadManifestFromFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
name: file-plugin
version: "1.0.0"
binary_path: ./my-plugin
task_types:
  - audit
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if manifest.Name != "file-plugin" {
		t.Errorf("name = %q, want %q", manifest.Name, "file-plugin")
	}
	if manifest.BinaryPath != filepath.Join(dir, "my-plugin") {
		t.Errorf("binary_path = %q, want %q", manifest.BinaryPath, filepath.Join(dir, "my-plugin"))
	}
}

func TestFullTaskType(t *testing.T) {
	tests := []struct {
		plugin   string
		taskType string
		want     string
	}{
		{"my-plugin", "audit", "my-plugin/audit"},
		{"my-plugin", "report", "my-plugin/report"},
	}
	for _, tt := range tests {
		got := FullTaskType(tt.plugin, tt.taskType)
		if got != tt.want {
			t.Errorf("FullTaskType(%q, %q) = %q, want %q", tt.plugin, tt.taskType, got, tt.want)
		}
	}
}
