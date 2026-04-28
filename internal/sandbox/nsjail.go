package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// NsjailConfig holds the resource and isolation parameters for an nsjail execution.
type NsjailConfig struct {
	TimeLimit    int      `json:"time_limit"`
	MemoryMB     int      `json:"memory_mb"`
	CPUPercent   int      `json:"cpu_percent"`
	MaxPIDs      int      `json:"max_pids"`
	MaxFileSize  int      `json:"max_file_size"`
	NetworkMode  string   `json:"network_mode"`
	AllowedIPs   []string `json:"allowed_ips"`
	WorkDir      string   `json:"work_dir"`
}

// ToArgs generates the nsjail CLI arguments for the given task.
func (c *NsjailConfig) ToArgs(taskID string) []string {
	args := []string{
		"--mode=ONCE",
		"--really_quiet",
		fmt.Sprintf("--name=sandbox-%s", taskID),
	}

	if c.TimeLimit > 0 {
		args = append(args, fmt.Sprintf("--time_limit=%d", c.TimeLimit))
	}

	// Cgroup limits.
	if c.MemoryMB > 0 {
		args = append(args, fmt.Sprintf("--cgroup_mem_max=%d", c.MemoryMB*1024*1024))
	}
	if c.CPUPercent > 0 {
		args = append(args, fmt.Sprintf("--cgroup_cpu_ms_per_sec=%d", c.CPUPercent*10))
	}
	if c.MaxPIDs > 0 {
		args = append(args, fmt.Sprintf("--cgroup_pids_max=%d", c.MaxPIDs))
	}

	// UID/GID mapping — run as nobody (65534).
	args = append(args,
		"--uid_mapping=0:65534:1",
		"--gid_mapping=0:65534:1",
	)

	// Read-only bind mounts for system directories.
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/etc"} {
		args = append(args, fmt.Sprintf("--bindmount_ro=%s", dir))
	}

	// tmpfs for writable scratch space.
	args = append(args,
		"--tmpfsmount=/tmp:tmpfs:size=67108864",
		"--tmpfsmount=/work:tmpfs:size=134217728",
	)

	// Seccomp policy string — use a basic allowlist.
	args = append(args, "--seccomp_policy_string=ALLOW { }")

	// Network mode.
	switch c.NetworkMode {
	case "allowlist":
		args = append(args, "--disable_clone_newnet")
	case "disabled":
		args = append(args, "")
	default:
		args = append(args, "")
	}

	// Set working directory.
	workDir := c.WorkDir
	if workDir == "" {
		workDir = "/work"
	}
	args = append(args, fmt.Sprintf("--cwd=%s", workDir))

	return args
}

// CommandArgs appends the executable command and arguments to the nsjail args.
func (c *NsjailConfig) CommandArgs(command string, cmdArgs []string) []string {
	args := c.ToArgs(command)
	args = append(args, "--")
	args = append(args, command)
	args = append(args, cmdArgs...)
	return args
}

// ScriptArgs appends the interpreter invocation to the nsjail args.
func (c *NsjailConfig) ScriptArgs(interpreter, scriptContent string) []string {
	interpPath := interpreterToPath(interpreter)
	args := c.ToArgs(interpreter)
	args = append(args, "--")
	args = append(args, interpPath, "-c", scriptContent)
	return args
}

// WriteConfigFile writes a minimal nsjail config file for the given task and returns its path.
func (c *NsjailConfig) WriteConfigFile(taskID string) (string, error) {
	cfgDir := filepath.Join(os.TempDir(), "nsjail-cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return "", fmt.Errorf("create nsjail config dir: %w", err)
	}

	cfgPath := filepath.Join(cfgDir, fmt.Sprintf("task-%s.cfg", taskID))
	content := c.buildConfigContent(taskID)

	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write nsjail config: %w", err)
	}
	return cfgPath, nil
}

// buildConfigContent produces the text of an nsjail protobuf config.
func (c *NsjailConfig) buildConfigContent(taskID string) string {
	s := fmt.Sprintf("name: \"sandbox-%s\"\nmode: ONCE\n", taskID)
	if c.TimeLimit > 0 {
		s += fmt.Sprintf("time_limit: %d\n", c.TimeLimit)
	}
	workDir := c.WorkDir
	if workDir == "" {
		workDir = "/work"
	}
	s += fmt.Sprintf("cwd: %q\n", workDir)

	// Cgroup.
	s += "cgroup_mem_max: " + strconv.Itoa(c.MemoryMB*1024*1024) + "\n"
	s += "cgroup_pids_max: " + strconv.Itoa(c.MaxPIDs) + "\n"

	// Mounts.
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/etc"} {
		s += fmt.Sprintf("mount { src: %q dst: %q is_bind: true rw: false }\n", dir, dir)
	}
	s += "mount { dst: \"/tmp\" fstype: \"tmpfs\" options: \"size=67108864\" rw: true }\n"
	s += "mount { dst: \"/work\" fstype: \"tmpfs\" options: \"size=134217728\" rw: true }\n"

	// UID/GID mapping.
	s += "uidmap { inside_id: 0 outside_id: 65534 count: 1 }\n"
	s += "gidmap { inside_id: 0 outside_id: 65534 count: 1 }\n"

	return s
}

// interpreterToPath maps a short interpreter name to its absolute path.
func interpreterToPath(interpreter string) string {
	switch interpreter {
	case "bash":
		return "/bin/bash"
	case "sh":
		return "/bin/sh"
	case "python3":
		return "/usr/bin/python3"
	case "python":
		return "/usr/bin/python3"
	case "ruby":
		return "/usr/bin/ruby"
	case "node":
		return "/usr/bin/node"
	case "perl":
		return "/usr/bin/perl"
	default:
		return interpreter
	}
}
