package sandbox

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSetCgroupLimits_MemoryError(t *testing.T) {
	// Non-existent directory should cause WriteFile to fail.
	err := SetCgroupLimits("/nonexistent/path", 128, 0, 0)
	if err == nil {
		t.Fatal("expected error when writing to nonexistent path")
	}
	if !strings.Contains(err.Error(), "set memory.max") {
		t.Errorf("expected error about memory.max, got: %v", err)
	}
}

func TestSetCgroupLimits_CPUError(t *testing.T) {
	dir := t.TempDir()
	// Use a regular file as the "cgroup path" -- WriteFile will fail because
	// it can't create a file inside a non-directory.
	blocker := filepath.Join(dir, "not-a-dir")
	os.WriteFile(blocker, []byte("blocker"), 0o644)

	err := SetCgroupLimits(blocker, 0, 50, 0)
	if err == nil {
		t.Fatal("expected error when writing cpu.max to a non-directory path")
	}
	if !strings.Contains(err.Error(), "set cpu.max") {
		t.Errorf("expected error about cpu.max, got: %v", err)
	}
}

func TestSetCgroupLimits_PIDsError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-dir")
	os.WriteFile(blocker, []byte("blocker"), 0o644)

	err := SetCgroupLimits(blocker, 0, 0, 32)
	if err == nil {
		t.Fatal("expected error when writing pids.max to a non-directory path")
	}
	if !strings.Contains(err.Error(), "set pids.max") {
		t.Errorf("expected error about pids.max, got: %v", err)
	}
}

func TestSetCgroupLimits_AllZero(t *testing.T) {
	dir := t.TempDir()
	cgPath := filepath.Join(dir, "zero-limits")
	os.MkdirAll(cgPath, 0o755)

	// All limits zero means no files written, should succeed.
	if err := SetCgroupLimits(cgPath, 0, 0, 0); err != nil {
		t.Fatalf("expected no error for zero limits, got: %v", err)
	}
}

func TestKillCgroupProcesses(t *testing.T) {
	dir := t.TempDir()
	cgPath := filepath.Join(dir, "test-kill")
	os.MkdirAll(cgPath, 0o755)

	if err := KillCgroupProcesses(cgPath); err != nil {
		t.Fatalf("KillCgroupProcesses: %v", err)
	}

	// Verify "1" was written to cgroup.kill.
	data, err := os.ReadFile(filepath.Join(cgPath, "cgroup.kill"))
	if err != nil {
		t.Fatalf("failed to read cgroup.kill: %v", err)
	}
	if string(data) != "1" {
		t.Errorf("cgroup.kill = %q, want %q", string(data), "1")
	}
}

func TestKillCgroupProcesses_Error(t *testing.T) {
	// Non-existent directory should cause an error.
	err := KillCgroupProcesses("/nonexistent/cgroup/path")
	if err == nil {
		t.Fatal("expected error when cgroup.kill path does not exist")
	}
	if !strings.Contains(err.Error(), "kill cgroup processes") {
		t.Errorf("expected 'kill cgroup processes' in error, got: %v", err)
	}
}

func TestReadCgroupStats_MalformedCPUStat(t *testing.T) {
	dir := t.TempDir()

	// cpu.stat with non-numeric values.
	os.WriteFile(filepath.Join(dir, "memory.peak"), []byte("1024\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("user_usec notanumber\nsystem_usec 200000\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "pids.current"), []byte("3\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// user_usec is non-numeric, so should be skipped (0).
	if stats.CPUTimeUserMs != 0 {
		t.Errorf("CPUTimeUserMs = %d, want 0 (non-numeric)", stats.CPUTimeUserMs)
	}
	// system_usec is valid: 200000 usec = 200 ms.
	if stats.CPUTimeSystemMs != 200 {
		t.Errorf("CPUTimeSystemMs = %d, want 200", stats.CPUTimeSystemMs)
	}
}

func TestReadCgroupStats_CPUStatWrongFieldCount(t *testing.T) {
	dir := t.TempDir()

	// cpu.stat lines with wrong number of fields (1 or 3 fields).
	os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("user_usec\nsingle_field\nsystem_usec 200000 extra\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Lines with != 2 fields are skipped.
	if stats.CPUTimeUserMs != 0 {
		t.Errorf("CPUTimeUserMs = %d, want 0 (wrong field count)", stats.CPUTimeUserMs)
	}
	if stats.CPUTimeSystemMs != 0 {
		t.Errorf("CPUTimeSystemMs = %d, want 0 (wrong field count)", stats.CPUTimeSystemMs)
	}
}

func TestReadCgroupStats_MalformedIOStat(t *testing.T) {
	dir := t.TempDir()

	// io.stat with non-numeric values for rbytes/wbytes.
	os.WriteFile(filepath.Join(dir, "io.stat"), []byte("8:0 rbytes abc wbytes def\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Non-numeric rbytes/wbytes should be skipped.
	if stats.BytesRead != 0 {
		t.Errorf("BytesRead = %d, want 0 (non-numeric)", stats.BytesRead)
	}
	if stats.BytesWritten != 0 {
		t.Errorf("BytesWritten = %d, want 0 (non-numeric)", stats.BytesWritten)
	}
}

func TestReadCgroupStats_MalformedMemoryPeak(t *testing.T) {
	dir := t.TempDir()

	// Non-numeric memory.peak.
	os.WriteFile(filepath.Join(dir, "memory.peak"), []byte("not_a_number\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// readSingleInt fails, so PeakMemoryBytes stays 0.
	if stats.PeakMemoryBytes != 0 {
		t.Errorf("PeakMemoryBytes = %d, want 0 (non-numeric)", stats.PeakMemoryBytes)
	}
}

func TestReadCgroupStats_MalformedPidsCurrent(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "pids.current"), []byte("abc\n"), 0o644)

	stats, err := ReadCgroupStats(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if stats.ProcessCount != 0 {
		t.Errorf("ProcessCount = %d, want 0 (non-numeric)", stats.ProcessCount)
	}
}

func TestCreateCgroup_BasePathError(t *testing.T) {
	dir := t.TempDir()
	// Place a regular file where MkdirAll expects to create a subdirectory.
	// On Linux as root, MkdirAll on "/nonexistent" still succeeds because
	// root can create anywhere. Instead, put a file in the way.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("not a dir"), 0o644)

	// CreateCgroup tries to mkdir blocker/sandbox-task-err, which fails
	// because blocker is a file.
	_, err := CreateCgroup(blocker, "task-err")
	if err == nil {
		t.Fatal("expected error when base path is a file")
	}
	if !strings.Contains(err.Error(), "create cgroup") {
		t.Errorf("expected 'create cgroup' in error, got: %v", err)
	}
}

func TestRemoveCgroup_Error(t *testing.T) {
	// RemoveAll on a path with a read-only parent can fail,
	// but actually RemoveAll doesn't fail easily. Let's test
	// with a path that contains a file where a directory is expected.
	dir := t.TempDir()
	// Create a file where we expect a directory in the path.
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("data"), 0o644)

	// This should succeed since RemoveAll handles files fine.
	err := RemoveCgroup(blocker)
	if err != nil {
		t.Fatalf("RemoveCgroup should handle files, got: %v", err)
	}
}
