package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sanitizeTaskID validates that a task ID does not contain path traversal sequences.
func sanitizeTaskID(taskID string) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("task ID is required")
	}
	cleaned := filepath.Clean(taskID)
	if cleaned == "." || cleaned == ".." || strings.ContainsAny(cleaned, `/\`) {
		return "", fmt.Errorf("invalid task ID: %q contains path traversal", taskID)
	}
	if strings.ContainsRune(cleaned, '\x00') {
		return "", fmt.Errorf("invalid task ID: contains null byte")
	}
	return cleaned, nil
}

// baseSyscalls is the minimal syscall whitelist for sandboxed processes.
// clone, fork, and vfork are deliberately excluded to prevent fork bombs.
const baseSyscalls = `ALLOW {
    read, write, open, close, mmap, munmap, mprotect, brk,
    access, stat, fstat, lstat, ioctl, pread64, pwrite64,
    readv, writev, pipe, dup, dup2, nanosleep, getpid,
    execve, exit, wait4, kill, uname,
    fcntl, flock, fsync, fdatasync, truncate, ftruncate,
    getdents, getcwd, chdir, rename, mkdir, rmdir, link,
    unlink, readlink, chmod, chown, umask, gettimeofday,
    getuid, getgid, geteuid, getegid, getppid, getpgrp,
    set_tid_address, futex, epoll_create, epoll_ctl,
    epoll_wait, clock_gettime, exit_group, set_robust_list,
    openat, mkdirat, newfstatat, unlinkat, renameat,
    readlinkat, faccessat, epoll_create1, pipe2, dup3,
    prlimit64, getrandom, rseq, sigaltstack, rt_sigaction,
    rt_sigprocmask, madvise, getpeername, getsockname,
    timerfd_create, timerfd_settime, timerfd_gettime
}`

// networkSyscalls are appended to the base policy when NetworkMode is "allowlist".
const networkSyscalls = `,
    socket, connect, bind, listen, accept, accept4,
    sendto, recvfrom, sendmsg, recvmsg, shutdown,
    setsockopt, getsockopt, socketpair, eventfd2`

// seccompPolicyString returns the seccomp policy for the given config.
// Network syscalls are only included when NetworkMode is "allowlist".
func (c *NsjailConfig) seccompPolicyString() string {
	if c.NetworkMode == "allowlist" {
		return baseSyscalls[:len(baseSyscalls)-1] + networkSyscalls + "\n}"
	}
	return baseSyscalls
}

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
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return nil
	}
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
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin"} {
		args = append(args, fmt.Sprintf("--bindmount_ro=%s", dir))
	}

	// tmpfs for writable scratch space.
	args = append(args,
		"--tmpfsmount=/etc:tmpfs:size=1048576",
		"--tmpfsmount=/tmp:tmpfs:size=67108864",
		"--tmpfsmount=/work:tmpfs:size=134217728",
	)

	// Seccomp policy string — minimal syscall whitelist.
	args = append(args, fmt.Sprintf("--seccomp_policy_string=%s", c.seccompPolicyString()))

	// Network mode.
	switch c.NetworkMode {
	case "allowlist":
		args = append(args, "--disable_clone_newnet")
	case "disabled":
		// No network access — do not append anything.
	default:
		// No network access — do not append anything.
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
func (c *NsjailConfig) CommandArgs(taskID string, command string, cmdArgs []string) []string {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return nil
	}
	args := c.ToArgs(taskID)
	args = append(args, "--", command)
	args = append(args, cmdArgs...)
	return args
}

// ScriptArgs appends the interpreter invocation to the nsjail args using a script file path.
func (c *NsjailConfig) ScriptArgs(taskID string, interpreter, scriptPath string) ([]string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return nil, err
	}
	interpPath, err := interpreterToPath(interpreter)
	if err != nil {
		return nil, err
	}
	args := c.ToArgs(taskID)
	args = append(args, "--", interpPath, scriptPath)
	return args, nil
}

// WriteScriptFile writes script content to a temporary file and returns its path.
func (c *NsjailConfig) WriteScriptFile(taskID, scriptContent string) (string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	scriptDir := filepath.Join(os.TempDir(), "nsjail-scripts")
	if err := os.MkdirAll(scriptDir, 0o700); err != nil {
		return "", fmt.Errorf("create script dir: %w", err)
	}
	scriptPath := filepath.Join(scriptDir, fmt.Sprintf("task-%s.sh", taskID))
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o600); err != nil {
		return "", fmt.Errorf("write script file: %w", err)
	}
	return scriptPath, nil
}

// WriteConfigFile writes a minimal nsjail config file for the given task and returns its path.
func (c *NsjailConfig) WriteConfigFile(taskID string) (string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
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
	for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin"} {
		s += fmt.Sprintf("mount { src: %q dst: %q is_bind: true rw: false }\n", dir, dir)
	}
	s += "mount { dst: \"/etc\" fstype: \"tmpfs\" options: \"size=1048576\" rw: true }\n"
	s += "mount { dst: \"/tmp\" fstype: \"tmpfs\" options: \"size=67108864\" rw: true }\n"
	s += "mount { dst: \"/work\" fstype: \"tmpfs\" options: \"size=134217728\" rw: true }\n"

	// UID/GID mapping.
	s += "uidmap { inside_id: 0 outside_id: 65534 count: 1 }\n"
	s += "gidmap { inside_id: 0 outside_id: 65534 count: 1 }\n"

	return s
}

// interpreterToPath maps a short interpreter name to its absolute path.
// Returns an error for unknown interpreters to prevent arbitrary binary execution.
func interpreterToPath(interpreter string) (string, error) {
	switch interpreter {
	case "bash":
		return "/bin/bash", nil
	case "sh":
		return "/bin/sh", nil
	case "python3":
		return "/usr/bin/python3", nil
	case "python":
		return "/usr/bin/python3", nil
	case "ruby":
		return "/usr/bin/ruby", nil
	case "node":
		return "/usr/bin/node", nil
	case "perl":
		return "/usr/bin/perl", nil
	default:
		return "", fmt.Errorf("unsupported interpreter %q", interpreter)
	}
}
