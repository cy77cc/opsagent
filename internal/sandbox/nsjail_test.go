package sandbox

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestNsjailConfigGenerate(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit:   30,
		MemoryMB:    128,
		CPUPercent:  50,
		MaxPIDs:     32,
		NetworkMode: "disabled",
		WorkDir:     "/work",
	}

	args := cfg.ToArgs("task-001")
	argStr := strings.Join(args, " ")

	// Basic mode.
	if !strings.Contains(argStr, "--mode=ONCE") {
		t.Error("expected --mode=ONCE")
	}
	if !strings.Contains(argStr, "--time_limit=30") {
		t.Error("expected --time_limit=30")
	}
	// Cgroup limits.
	if !strings.Contains(argStr, "--cgroup_mem_max=") {
		t.Error("expected cgroup_mem_max")
	}
	if !strings.Contains(argStr, "--cgroup_cpu_ms_per_sec=500") {
		t.Error("expected --cgroup_cpu_ms_per_sec=500 for 50%")
	}
	if !strings.Contains(argStr, "--cgroup_pids_max=32") {
		t.Error("expected --cgroup_pids_max=32")
	}
	// UID/GID.
	if !strings.Contains(argStr, "--uid_mapping=0:65534:1") {
		t.Error("expected uid_mapping")
	}
	if !strings.Contains(argStr, "--gid_mapping=0:65534:1") {
		t.Error("expected gid_mapping")
	}
	// Bind mounts.
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/etc"} {
		if !strings.Contains(argStr, "--bindmount_ro="+dir) {
			t.Errorf("expected bindmount_ro=%s", dir)
		}
	}
	// tmpfs.
	if !strings.Contains(argStr, "--tmpfsmount=/tmp") {
		t.Error("expected tmpfsmount=/tmp")
	}
	if !strings.Contains(argStr, "--tmpfsmount=/work") {
		t.Error("expected tmpfsmount=/work")
	}
	// Seccomp.
	if !strings.Contains(argStr, "--seccomp_policy_string=") {
		t.Error("expected seccomp_policy_string")
	}
	// CWD.
	if !strings.Contains(argStr, "--cwd=/work") {
		t.Error("expected --cwd=/work")
	}
}

func TestNsjailConfigCommand(t *testing.T) {
	cfg := NsjailConfig{TimeLimit: 10, MemoryMB: 64, WorkDir: "/work"}
	args := cfg.CommandArgs("task-001", "echo", []string{"hello", "world"})
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "-- echo hello world") {
		t.Errorf("expected '-- echo hello world' in args, got: %s", argStr)
	}
	if !strings.Contains(argStr, "--name=sandbox-task-001") {
		t.Errorf("expected '--name=sandbox-task-001' in args, got: %s", argStr)
	}
}

func TestNsjailConfigScript(t *testing.T) {
	cfg := NsjailConfig{TimeLimit: 10, MemoryMB: 64, WorkDir: "/work"}
	args := cfg.ScriptArgs("task-002", "bash", "/tmp/script.sh")
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "/bin/bash /tmp/script.sh") {
		t.Errorf("expected '/bin/bash /tmp/script.sh', got: %s", argStr)
	}
	if !strings.Contains(argStr, "--name=sandbox-task-002") {
		t.Errorf("expected '--name=sandbox-task-002' in args, got: %s", argStr)
	}
}

func TestBuildConfigContentBasic(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit:   30,
		MemoryMB:    128,
		MaxPIDs:     32,
		NetworkMode: "disabled",
		WorkDir:     "/work",
	}

	content := cfg.buildConfigContent("task-001")

	// Name and mode.
	if !strings.Contains(content, `name: "sandbox-task-001"`) {
		t.Error("expected name field in config content")
	}
	if !strings.Contains(content, "mode: ONCE") {
		t.Error("expected mode: ONCE in config content")
	}

	// Time limit.
	if !strings.Contains(content, "time_limit: 30") {
		t.Error("expected time_limit: 30")
	}

	// Working directory.
	if !strings.Contains(content, `cwd: "/work"`) {
		t.Error("expected cwd: /work")
	}

	// Cgroup limits.
	if !strings.Contains(content, "cgroup_mem_max: 134217728") {
		t.Errorf("expected cgroup_mem_max for 128MB (134217728), got content:\n%s", content)
	}
	if !strings.Contains(content, "cgroup_pids_max: 32") {
		t.Error("expected cgroup_pids_max: 32")
	}

	// Read-only bind mounts.
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/etc"} {
		expected := fmt.Sprintf(`src: %q dst: %q is_bind: true rw: false`, dir, dir)
		if !strings.Contains(content, expected) {
			t.Errorf("expected bindmount_ro for %s", dir)
		}
	}

	// tmpfs mounts.
	if !strings.Contains(content, `dst: "/tmp"`) {
		t.Error("expected tmpfs mount for /tmp")
	}
	if !strings.Contains(content, `dst: "/work"`) {
		t.Error("expected tmpfs mount for /work")
	}

	// UID/GID mapping.
	if !strings.Contains(content, "uidmap { inside_id: 0 outside_id: 65534 count: 1 }") {
		t.Error("expected uidmap in config content")
	}
	if !strings.Contains(content, "gidmap { inside_id: 0 outside_id: 65534 count: 1 }") {
		t.Error("expected gidmap in config content")
	}
}

func TestBuildConfigContentDefaultWorkDir(t *testing.T) {
	cfg := NsjailConfig{
		MemoryMB: 64,
		MaxPIDs:  16,
		// WorkDir intentionally empty — should default to /work.
	}

	content := cfg.buildConfigContent("task-default")

	if !strings.Contains(content, `cwd: "/work"`) {
		t.Errorf("expected default cwd /work when WorkDir is empty, got:\n%s", content)
	}
}

func TestBuildConfigContentNoTimeLimit(t *testing.T) {
	cfg := NsjailConfig{
		MemoryMB: 64,
		MaxPIDs:  16,
		WorkDir:  "/work",
		// TimeLimit is 0 — should not appear.
	}

	content := cfg.buildConfigContent("task-notime")

	if strings.Contains(content, "time_limit") {
		t.Errorf("expected no time_limit when TimeLimit is 0, got:\n%s", content)
	}
}

func TestBuildConfigContentZeroMemory(t *testing.T) {
	cfg := NsjailConfig{
		MaxPIDs: 16,
		WorkDir: "/work",
		// MemoryMB is 0.
	}

	content := cfg.buildConfigContent("task-mem0")

	// cgroup_mem_max should still appear (value 0), since the code unconditionally writes it.
	if !strings.Contains(content, "cgroup_mem_max: 0") {
		t.Errorf("expected cgroup_mem_max: 0 when MemoryMB is 0, got:\n%s", content)
	}
}

func TestBuildConfigContentWriteConfigFile(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit:  15,
		MemoryMB:   256,
		MaxPIDs:    64,
		WorkDir:    "/work",
	}

	path, err := cfg.WriteConfigFile("task-cfg-001")
	if err != nil {
		t.Fatalf("WriteConfigFile() error: %v", err)
	}
	defer os.Remove(path)

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}

	s := string(content)
	if !strings.Contains(s, `name: "sandbox-task-cfg-001"`) {
		t.Error("expected task name in written config file")
	}
	if !strings.Contains(s, "time_limit: 15") {
		t.Error("expected time_limit in written config file")
	}
	if !strings.Contains(s, "cgroup_mem_max: 268435456") {
		t.Errorf("expected cgroup_mem_max for 256MB, got:\n%s", s)
	}

	// Verify permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestInterpreterToPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"bash", "/bin/bash"},
		{"sh", "/bin/sh"},
		{"python3", "/usr/bin/python3"},
		{"python", "/usr/bin/python3"},
		{"node", "/usr/bin/node"},
		{"ruby", "/usr/bin/ruby"},
		{"perl", "/usr/bin/perl"},
		{"custom-interp", "custom-interp"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := interpreterToPath(tc.input)
			if got != tc.want {
				t.Errorf("interpreterToPath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
