package sandbox

import (
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
	args := cfg.ScriptArgs("task-002", "bash", "echo hello")
	argStr := strings.Join(args, " ")

	if !strings.Contains(argStr, "/bin/bash -c echo hello") {
		t.Errorf("expected '/bin/bash -c echo hello', got: %s", argStr)
	}
	if !strings.Contains(argStr, "--name=sandbox-task-002") {
		t.Errorf("expected '--name=sandbox-task-002' in args, got: %s", argStr)
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
