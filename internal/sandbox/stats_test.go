package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCgroupStats(t *testing.T) {
	dir := t.TempDir()

	// Write mock cgroup v2 files.
	os.WriteFile(filepath.Join(dir, "memory.peak"), []byte("1048576\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("usage_usec 500000\nuser_usec 300000\nsystem_usec 200000\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pids.current"), []byte("5\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "io.stat"), []byte("8:0 rbytes 1024 wbytes 2048\n8:1 rbytes 512 wbytes 256\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.PeakMemoryBytes != 1048576 {
		t.Errorf("PeakMemoryBytes = %d, want 1048576", stats.PeakMemoryBytes)
	}
	if stats.CPUTimeUserMs != 300 {
		t.Errorf("CPUTimeUserMs = %d, want 300", stats.CPUTimeUserMs)
	}
	if stats.CPUTimeSystemMs != 200 {
		t.Errorf("CPUTimeSystemMs = %d, want 200", stats.CPUTimeSystemMs)
	}
	if stats.ProcessCount != 5 {
		t.Errorf("ProcessCount = %d, want 5", stats.ProcessCount)
	}
	if stats.BytesRead != 1536 {
		t.Errorf("BytesRead = %d, want 1536 (1024+512)", stats.BytesRead)
	}
	if stats.BytesWritten != 2304 {
		t.Errorf("BytesWritten = %d, want 2304 (2048+256)", stats.BytesWritten)
	}
}

func TestReadCgroupStats_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("expected no error for missing files, got: %v", err)
	}
	if stats.PeakMemoryBytes != 0 {
		t.Errorf("expected 0 PeakMemoryBytes, got %d", stats.PeakMemoryBytes)
	}
}

func TestCreateAndRemoveCgroup(t *testing.T) {
	dir := t.TempDir()

	cgPath, err := CreateCgroup(dir, "test-task-001")
	if err != nil {
		t.Fatalf("CreateCgroup: %v", err)
	}

	if _, err := os.Stat(cgPath); os.IsNotExist(err) {
		t.Fatal("expected cgroup directory to exist")
	}

	if err := RemoveCgroup(cgPath); err != nil {
		t.Fatalf("RemoveCgroup: %v", err)
	}

	if _, err := os.Stat(cgPath); !os.IsNotExist(err) {
		t.Fatal("expected cgroup directory to be removed")
	}
}

func TestSetCgroupLimits(t *testing.T) {
	dir := t.TempDir()
	cgPath := filepath.Join(dir, "test-cg")
	os.MkdirAll(cgPath, 0o755)

	if err := SetCgroupLimits(cgPath, 128, 50, 32); err != nil {
		t.Fatalf("SetCgroupLimits: %v", err)
	}

	// Verify memory.max
	data, _ := os.ReadFile(filepath.Join(cgPath, "memory.max"))
	if string(data) != "134217728" {
		t.Errorf("memory.max = %q, want 134217728", string(data))
	}

	// Verify cpu.max
	data, _ = os.ReadFile(filepath.Join(cgPath, "cpu.max"))
	if string(data) != "50000 100000" {
		t.Errorf("cpu.max = %q, want '50000 100000'", string(data))
	}

	// Verify pids.max
	data, _ = os.ReadFile(filepath.Join(cgPath, "pids.max"))
	if string(data) != "32" {
		t.Errorf("pids.max = %q, want 32", string(data))
	}
}
