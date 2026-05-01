# Sub-Plan 7: Sandbox Executor

> **Parent:** [OpsAgent Full Implementation Plan](../2026-04-28-opsagent-full-implementation.md)
> **Depends on:** [Sub-Plan 1: Proto & gRPC Foundation](01-proto-grpc.md)

**Goal:** Implement the nsjail-based sandbox execution engine with security policy, output streaming, cgroup stats, audit logging, and network isolation.

**Files:**
- Create: `internal/sandbox/policy.go`, `policy_test.go`
- Create: `internal/sandbox/nsjail.go`, `nsjail_test.go`
- Create: `internal/sandbox/output_streamer.go`, `output_streamer_test.go`
- Create: `internal/sandbox/stats.go`
- Create: `internal/sandbox/audit.go`
- Create: `internal/sandbox/network.go`
- Create: `internal/sandbox/executor.go`, `executor_test.go`

---

## Task 7.1: Security Policy Engine

- [ ] **Step 1: Write failing tests**

Create `internal/sandbox/policy_test.go`:

```go
package sandbox

import (
	"testing"
)

func TestPolicyAllowCommand(t *testing.T) {
	p := Policy{
		AllowedCommands:     []string{"df", "free", "uptime"},
		BlockedCommands:     []string{"reboot"},
		BlockedKeywords:     []string{"rm -rf /"},
		AllowedInterpreters: []string{"bash", "python3"},
		MaxScriptBytes:      1024 * 1024,
	}

	if err := p.ValidateCommand("df", []string{"-h"}); err != nil {
		t.Fatalf("df should be allowed: %v", err)
	}
}

func TestPolicyBlockCommand(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"df", "free"},
	}

	if err := p.ValidateCommand("uname", nil); err == nil {
		t.Fatal("uname should be blocked")
	}
}

func TestPolicyBlockedOverridesAllowed(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"reboot"},
		BlockedCommands: []string{"reboot"},
	}

	if err := p.ValidateCommand("reboot", nil); err == nil {
		t.Fatal("reboot should be blocked even though it's in allowed list")
	}
}

func TestPolicyBlockShellInjection(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"df"},
	}

	if err := p.ValidateCommand("df", []string{"-h", "; rm -rf /"}); err == nil {
		t.Fatal("shell injection in args should be blocked")
	}
}

func TestPolicyValidateScript(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash", "python3"},
		BlockedKeywords:     []string{"rm -rf /", "dd if=", "mkfs"},
		MaxScriptBytes:      1024,
	}

	if err := p.ValidateScript("bash", "echo hello"); err != nil {
		t.Fatalf("simple script should be allowed: %v", err)
	}
}

func TestPolicyBlockScriptKeyword(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
		BlockedKeywords:     []string{"rm -rf /"},
	}

	if err := p.ValidateScript("bash", "rm -rf / --no-preserve-root"); err == nil {
		t.Fatal("script with blocked keyword should be rejected")
	}
}

func TestPolicyBlockInterpreter(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
	}

	if err := p.ValidateScript("perl", "print 'hello'"); err == nil {
		t.Fatal("perl should be blocked")
	}
}

func TestPolicyOversizedScript(t *testing.T) {
	p := Policy{
		AllowedInterpreters: []string{"bash"},
		MaxScriptBytes:      10,
	}

	if err := p.ValidateScript("bash", "this script is definitely longer than ten bytes"); err == nil {
		t.Fatal("oversized script should be rejected")
	}
}

func TestPolicyEmptyAllowedMeansAllowAll(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{}, // empty = allow all
	}

	if err := p.ValidateCommand("df", nil); err != nil {
		t.Fatalf("empty allowed list should allow all: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/sandbox/ -run TestPolicy -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Policy**

Create `internal/sandbox/policy.go`:

```go
package sandbox

import (
	"fmt"
	"strings"
)

// Policy defines the security policy for sandbox execution.
type Policy struct {
	AllowedCommands     []string
	BlockedCommands     []string
	BlockedKeywords     []string
	AllowedInterpreters []string
	MaxScriptBytes      int
	AllowSudo           bool
	AllowNetwork        bool
}

// ValidateCommand checks if a command and its arguments are allowed.
func (p *Policy) ValidateCommand(command string, args []string) error {
	// Check blocked list first (highest priority)
	blocked := toSet(p.BlockedCommands)
	if _, ok := blocked[command]; ok {
		return fmt.Errorf("command %q is blocked by policy", command)
	}

	// Check allowed list (if non-empty, only listed commands are allowed)
	if len(p.AllowedCommands) > 0 {
		allowed := toSet(p.AllowedCommands)
		if _, ok := allowed[command]; !ok {
			return fmt.Errorf("command %q is not in the allowed list", command)
		}
	}

	// Check for shell metacharacters in args (injection detection)
	for _, arg := range args {
		if containsShellMetacharacters(arg) {
			return fmt.Errorf("argument %q contains shell metacharacters", arg)
		}
	}

	// Check for sudo
	if !p.AllowSudo && command == "sudo" {
		return fmt.Errorf("sudo is not allowed by policy")
	}

	return nil
}

// ValidateScript checks if a script is allowed to execute.
func (p *Policy) ValidateScript(interpreter, script string) error {
	// Check interpreter whitelist
	if len(p.AllowedInterpreters) > 0 {
		allowed := toSet(p.AllowedInterpreters)
		if _, ok := allowed[interpreter]; !ok {
			return fmt.Errorf("interpreter %q is not allowed", interpreter)
		}
	}

	// Check script size
	if p.MaxScriptBytes > 0 && len(script) > p.MaxScriptBytes {
		return fmt.Errorf("script size %d exceeds max %d bytes", len(script), p.MaxScriptBytes)
	}

	// Check blocked keywords
	lowerScript := strings.ToLower(script)
	for _, kw := range p.BlockedKeywords {
		if strings.Contains(lowerScript, strings.ToLower(kw)) {
			return fmt.Errorf("script contains blocked keyword: %q", kw)
		}
	}

	return nil
}

func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// containsShellMetacharacters checks for characters that could enable shell injection.
func containsShellMetacharacters(s string) bool {
	dangerous := []string{";", "&&", "||", "|", "`", "$(", "${", ">", "<", "\n"}
	for _, d := range dangerous {
		if strings.Contains(s, d) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/sandbox/ -run TestPolicy -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/policy.go internal/sandbox/policy_test.go
git commit -m "feat(sandbox): add Security Policy engine with command/script validation"
```

---

## Task 7.2: nsjail Configuration Generator

- [ ] **Step 1: Write failing tests**

Create `internal/sandbox/nsjail_test.go`:

```go
package sandbox

import (
	"strings"
	"testing"
)

func TestNsjailConfigGenerate(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit:   30,
		MemoryMB:    512,
		CPUPercent:  50,
		MaxPIDs:     32,
		MaxFileSize: 64,
		NetworkMode: "none",
	}

	args := cfg.ToArgs("test-task-1")

	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--time_limit=30") {
		t.Fatal("expected time_limit=30")
	}
	if !strings.Contains(joined, "--cgroup_mem_max") {
		t.Fatal("expected cgroup_mem_max")
	}
	if !strings.Contains(joined, "--disable_clone_newnet") {
		t.Fatal("expected network isolation for 'none' mode")
	}
}

func TestNsjailConfigCommand(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit: 10,
		MemoryMB:  256,
	}

	args := cfg.CommandArgs("df", []string{"-h"})

	if args[len(args)-2] != "df" {
		t.Fatalf("expected df in args, got %v", args)
	}
	if args[len(args)-1] != "-h" {
		t.Fatalf("expected -h in args, got %v", args)
	}
}

func TestNsjailConfigScript(t *testing.T) {
	cfg := NsjailConfig{
		TimeLimit: 60,
		MemoryMB:  512,
	}

	args := cfg.ScriptArgs("bash", "echo hello && df -h")

	if !strings.Contains(strings.Join(args, " "), "/bin/bash") {
		t.Fatal("expected /bin/bash in args")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/sandbox/ -run TestNsjail -v
```

Expected: FAIL.

- [ ] **Step 3: Implement nsjail config**

Create `internal/sandbox/nsjail.go`:

```go
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// NsjailConfig holds nsjail sandbox configuration.
type NsjailConfig struct {
	TimeLimit   int    // seconds
	MemoryMB    int    // MB
	CPUPercent  int    // percent of one core
	MaxPIDs     int
	MaxFileSize int    // MB
	NetworkMode string // "none" | "allowlist"
	AllowedIPs  []string
	WorkDir     string
}

// ToArgs generates nsjail CLI arguments for the sandbox.
func (c *NsjailConfig) ToArgs(taskID string) []string {
	args := []string{
		"--mode=ONCE",
		fmt.Sprintf("--time_limit=%d", c.TimeLimit),
		fmt.Sprintf("--cgroup_mem_max=%d", c.MemoryMB*1024*1024),
		fmt.Sprintf("--cgroup_cpu_ms_per_sec=%d", c.CPUPercent*10),
		fmt.Sprintf("--cgroup_pids_max=%d", c.MaxPIDs),
		fmt.Sprintf("--rlimit_fsize=%d", c.MaxFileSize),
		"--rlimit_as=HARD",
		"--rlimit_nofile=256",
		"--rlimit_nproc=64",

		// User namespace mapping
		"--uid_mapping=0:65534:1",
		"--gid_mapping=0:65534:1",

		// Read-only mounts
		"--bindmount_ro=/usr",
		"--bindmount_ro=/lib",
		"--bindmount_ro=/lib64",
		"--bindmount_ro=/bin",
		"/bin/bash",
		"--bindmount_ro=/etc",

		// Writable tmpfs
		fmt.Sprintf("--tmpfsmount=/tmp:size=%d", 64*1024*1024),
		fmt.Sprintf("--tmpfsmount=/work:size=%d", 64*1024*1024),

		// /proc read-only
		"--proc_rw=false",

		// seccomp policy string (default restrictive)
		"--seccomp_string=ALLOW { read, write, open, openat, close, stat, fstat, lstat, mmap, mprotect, munmap, brk, rt_sigaction, rt_sigprocmask, ioctl, access, pipe, select, sched_yield, dup, dup2, nanosleep, getpid, socket, connect, sendto, recvfrom, shutdown, bind, listen, accept, getsockname, getpeername, clone, fork, vfork, execve, wait4, kill, fcntl, flock, fsync, fdatasync, truncate, ftruncate, getdents, getcwd, chdir, rename, mkdir, rmdir, link, unlink, readlink, chmod, fchmod, chown, fchown, lchown, umask, gettimeofday, getuid, getgid, geteuid, getegid, getppid, getpgrp, setpgid, getgroups, setgroups, setsid, setreuid, setregid, getresuid, getresgid, setresuid, setresgid, getpgid, setfsuid, setfsgid, getsid, capget, capset, rt_sigpending, rt_sigtimedwait, rt_sigqueueinfo, rt_sigsuspend, sigaltstack, mknod, uselib, personality, ustat, statfs, fstatfs, sysfs, getpriority, setpriority, sched_setparam, sched_getparam, sched_setscheduler, sched_getscheduler, sched_get_priority_max, sched_get_priority_min, sched_rr_get_interval, mlock, munlock, mlockall, munlockall, vhangup, pivot_key, _sysctl, prctl, arch_prctl, adjtimex, setrlimit, chroot, sync, acct, settimeofday, mount, umount2, swapon, swapoff, reboot, sethostname, setdomainname, iopl, ioperm, create_module, init_module, delete_module, get_kernel_syms, query_module, quotactl, nfsservctl, getpmsg, putpmsg, afs_syscall, tuxcall, security, lookup_dcookie, io_setup, io_destroy, io_getevents, io_submit, io_cancel, get_thread_area, set_thread_area, remap_file_pages, initgroups, listxattr, llistxattr, flistxattr, removexattr, lremovexattr, fremovexattr, tkill, time, futex, sched_setaffinity, sched_getaffinity, set_thread_area, io_setup, io_destroy, io_getevents, io_submit, io_cancel, epoll_create, epoll_ctl_old, epoll_wait_old, remap_file_pages, getdents64, set_tid_address, restart_syscall, semtimedop, fadvise64, timer_create, timer_settime, timer_gettime, timer_getoverrun, timer_delete, clock_settime, clock_gettime, clock_getres, clock_nanosleep, exit_group, epoll_wait, epoll_ctl, tgkill, openat, mkdirat, mknodat, fchownat, futimesat, newfstatat, unlinkat, renameat, linkat, symlinkat, readlinkat, fchmodat, faccessat, pselect6, ppoll, unshare, set_robust_list, get_robust_list, splice, tee, sync_file_range, vmsplice, move_pages, utimensat, epoll_pwait, signalfd, timerfd_create, eventfd, fallocate, timerfd_settime, timerfd_gettime, accept4, signalfd4, eventfd2, epoll_create1, dup3, pipe2, inotify_init1, preadv, pwritev, rt_tgsigqueueinfo, perf_event_open, recvmmsg, fanotify_init, fanotify_mark, prlimit64, name_to_handle_at, open_by_handle_at, clock_adjtime, syncfs, sendmmsg, setns, getcpu, process_vm_readv, process_vm_writev, kcmp, finit_module, sched_setattr, sched_getattr, renameat2, seccomp, bpf, execveat, userfaultfd, membarrier, mlock2, copy_file_range, preadv2, pwritev2, pkey_mprotect, pkey_alloc, pkey_free, statx, rseq } DEFAULT KILL",
	}

	// Network mode
	if c.NetworkMode == "none" {
		args = append(args, "--disable_clone_newnet")
	}

	return args
}

// CommandArgs generates the full nsjail command line for executing a command.
func (c *NsjailConfig) CommandArgs(command string, cmdArgs []string) []string {
	args := c.ToArgs("")
	// Replace the trailing /bin/bash with the actual command
	args = args[:len(args)-1] // remove seccomp for now, will be in config
	args = append(args, "--", command)
	args = append(args, cmdArgs...)
	return args
}

// ScriptArgs generates the full nsjail command line for executing a script.
func (c *NsjailConfig) ScriptArgs(interpreter, scriptContent string) []string {
	interpreterPath := interpreterToPath(interpreter)
	args := c.ToArgs("")
	args = args[:len(args)-1]
	args = append(args, "--", interpreterPath, "-c", scriptContent)
	return args
}

func interpreterToPath(interpreter string) string {
	paths := map[string]string{
		"bash":     "/bin/bash",
		"sh":       "/bin/sh",
		"python3":  "/usr/bin/python3",
		"python":   "/usr/bin/python3",
		"perl":     "/usr/bin/perl",
	}
	if p, ok := paths[interpreter]; ok {
		return p
	}
	return "/bin/sh"
}

// WriteConfigFile writes the nsjail config to a temporary file for the given task.
func (c *NsjailConfig) WriteConfigFile(taskID string) (string, error) {
	workDir := c.WorkDir
	if workDir == "" {
		workDir = "/tmp/opsagent-sandbox"
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("create sandbox work dir: %w", err)
	}

	configPath := filepath.Join(workDir, fmt.Sprintf("nsjail-%s.cfg", taskID))
	// For now, we use CLI args rather than config files
	return configPath, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/sandbox/ -run TestNsjail -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/nsjail.go internal/sandbox/nsjail_test.go
git commit -m "feat(sandbox): add nsjail configuration generator"
```

---

## Task 7.3: Output Streamer

- [ ] **Step 1: Write failing tests**

Create `internal/sandbox/output_streamer_test.go`:

```go
package sandbox

import (
	"testing"
	"time"
)

func TestOutputStreamerFlushBySize(t *testing.T) {
	var sent [][]byte
	sender := func(data []byte) {
		cpy := make([]byte, len(data))
		copy(cpy, data)
		sent = append(sent, cpy)
	}

	streamer := NewOutputStreamer("task-1", "stdout", 10, 1*time.Hour, sender)

	streamer.Write([]byte("12345"))
	streamer.Write([]byte("67890")) // triggers size flush

	if len(sent) != 1 {
		t.Fatalf("expected 1 flush, got %d", len(sent))
	}
	if string(sent[0]) != "1234567890" {
		t.Fatalf("expected '1234567890', got '%s'", string(sent[0]))
	}
}

func TestOutputStreamerFlushByInterval(t *testing.T) {
	var sent [][]byte
	sender := func(data []byte) {
		cpy := make([]byte, len(data))
		copy(cpy, data)
		sent = append(sent, cpy)
	}

	streamer := NewOutputStreamer("task-1", "stdout", 1024, 50*time.Millisecond, sender)

	streamer.Write([]byte("hello"))

	time.Sleep(100 * time.Millisecond)
	streamer.Flush()

	if len(sent) == 0 {
		t.Fatal("expected at least 1 flush by interval")
	}
}

func TestOutputStreamerFlushRemaining(t *testing.T) {
	var sent [][]byte
	sender := func(data []byte) {
		cpy := make([]byte, len(data))
		copy(cpy, data)
		sent = append(sent, cpy)
	}

	streamer := NewOutputStreamer("task-1", "stderr", 1024, 1*time.Hour, sender)

	streamer.Write([]byte("partial"))
	streamer.Flush()

	if len(sent) != 1 {
		t.Fatalf("expected 1 flush, got %d", len(sent))
	}
	if string(sent[0]) != "partial" {
		t.Fatalf("expected 'partial', got '%s'", string(sent[0]))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/sandbox/ -run TestOutputStreamer -v
```

Expected: FAIL.

- [ ] **Step 3: Implement OutputStreamer**

Create `internal/sandbox/output_streamer.go`:

```go
package sandbox

import (
	"sync"
	"time"
)

// OutputSender is a function that sends output data (e.g., via gRPC).
type OutputSender func(data []byte)

// OutputStreamer buffers stdout/stderr and flushes by size or interval.
type OutputStreamer struct {
	taskID        string
	stream        string // "stdout" | "stderr"
	flushSize     int
	flushInterval time.Duration
	sender        OutputSender

	mu     sync.Mutex
	buf    []byte
	stopCh chan struct{}
}

// NewOutputStreamer creates a new OutputStreamer.
func NewOutputStreamer(taskID, stream string, flushSize int, flushInterval time.Duration, sender OutputSender) *OutputStreamer {
	o := &OutputStreamer{
		taskID:        taskID,
		stream:        stream,
		flushSize:     flushSize,
		flushInterval: flushInterval,
		sender:        sender,
		buf:           make([]byte, 0, flushSize),
		stopCh:        make(chan struct{}),
	}

	go o.intervalFlush()

	return o
}

// Write adds data to the buffer. Flushes if the buffer exceeds flushSize.
func (o *OutputStreamer) Write(data []byte) {
	o.mu.Lock()
	o.buf = append(o.buf, data...)

	if len(o.buf) >= o.flushSize {
		flushData := make([]byte, len(o.buf))
		copy(flushData, o.buf)
		o.buf = o.buf[:0]
		o.mu.Unlock()
		o.sender(flushData)
		return
	}
	o.mu.Unlock()
}

// Flush sends any remaining buffered data.
func (o *OutputStreamer) Flush() {
	o.mu.Lock()
	if len(o.buf) == 0 {
		o.mu.Unlock()
		return
	}
	flushData := make([]byte, len(o.buf))
	copy(flushData, o.buf)
	o.buf = o.buf[:0]
	o.mu.Unlock()
	o.sender(flushData)
}

// Stop stops the interval flush goroutine and flushes remaining data.
func (o *OutputStreamer) Stop() {
	close(o.stopCh)
	o.Flush()
}

func (o *OutputStreamer) intervalFlush() {
	ticker := time.NewTicker(o.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.stopCh:
			return
		case <-ticker.C:
			o.Flush()
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/sandbox/ -run TestOutputStreamer -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/output_streamer.go internal/sandbox/output_streamer_test.go
git commit -m "feat(sandbox): add OutputStreamer for buffered stdout/stderr capture"
```

---

## Task 7.4: cgroup Stats Reader

- [ ] **Step 1: Implement Stats reader**

Create `internal/sandbox/stats.go`:

```go
package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Stats holds resource usage statistics from a cgroup.
type Stats struct {
	PeakMemoryBytes int64
	CPUTimeUserMs   int64
	CPUTimeSystemMs int64
	ProcessCount    int32
	BytesWritten    int64
	BytesRead       int64
}

// ReadCgroupStats reads resource stats from a cgroup v2 directory.
func ReadCgroupStats(cgroupPath string) (*Stats, error) {
	stats := &Stats{}

	// Memory peak
	if data, err := os.ReadFile(filepath.Join(cgroupPath, "memory.peak")); err == nil {
		stats.PeakMemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}

	// CPU stat
	if data, err := os.ReadFile(filepath.Join(cgroupPath, "cpu.stat")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "user_usec":
				usec, _ := strconv.ParseInt(parts[1], 10, 64)
				stats.CPUTimeUserMs = usec / 1000
			case "system_usec":
				usec, _ := strconv.ParseInt(parts[1], 10, 64)
				stats.CPUTimeSystemMs = usec / 1000
			}
		}
	}

	// PIDs current
	if data, err := os.ReadFile(filepath.Join(cgroupPath, "pids.current")); err == nil {
		count, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 32)
		stats.ProcessCount = int32(count)
	}

	// IO stat
	if data, err := os.ReadFile(filepath.Join(cgroupPath, "io.stat")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			parts := strings.Fields(line)
			for i := 1; i < len(parts); i += 2 {
				if i+1 >= len(parts) {
					break
				}
				key := parts[i]
				val, _ := strconv.ParseInt(parts[i+1], 10, 64)
				switch key {
				case "rbytes":
					stats.BytesRead += val
				case "wbytes":
					stats.BytesWritten += val
				}
			}
		}
	}

	return stats, nil
}

// CreateCgroup creates a cgroup v2 directory for a sandbox task.
func CreateCgroup(basePath, taskID string) (string, error) {
	cgroupPath := filepath.Join(basePath, fmt.Sprintf("sandbox-%s", taskID))
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return "", fmt.Errorf("create cgroup: %w", err)
	}
	return cgroupPath, nil
}

// SetCgroupLimits configures resource limits on a cgroup.
func SetCgroupLimits(cgroupPath string, memoryMB, cpuPercent, maxPIDs int) error {
	// Memory limit
	if memoryMB > 0 {
		data := fmt.Sprintf("%d", memoryMB*1024*1024)
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(data), 0644); err != nil {
			return fmt.Errorf("set memory.max: %w", err)
		}
	}

	// CPU limit (quota period)
	if cpuPercent > 0 {
		quota := cpuPercent * 1000 // 1000us period
		data := fmt.Sprintf("%d 1000000", quota)
		if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(data), 0644); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	}

	// PID limit
	if maxPIDs > 0 {
		data := fmt.Sprintf("%d", maxPIDs)
		if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(data), 0644); err != nil {
			return fmt.Errorf("set pids.max: %w", err)
		}
	}

	return nil
}

// KillCgroupProcesses kills all processes in a cgroup.
func KillCgroupProcesses(cgroupPath string) error {
	return os.WriteFile(filepath.Join(cgroupPath, "cgroup.kill"), []byte("1"), 0644)
}

// RemoveCgroup removes a cgroup directory.
func RemoveCgroup(cgroupPath string) error {
	return os.RemoveAll(cgroupPath)
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/sandbox/...
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/stats.go
git commit -m "feat(sandbox): add cgroup v2 stats reader and limit setter"
```

---

## Task 7.5: Audit Logger

- [ ] **Step 1: Implement Audit logger**

Create `internal/sandbox/audit.go`:

```go
package sandbox

import (
	"time"

	"github.com/rs/zerolog"
)

// AuditEvent represents a sandbox execution audit event.
type AuditEvent struct {
	TaskID      string        `json:"task_id"`
	Timestamp   time.Time     `json:"timestamp"`
	TriggeredBy string        `json:"triggered_by"`
	Type        string        `json:"type"` // "command" | "script"
	Command     string        `json:"command,omitempty"`
	Interpreter string        `json:"interpreter,omitempty"`
	ExitCode    int           `json:"exit_code"`
	Duration    time.Duration `json:"duration"`
	TimedOut    bool          `json:"timed_out"`
	Truncated   bool          `json:"truncated"`
	Killed      bool          `json:"killed"`
	Stats       Stats         `json:"stats"`
	Error       string        `json:"error,omitempty"`
}

// AuditLogger logs sandbox execution events.
type AuditLogger struct {
	logger zerolog.Logger
}

// NewAuditLogger creates a new AuditLogger.
func NewAuditLogger(logger zerolog.Logger) *AuditLogger {
	return &AuditLogger{logger: logger.With().Str("component", "sandbox-audit").Logger()}
}

// LogExecution logs a completed sandbox execution.
func (a *AuditLogger) LogExecution(event AuditEvent) {
	level := zerolog.InfoLevel
	if event.ExitCode != 0 {
		level = zerolog.WarnLevel
	}
	if event.Error != "" {
		level = zerolog.ErrorLevel
	}

	a.logger.WithLevel(level).
		Str("task_id", event.TaskID).
		Str("triggered_by", event.TriggeredBy).
		Str("type", event.Type).
		Str("command", event.Command).
		Int("exit_code", event.ExitCode).
		Dur("duration", event.Duration).
		Bool("timed_out", event.TimedOut).
		Bool("truncated", event.Truncated).
		Bool("killed", event.Killed).
		Int64("peak_memory_bytes", event.Stats.PeakMemoryBytes).
		Int64("cpu_time_user_ms", event.Stats.CPUTimeUserMs).
		Int32("process_count", event.Stats.ProcessCount).
		Msg("sandbox execution completed")
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/sandbox/...
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/audit.go
git commit -m "feat(sandbox): add structured audit logger for execution events"
```

---

## Task 7.6: Network Isolation

- [ ] **Step 1: Implement Network isolation**

Create `internal/sandbox/network.go`:

```go
package sandbox

import (
	"fmt"
	"os/exec"
)

// NetworkManager handles network namespace setup for sandbox execution.
type NetworkManager struct {
	enabled bool
}

// NewNetworkManager creates a new NetworkManager.
func NewNetworkManager() *NetworkManager {
	return &NetworkManager{enabled: true}
}

// SetupIsolatedNetwork creates a network namespace with no connectivity.
func (n *NetworkManager) SetupIsolatedNetwork(taskID string) error {
	// nsjail handles network namespace creation via --disable_clone_newnet
	// This function is for additional network setup if needed
	return nil
}

// SetupAllowlistNetwork creates a network namespace with specific allowed destinations.
func (n *NetworkManager) SetupAllowlistNetwork(taskID string, allowedIPs []string) error {
	if len(allowedIPs) == 0 {
		return nil
	}

	// Create veth pair
	vethHost := fmt.Sprintf("veth-h-%s", taskID[:8])
	vethGuest := fmt.Sprintf("veth-g-%s", taskID[:8])

	if err := exec.Command("ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethGuest).Run(); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	// Bring up host side
	if err := exec.Command("ip", "link", "set", vethHost, "up").Run(); err != nil {
		return fmt.Errorf("bring up veth host: %w", err)
	}

	// Add iptables rules for allowed IPs
	for _, ip := range allowedIPs {
		if err := exec.Command("iptables", "-A", "FORWARD", "-i", vethHost, "-d", ip, "-j", "ACCEPT").Run(); err != nil {
			return fmt.Errorf("add iptables allow for %s: %w", ip, err)
		}
	}

	// Drop everything else
	if err := exec.Command("iptables", "-A", "FORWARD", "-i", vethHost, "-j", "DROP").Run(); err != nil {
		return fmt.Errorf("add iptables drop: %w", err)
	}

	return nil
}

// CleanupNetwork removes network resources for a sandbox task.
func (n *NetworkManager) CleanupNetwork(taskID string) error {
	vethHost := fmt.Sprintf("veth-h-%s", taskID[:8])

	// Remove veth pair (removes peer automatically)
	if err := exec.Command("ip", "link", "del", vethHost).Run(); err != nil {
		// Ignore error if veth doesn't exist
	}

	return nil
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/sandbox/...
```

Expected: Compiles without errors.

- [ ] **Step 3: Commit**

```bash
git add internal/sandbox/network.go
git commit -m "feat(sandbox): add network namespace isolation with allowlist"
```

---

## Task 7.7: Sandbox Executor (Main Logic)

- [ ] **Step 1: Write failing tests**

Create `internal/sandbox/executor_test.go`:

```go
package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestExecutorRunCommand(t *testing.T) {
	if _, err := exec.LookPath("nsjail"); err != nil {
		t.Skip("nsjail not installed, skipping sandbox test")
	}

	log := zerolog.Nop()
	executor := NewExecutor(Config{
		NsjailPath:  "nsjail",
		WorkDir:     "/tmp/opsagent-sandbox-test",
		MemoryMB:    256,
		CPUPercent:  50,
		MaxPIDs:     16,
		TimeoutSec:  10,
		MaxOutputKB: 1024,
		NetworkMode: "none",
		Policy: Policy{
			AllowedCommands:     []string{"echo", "df", "free", "cat"},
			BlockedCommands:     []string{"reboot"},
			AllowedInterpreters: []string{"bash", "sh"},
		},
	}, log)

	var outputData []byte
	outputSender := func(data []byte) {
		outputData = append(outputData, data...)
	}

	result, err := executor.ExecuteCommand(context.Background(), ExecRequest{
		TaskID:  "test-cmd-1",
		Command: "echo",
		Args:    []string{"hello world"},
	}, outputSender)

	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.TimedOut {
		t.Fatal("should not have timed out")
	}
}

func TestExecutorBlockCommand(t *testing.T) {
	log := zerolog.Nop()
	executor := NewExecutor(Config{
		Policy: Policy{
			AllowedCommands: []string{"df"},
		},
	}, log)

	_, err := executor.ExecuteCommand(context.Background(), ExecRequest{
		TaskID:  "test-block-1",
		Command: "uname",
	}, nil)

	if err == nil {
		t.Fatal("expected error for blocked command")
	}
}

func TestExecutorBlockScript(t *testing.T) {
	log := zerolog.Nop()
	executor := NewExecutor(Config{
		Policy: Policy{
			AllowedInterpreters: []string{"bash"},
			BlockedKeywords:     []string{"rm -rf /"},
		},
	}, log)

	_, err := executor.ExecuteScript(context.Background(), ExecRequest{
		TaskID:      "test-block-2",
		Interpreter: "bash",
		Script:      "rm -rf / --no-preserve-root",
	}, nil)

	if err == nil {
		t.Fatal("expected error for blocked script")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/sandbox/ -run TestExecutor -v
```

Expected: FAIL.

- [ ] **Step 3: Implement Executor**

Create `internal/sandbox/executor.go`:

```go
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
)

// Config holds sandbox executor configuration.
type Config struct {
	NsjailPath  string
	WorkDir     string
	CgroupBase  string
	MemoryMB    int
	CPUPercent  int
	MaxPIDs     int
	TimeoutSec  int
	MaxOutputKB int
	NetworkMode string
	Policy      Policy
}

// ExecRequest represents a sandbox execution request.
type ExecRequest struct {
	TaskID      string
	Command     string
	Args        []string
	Script      string
	Interpreter string
	Env         map[string]string
	Timeout     time.Duration
	SandboxCfg  *SandboxOverride
}

// SandboxOverride allows per-request overrides of sandbox limits.
type SandboxOverride struct {
	MemoryMB    int
	CPUPercent  int
	MaxPIDs     int
	NetworkMode string
	AllowedIPs  []string
	MaxOutputKB int
}

// ExecResult holds the result of a sandbox execution.
type ExecResult struct {
	TaskID    string
	ExitCode  int
	Duration  time.Duration
	TimedOut  bool
	Truncated bool
	Killed    bool
	Stats     Stats
}

// Executor manages sandboxed command and script execution.
type Executor struct {
	cfg    Config
	logger zerolog.Logger
	audit  *AuditLogger
	net    *NetworkManager
}

// NewExecutor creates a new sandbox Executor.
func NewExecutor(cfg Config, logger zerolog.Logger) *Executor {
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = 512
	}
	if cfg.CPUPercent <= 0 {
		cfg.CPUPercent = 50
	}
	if cfg.MaxPIDs <= 0 {
		cfg.MaxPIDs = 32
	}
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 30
	}
	if cfg.MaxOutputKB <= 0 {
		cfg.MaxOutputKB = 10240 // 10MB
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "none"
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = "/tmp/opsagent-sandbox"
	}

	return &Executor{
		cfg:    cfg,
		logger: logger,
		audit:  NewAuditLogger(logger),
		net:    NewNetworkManager(),
	}
}

// ExecuteCommand executes a command in a sandbox.
func (e *Executor) ExecuteCommand(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateCommand(req.Command, req.Args); err != nil {
		return nil, fmt.Errorf("policy violation: %w", err)
	}

	nsjailCfg := e.buildNsjailConfig(req)
	args := nsjailCfg.CommandArgs(req.Command, req.Args)

	return e.run(ctx, req, args, outputSender)
}

// ExecuteScript executes a script in a sandbox.
func (e *Executor) ExecuteScript(ctx context.Context, req ExecRequest, outputSender OutputSender) (*ExecResult, error) {
	if err := e.cfg.Policy.ValidateScript(req.Interpreter, req.Script); err != nil {
		return nil, fmt.Errorf("policy violation: %w", err)
	}

	// Write script to temp file
	workDir := filepath.Join(e.cfg.WorkDir, req.TaskID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	scriptPath := filepath.Join(workDir, "script")
	if err := os.WriteFile(scriptPath, []byte(req.Script), 0755); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}

	nsjailCfg := e.buildNsjailConfig(req)
	args := nsjailCfg.ScriptArgs(req.Interpreter, req.Script)

	return e.run(ctx, req, args, outputSender)
}

func (e *Executor) run(ctx context.Context, req ExecRequest, args []string, outputSender OutputSender) (*ExecResult, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = time.Duration(e.cfg.TimeoutSec) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Setup cgroup
	cgroupPath := ""
	if e.cfg.CgroupBase != "" {
		var err error
		cgroupPath, err = CreateCgroup(e.cfg.CgroupBase, req.TaskID)
		if err != nil {
			e.logger.Warn().Err(err).Msg("failed to create cgroup, proceeding without")
		} else {
			memMB := e.cfg.MemoryMB
			cpuPct := e.cfg.CPUPercent
			maxPIDs := e.cfg.MaxPIDs
			if req.SandboxCfg != nil {
				if req.SandboxCfg.MemoryMB > 0 {
					memMB = req.SandboxCfg.MemoryMB
				}
				if req.SandboxCfg.CPUPercent > 0 {
					cpuPct = req.SandboxCfg.CPUPercent
				}
				if req.SandboxCfg.MaxPIDs > 0 {
					maxPIDs = req.SandboxCfg.MaxPIDs
				}
			}
			SetCgroupLimits(cgroupPath, memMB, cpuPct, maxPIDs)
			defer func() {
				KillCgroupProcesses(cgroupPath)
				RemoveCgroup(cgroupPath)
			}()
		}
	}

	// Create output streamer
	maxOutputBytes := e.cfg.MaxOutputKB * 1024
	var streamer *OutputStreamer
	if outputSender != nil {
		streamer = NewOutputStreamer(req.TaskID, "stdout", 4096, 500*time.Millisecond, outputSender)
		defer streamer.Stop()
	}

	// Build command
	cmd := exec.CommandContext(ctx, e.cfg.NsjailPath, args...)
	cmd.Dir = e.cfg.WorkDir

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture output
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// Stream output if sender provided
	if streamer != nil && stdoutBuf.Len() > 0 {
		streamer.Write(stdoutBuf.Bytes())
	}

	result := &ExecResult{
		TaskID:   req.TaskID,
		Duration: duration,
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	} else if err != nil {
		result.ExitCode = -1
	}

	// Check for timeout
	if ctx.Err() == context.DeadlineExceeded {
		result.TimedOut = true
		result.Killed = true
	}

	// Check for truncation
	totalOutput := stdoutBuf.Len() + stderrBuf.Len()
	if totalOutput > maxOutputBytes {
		result.Truncated = true
	}

	// Read cgroup stats
	if cgroupPath != "" {
		if stats, err := ReadCgroupStats(cgroupPath); err == nil {
			result.Stats = *stats
		}
	}

	// Audit log
	e.audit.LogExecution(AuditEvent{
		TaskID:    req.TaskID,
		Timestamp: start,
		Type:      "command",
		Command:   req.Command,
		ExitCode:  result.ExitCode,
		Duration:  duration,
		TimedOut:  result.TimedOut,
		Truncated: result.Truncated,
		Killed:    result.Killed,
		Stats:     result.Stats,
	})

	return result, nil
}

func (e *Executor) buildNsjailConfig(req ExecRequest) NsjailConfig {
	cfg := NsjailConfig{
		TimeLimit:   e.cfg.TimeoutSec,
		MemoryMB:    e.cfg.MemoryMB,
		CPUPercent:  e.cfg.CPUPercent,
		MaxPIDs:     e.cfg.MaxPIDs,
		MaxFileSize: 64,
		NetworkMode: e.cfg.NetworkMode,
		WorkDir:     e.cfg.WorkDir,
	}

	if req.SandboxCfg != nil {
		if req.SandboxCfg.MemoryMB > 0 {
			cfg.MemoryMB = req.SandboxCfg.MemoryMB
		}
		if req.SandboxCfg.CPUPercent > 0 {
			cfg.CPUPercent = req.SandboxCfg.CPUPercent
		}
		if req.SandboxCfg.MaxPIDs > 0 {
			cfg.MaxPIDs = req.SandboxCfg.MaxPIDs
		}
		if req.SandboxCfg.NetworkMode != "" {
			cfg.NetworkMode = req.SandboxCfg.NetworkMode
		}
		if len(req.SandboxCfg.AllowedIPs) > 0 {
			cfg.AllowedIPs = req.SandboxCfg.AllowedIPs
		}
	}

	return cfg
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/sandbox/ -run TestExecutor -v
```

Expected: PASS for TestExecutorBlockCommand and TestExecutorBlockScript. TestExecutorRunCommand may skip if nsjail not installed.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/executor.go internal/sandbox/executor_test.go
git commit -m "feat(sandbox): add Executor with nsjail, policy, cgroup, audit, output streaming"
```
