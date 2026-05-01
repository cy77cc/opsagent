# Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all 33 security findings from the code review across sandbox, plugin, API/auth, and data subsystems.

**Architecture:** Four phases of targeted security fixes, each producing a testable PR. Fixes are localized to specific files with new validation functions, permission changes, and middleware additions.

**Tech Stack:** Go (nsjail sandbox, gRPC, HTTP server), Rust (plugin runtime), systemd

---

## Phase 1: Sandbox Security

### Task 1: TaskID Path Traversal Fix

**Files:**
- Modify: `internal/sandbox/nsjail.go`
- Modify: `internal/sandbox/stats.go`
- Create: `internal/sandbox/sanitize_test.go`

- [ ] **Step 1: Write failing tests for sanitizeTaskID**

```go
// internal/sandbox/sanitize_test.go
package sandbox

import "testing"

func TestSanitizeTaskID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "task-123", false},
		{"valid with dots", "task-1.2.3", false},
		{"empty", "", true},
		{"path traversal dotdot", "../../etc/passwd", true},
		{"path traversal slash", "etc/passwd", true},
		{"path traversal backslash", `etc\passwd`, true},
		{"just dotdot", "..", true},
		{"embedded dotdot", "task-../evil", true},
		{"null byte", "task\x00evil", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizeTaskID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeTaskID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestSanitizeTaskID -v`
Expected: FAIL — `sanitizeTaskID` not defined

- [ ] **Step 3: Implement sanitizeTaskID**

Add to `internal/sandbox/nsjail.go`:

```go
import "strings"

// sanitizeTaskID validates that a task ID does not contain path traversal sequences.
func sanitizeTaskID(taskID string) (string, error) {
	if taskID == "" {
		return "", fmt.Errorf("task ID is required")
	}
	cleaned := filepath.Clean(taskID)
	if cleaned == ".." || strings.ContainsAny(cleaned, `/\`) {
		return "", fmt.Errorf("invalid task ID: %q contains path traversal", taskID)
	}
	if strings.ContainsRune(cleaned, '\x00') {
		return "", fmt.Errorf("invalid task ID: contains null byte")
	}
	return cleaned, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestSanitizeTaskID -v`
Expected: PASS

- [ ] **Step 5: Apply sanitizeTaskID in WriteScriptFile, WriteConfigFile**

In `nsjail.go`, update `WriteScriptFile`:

```go
func (c *NsjailConfig) WriteScriptFile(taskID, scriptContent string) (string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	// ... rest unchanged
}
```

Update `WriteConfigFile` similarly.

- [ ] **Step 6: Apply sanitizeTaskID in CreateCgroup**

In `stats.go`, update `CreateCgroup`:

```go
func CreateCgroup(basePath, taskID string) (string, error) {
	taskID, err := sanitizeTaskID(taskID)
	if err != nil {
		return "", err
	}
	cgroupPath := filepath.Join(basePath, fmt.Sprintf("sandbox-%s", taskID))
	// ... rest unchanged
}
```

- [ ] **Step 7: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/nsjail.go internal/sandbox/stats.go internal/sandbox/sanitize_test.go
git commit -m "fix(security): sanitize TaskID to prevent path traversal in sandbox file operations"
```

---

### Task 2: iptables IP Validation

**Files:**
- Modify: `internal/sandbox/network.go`
- Modify: `internal/sandbox/network_test.go` (create if not exists)

- [ ] **Step 1: Write failing tests for validateIPList**

```go
// internal/sandbox/network_test.go
package sandbox

import "testing"

func TestValidateIPList(t *testing.T) {
	tests := []struct {
		name    string
		ips     []string
		wantErr bool
	}{
		{"valid IPs", []string{"10.0.0.1", "192.168.1.1"}, false},
		{"valid CIDR", []string{"10.0.0.0/8", "192.168.0.0/16"}, false},
		{"empty list", []string{}, false},
		{"invalid IP", []string{"not-an-ip"}, true},
		{"injection attempt", []string{"0.0.0.0/0 -j ACCEPT --dport 22"}, true},
		{"iptables flag", []string{"-A"}, true},
		{"empty string", []string{""}, true},
		{"mixed valid invalid", []string{"10.0.0.1", "bad"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIPList(tt.ips)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIPList(%v) error = %v, wantErr %v", tt.ips, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestValidateIPList -v`
Expected: FAIL — `validateIPList` not defined

- [ ] **Step 3: Implement validateIPList**

Add to `internal/sandbox/network.go`:

```go
import (
	"fmt"
	"net"
	"os/exec"
	"strings"
)

func validateIPList(ips []string) error {
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			return fmt.Errorf("empty IP address")
		}
		if strings.Contains(ip, "/") {
			if _, _, err := net.ParseCIDR(ip); err != nil {
				return fmt.Errorf("invalid CIDR %q: %w", ip, err)
			}
		} else {
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("invalid IP address %q", ip)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestValidateIPList -v`
Expected: PASS

- [ ] **Step 5: Add validation call in SetupAllowlistNetwork**

In `network.go`, add at the start of `SetupAllowlistNetwork`:

```go
func (nm *NetworkManager) SetupAllowlistNetwork(taskID string, allowedIPs []string) error {
	if !nm.enabled {
		return fmt.Errorf("network manager is not enabled")
	}
	if err := validateIPList(allowedIPs); err != nil {
		return fmt.Errorf("invalid allowed IPs: %w", err)
	}
	// ... rest unchanged
}
```

- [ ] **Step 6: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/network.go internal/sandbox/network_test.go
git commit -m "fix(security): validate IP addresses before passing to iptables"
```

---

### Task 3: Dynamic Seccomp Policy + Fork Bomb Fix

**Files:**
- Modify: `internal/sandbox/nsjail.go`
- Modify: `internal/sandbox/sanitize_test.go`

- [ ] **Step 1: Write failing tests for seccompPolicyString**

Add to `internal/sandbox/sanitize_test.go`:

```go
func TestSeccompPolicyString(t *testing.T) {
	tests := []struct {
		name        string
		networkMode string
		wantNetwork bool
	}{
		{"disabled mode excludes network syscalls", "disabled", false},
		{"allowlist mode includes network syscalls", "allowlist", true},
		{"default excludes network syscalls", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NsjailConfig{NetworkMode: tt.networkMode}
			policy := cfg.seccompPolicyString()
			if tt.wantNetwork && !strings.Contains(policy, "socket") {
				t.Errorf("expected network syscalls in allowlist mode")
			}
			if !tt.wantNetwork && strings.Contains(policy, "socket") {
				t.Errorf("expected no network syscalls in %q mode", tt.networkMode)
			}
			// Verify fork bomb protection
			if strings.Contains(policy, "clone") || strings.Contains(policy, "fork") || strings.Contains(policy, "vfork") {
				t.Errorf("seccomp policy should not allow clone/fork/vfork")
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestSeccompPolicyString -v`
Expected: FAIL — `seccompPolicyString` not defined

- [ ] **Step 3: Replace static seccompPolicy with dynamic generation**

In `nsjail.go`, replace the `seccompPolicy` constant and add the method:

```go
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

const networkSyscalls = `,
    socket, connect, bind, listen, accept, accept4,
    sendto, recvfrom, sendmsg, recvmsg, shutdown,
    setsockopt, getsockopt, socketpair, eventfd2`

func (c *NsjailConfig) seccompPolicyString() string {
	if c.NetworkMode == "allowlist" {
		return baseSyscalls[:len(baseSyscalls)-1] + networkSyscalls + "\n}"
	}
	return baseSyscalls
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestSeccompPolicyString -v`
Expected: PASS

- [ ] **Step 5: Update ToArgs to use dynamic policy**

In `nsjail.go`, replace line 86:

```go
// Before:
args = append(args, fmt.Sprintf("--seccomp_policy_string=%s", seccompPolicy))
// After:
args = append(args, fmt.Sprintf("--seccomp_policy_string=%s", c.seccompPolicyString()))
```

- [ ] **Step 6: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/nsjail.go internal/sandbox/sanitize_test.go
git commit -m "fix(security): make seccomp policy dynamic, remove clone/fork/vfork to prevent fork bombs"
```

---

### Task 4: Sandbox /etc Mount + Resource Bounds + Command Name Check

**Files:**
- Modify: `internal/sandbox/nsjail.go`
- Modify: `internal/sandbox/executor.go`
- Modify: `internal/sandbox/policy.go`
- Modify: `internal/sandbox/sanitize_test.go`

- [ ] **Step 1: Write failing test for command name metacharacter check**

Add to `internal/sandbox/sanitize_test.go`:

```go
func TestValidateCommandNameMetacharacters(t *testing.T) {
	p := Policy{
		AllowedCommands: []string{"ls", "echo"},
	}
	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"valid command", "ls", false},
		{"semicolon injection", "ls;rm -rf /", true},
		{"pipe injection", "ls|cat", true},
		{"backtick injection", "ls`whoami`", true},
		{"dollar injection", "ls$(whoami)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.ValidateCommand(tt.command, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestValidateCommandNameMetacharacters -v`
Expected: FAIL — metacharacter check on command name not yet implemented

- [ ] **Step 3: Add metacharacter check for command name in ValidateCommand**

In `policy.go`, after the `cmdName == ""` check:

```go
func (p *Policy) ValidateCommand(command string, args []string) error {
	cmdName := strings.TrimSpace(command)
	if cmdName == "" {
		return fmt.Errorf("command is required")
	}
	if containsShellMetacharacters(cmdName) {
		return fmt.Errorf("command name contains shell metacharacters: %q", cmdName)
	}
	// ... rest unchanged
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestValidateCommandNameMetacharacters -v`
Expected: PASS

- [ ] **Step 5: Remove /etc from bind mounts in ToArgs**

In `nsjail.go`, change line 75:

```go
// Before:
for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin", "/etc"} {
// After:
for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin"} {
    args = append(args, fmt.Sprintf("--bindmount_ro=%s", dir))
}
// Add minimal /etc tmpfs
args = append(args, "--tmpfsmount=/etc:tmpfs:size=1048576")
```

Also update `buildConfigContent` similarly (line 173).

- [ ] **Step 6: Add resource bounds to buildNsjailConfig**

In `executor.go`, update `buildNsjailConfig`:

```go
func (e *Executor) buildNsjailConfig(req ExecRequest) NsjailConfig {
	cfg := NsjailConfig{
		TimeLimit:   e.cfg.TimeoutSec,
		MemoryMB:    e.cfg.MemoryMB,
		CPUPercent:  e.cfg.CPUPercent,
		MaxPIDs:     e.cfg.MaxPIDs,
		NetworkMode: e.cfg.NetworkMode,
		WorkDir:     e.cfg.WorkDir,
	}

	if req.SandboxCfg != nil {
		if req.SandboxCfg.MemoryMB > 0 {
			cfg.MemoryMB = min(req.SandboxCfg.MemoryMB, 1024)
		}
		if req.SandboxCfg.CPUPercent > 0 {
			cfg.CPUPercent = min(req.SandboxCfg.CPUPercent, 100)
		}
		if req.SandboxCfg.MaxPIDs > 0 {
			cfg.MaxPIDs = min(req.SandboxCfg.MaxPIDs, 256)
		}
		if req.SandboxCfg.NetworkMode != "" && e.cfg.NetworkMode != "disabled" {
			cfg.NetworkMode = req.SandboxCfg.NetworkMode
		}
		if len(req.SandboxCfg.AllowedIPs) > 0 {
			cfg.AllowedIPs = req.SandboxCfg.AllowedIPs
		}
	}

	return cfg
}
```

- [ ] **Step 7: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/sandbox/nsjail.go internal/sandbox/executor.go internal/sandbox/policy.go internal/sandbox/sanitize_test.go
git commit -m "fix(security): remove /etc mount, add resource bounds, check command name for metacharacters"
```

---

### Task 5: Audit Log Permissions + Predictable Temp Paths

**Files:**
- Modify: `internal/sandbox/audit.go`
- Modify: `internal/sandbox/nsjail.go`
- Modify: `internal/sandbox/sanitize_test.go`

- [ ] **Step 1: Write test for unpredictable temp file creation**

Add to `internal/sandbox/sanitize_test.go`:

```go
func TestWriteScriptFileUnpredictablePath(t *testing.T) {
	cfg := NsjailConfig{}
	path1, err := cfg.WriteScriptFile("task-1", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path1)

	path2, err := cfg.WriteScriptFile("task-1", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path2)

	// Paths should be different even for same taskID (unpredictable)
	if path1 == path2 {
		t.Errorf("expected unpredictable paths, got same path twice: %s", path1)
	}

	// Verify file permissions
	info, err := os.Stat(path1)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestWriteScriptFileUnpredictablePath -v`
Expected: FAIL — paths are the same (predictable)

- [ ] **Step 3: Update WriteScriptFile to use os.CreateTemp**

In `nsjail.go`:

```go
func (c *NsjailConfig) WriteScriptFile(taskID, scriptContent string) (string, error) {
	if _, err := sanitizeTaskID(taskID); err != nil {
		return "", err
	}
	scriptDir := filepath.Join(os.TempDir(), "nsjail-scripts")
	if err := os.MkdirAll(scriptDir, 0o700); err != nil {
		return "", fmt.Errorf("create script dir: %w", err)
	}
	f, err := os.CreateTemp(scriptDir, "task-*.sh")
	if err != nil {
		return "", fmt.Errorf("create script file: %w", err)
	}
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if _, err := f.WriteString(scriptContent); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
```

Update `WriteConfigFile` similarly with `os.CreateTemp(scriptDir, "task-*.cfg")`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestWriteScriptFileUnpredictablePath -v`
Expected: PASS

- [ ] **Step 5: Fix audit log permissions**

In `audit.go`, line 40:

```go
// Before:
f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
// After:
f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
```

- [ ] **Step 6: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/audit.go internal/sandbox/nsjail.go internal/sandbox/sanitize_test.go
git commit -m "fix(security): use unpredictable temp paths, restrict audit log permissions to 0600"
```

---

### Task 6: Sandbox Environment Variable Sanitization

**Files:**
- Modify: `internal/sandbox/executor.go`
- Modify: `internal/sandbox/sanitize_test.go`

- [ ] **Step 1: Write test for minimal sandbox environment**

Add to `internal/sandbox/sanitize_test.go`:

```go
func TestBuildSandboxEnv(t *testing.T) {
	reqEnv := map[string]string{
		"MY_VAR":      "value",
		"LD_PRELOAD":  "/evil.so",
		"LD_LIBRARY_PATH": "/evil",
	}
	env := buildSandboxEnv(reqEnv)

	// Should contain PATH
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			found = true
		}
		if strings.HasPrefix(e, "LD_PRELOAD=") {
			t.Error("LD_PRELOAD should be blocked")
		}
		if strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			t.Error("LD_LIBRARY_PATH should be blocked")
		}
		if e == "MY_VAR=value" {
			// good
		}
	}
	if !found {
		t.Error("PATH should be present")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run TestBuildSandboxEnv -v`
Expected: FAIL — `buildSandboxEnv` not defined

- [ ] **Step 3: Implement buildSandboxEnv**

In `executor.go`:

```go
func buildSandboxEnv(reqEnv map[string]string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/tmp",
		"LANG=C",
	}
	for k, v := range reqEnv {
		if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "DYLD_INSERT_LIBRARIES" {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run TestBuildSandboxEnv -v`
Expected: PASS

- [ ] **Step 5: Replace os.Environ() in run method**

In `executor.go`, line 224:

```go
// Before:
cmd.Env = os.Environ()
for k, v := range req.Env {
    cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
}
// After:
cmd.Env = buildSandboxEnv(req.Env)
```

- [ ] **Step 6: Run all sandbox tests**

Run: `go test ./internal/sandbox/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/executor.go internal/sandbox/sanitize_test.go
git commit -m "fix(security): sanitize environment variables passed to sandbox, block LD_PRELOAD"
```

---

## Phase 2: Plugin Security

### Task 7: Plugin Binary Path Traversal + Socket Injection

**Files:**
- Modify: `internal/pluginruntime/manifest.go`
- Modify: `internal/pluginruntime/gateway.go`
- Create: `internal/pluginruntime/security_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/pluginruntime/security_test.go
package pluginruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestPathTraversal(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "myplugin")
	os.MkdirAll(pluginDir, 0o755)

	// Write a manifest with path traversal in binary_path
	manifest := `
name: evil
version: 1.0.0
binary_path: ../../bin/evil
task_types: [test]
`
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")
	os.WriteFile(manifestPath, []byte(manifest), 0o644)

	_, err := LoadManifest(manifestPath)
	if err == nil {
		t.Error("expected error for path traversal in binary_path")
	}
}

func TestSanitizePluginName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "my-plugin_1", false},
		{"with slash", "../../etc/evil", true},
		{"with dot", "../evil", true},
		{"empty", "", true},
		{"starts with dash", "-bad", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizePluginName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizePluginName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pluginruntime/ -run "TestLoadManifestPathTraversal|TestSanitizePluginName" -v`
Expected: FAIL

- [ ] **Step 3: Implement sanitizePluginName**

In `gateway.go`:

```go
import "regexp"

var validPluginName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func sanitizePluginName(name string) (string, error) {
	if !validPluginName.MatchString(name) {
		return "", fmt.Errorf("invalid plugin name %q: must be alphanumeric with hyphens/underscores", name)
	}
	return name, nil
}
```

- [ ] **Step 4: Add path boundary check in LoadManifest**

In `manifest.go`:

```go
func LoadManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	m.resolvedDir = filepath.Dir(path)
	if !filepath.IsAbs(m.BinaryPath) {
		m.BinaryPath = filepath.Join(m.resolvedDir, m.BinaryPath)
	}
	cleanBin := filepath.Clean(m.BinaryPath)
	cleanDir := filepath.Clean(m.resolvedDir)
	if !strings.HasPrefix(cleanBin, cleanDir+string(filepath.Separator)) {
		return nil, fmt.Errorf("binary_path %q escapes plugin directory %q", m.BinaryPath, m.resolvedDir)
	}
	m.BinaryPath = cleanBin
	return m, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/pluginruntime/ -run "TestLoadManifestPathTraversal|TestSanitizePluginName" -v`
Expected: PASS

- [ ] **Step 6: Update loadPlugin to use sanitizePluginName and secure socket path**

In `gateway.go`, update `loadPlugin`:

```go
func (g *Gateway) loadPlugin(manifest *PluginManifest) error {
	mergedCfg := mergePluginConfig(manifest.Config, g.cfg.PluginConfigs[manifest.Name])
	manifest.Config = mergedCfg

	safeName, err := sanitizePluginName(manifest.Name)
	if err != nil {
		return err
	}
	socketDir := filepath.Join(os.TempDir(), "opsagent-plugins")
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	socketPath := filepath.Join(socketDir, safeName+".sock")

	// Check for symlink attack
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("socket path %q is a symlink, refusing to overwrite", socketPath)
		}
	}
	_ = os.Remove(socketPath)

	// ... rest unchanged
}
```

- [ ] **Step 7: Run all pluginruntime tests**

Run: `go test ./internal/pluginruntime/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/pluginruntime/manifest.go internal/pluginruntime/gateway.go internal/pluginruntime/security_test.go
git commit -m "fix(security): prevent plugin binary path traversal, sanitize socket paths, block symlink attacks"
```

---

### Task 8: Plugin Environment Sanitization + Socket Permissions

**Files:**
- Modify: `internal/pluginruntime/gateway.go`
- Modify: `sdk/plugin/serve.go`
- Modify: `internal/pluginruntime/security_test.go`

- [ ] **Step 1: Write failing test for buildPluginEnv**

Add to `internal/pluginruntime/security_test.go`:

```go
func TestBuildPluginEnv(t *testing.T) {
	manifestEnv := map[string]string{
		"MY_CONFIG":   "value",
		"LD_PRELOAD":  "/evil.so",
	}
	env := buildPluginEnv("/tmp/test.sock", manifestEnv)

	hasSocket := false
	hasPath := false
	hasLD := false
	hasConfig := false
	for _, e := range env {
		if e == "OPSAGENT_PLUGIN_SOCKET=/tmp/test.sock" {
			hasSocket = true
		}
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if strings.HasPrefix(e, "LD_PRELOAD=") {
			hasLD = true
		}
		if e == "MY_CONFIG=value" {
			hasConfig = true
		}
	}
	if !hasSocket {
		t.Error("missing OPSAGENT_PLUGIN_SOCKET")
	}
	if !hasPath {
		t.Error("missing PATH")
	}
	if hasLD {
		t.Error("LD_PRELOAD should be blocked")
	}
	if !hasConfig {
		t.Error("missing MY_CONFIG")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pluginruntime/ -run TestBuildPluginEnv -v`
Expected: FAIL — `buildPluginEnv` not defined

- [ ] **Step 3: Implement buildPluginEnv**

In `gateway.go`:

```go
func buildPluginEnv(socketPath string, manifestEnv map[string]string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"OPSAGENT_PLUGIN_SOCKET=" + socketPath,
	}
	for k, v := range manifestEnv {
		if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "DYLD_INSERT_LIBRARIES" {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/pluginruntime/ -run TestBuildPluginEnv -v`
Expected: PASS

- [ ] **Step 5: Replace os.Environ() in startProcess**

In `gateway.go`, line 465:

```go
// Before:
cmd.Env = append(os.Environ(), "OPSAGENT_PLUGIN_SOCKET="+socketPath)
// After:
cmd.Env = buildPluginEnv(socketPath, manifest.Env)
```

- [ ] **Step 6: Fix socket permissions in SDK serve.go**

In `sdk/plugin/serve.go`, after `net.Listen`:

```go
ln, err := net.Listen("unix", socketPath)
if err != nil {
    return fmt.Errorf("listen %s: %w", socketPath, err)
}
if err := os.Chmod(socketPath, 0o600); err != nil {
    ln.Close()
    return fmt.Errorf("chmod socket: %w", err)
}
```

- [ ] **Step 7: Run all pluginruntime tests**

Run: `go test ./internal/pluginruntime/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/pluginruntime/gateway.go sdk/plugin/serve.go internal/pluginruntime/security_test.go
git commit -m "fix(security): sanitize plugin env vars, set socket permissions to 0600"
```

---

## Phase 3: API/Auth Security

### Task 9: Auth Hot-Reload Protection

**Files:**
- Modify: `internal/server/reload.go`
- Modify: `internal/server/reload_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/server/reload_test.go`:

```go
func TestAuthReloaderRejectsDisable(t *testing.T) {
	s := &Server{}
	s.options.Auth.Enabled = true
	s.options.Auth.BearerToken = "01234567890123456789012345678901"

	r := NewAuthReloader(s)

	// Try to disable auth
	newCfg := &config.Config{}
	newCfg.Auth.Enabled = false
	newCfg.Auth.BearerToken = "01234567890123456789012345678901"

	err := r.Apply(newCfg)
	if err == nil {
		t.Error("expected error when disabling auth via hot-reload")
	}
}

func TestAuthReloaderRejectsEmptyToken(t *testing.T) {
	s := &Server{}
	s.options.Auth.Enabled = true
	s.options.Auth.BearerToken = "01234567890123456789012345678901"

	r := NewAuthReloader(s)

	newCfg := &config.Config{}
	newCfg.Auth.Enabled = true
	newCfg.Auth.BearerToken = ""

	err := r.Apply(newCfg)
	if err == nil {
		t.Error("expected error for empty bearer token")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run "TestAuthReloaderRejects" -v`
Expected: FAIL — currently allows disabling auth

- [ ] **Step 3: Update AuthReloader.Apply**

In `reload.go`:

```go
func (r *AuthReloader) Apply(newCfg *config.Config) error {
	if !newCfg.Auth.Enabled {
		return fmt.Errorf("auth cannot be disabled via hot-reload (restart required)")
	}
	if newCfg.Auth.BearerToken == "" {
		return fmt.Errorf("bearer_token cannot be empty")
	}
	if len(newCfg.Auth.BearerToken) < 32 {
		return fmt.Errorf("bearer_token must be at least 32 characters")
	}
	r.server.UpdateAuth(AuthConfig{
		Enabled:     newCfg.Auth.Enabled,
		BearerToken: newCfg.Auth.BearerToken,
	})
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -run "TestAuthReloaderRejects" -v`
Expected: PASS

- [ ] **Step 5: Run all server tests**

Run: `go test ./internal/server/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/server/reload.go internal/server/reload_test.go
git commit -m "fix(security): prevent auth from being disabled via hot-reload"
```

---

### Task 10: Security Headers + Rate Limiting + Method Restriction

**Files:**
- Modify: `internal/server/middleware.go`
- Modify: `internal/server/handlers.go`
- Modify: `internal/server/server_test.go`

- [ ] **Step 1: Write failing tests for security headers**

Add to `internal/server/server_test.go`:

```go
func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "DENY")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run "TestSecurityHeaders|TestHealthzRejectsPost" -v`
Expected: FAIL

- [ ] **Step 3: Add security headers middleware**

In `middleware.go`:

```go
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
```

Update `withMiddleware`:

```go
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return s.recoverMiddleware(s.securityHeadersMiddleware(s.loggingMiddleware(s.authMiddleware(next))))
}
```

- [ ] **Step 4: Add method checks to read-only endpoints**

In `handlers.go`, add method check to `handleHealthz`:

```go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}
	// ... existing code
}
```

Add same pattern to `handleReadyz` and `handleLatestMetrics`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/server/ -run "TestSecurityHeaders|TestHealthzRejectsPost" -v`
Expected: PASS

- [ ] **Step 6: Add timeout upper bound**

In `handlers.go`, update `handleTask`:

```go
const maxTimeoutSeconds = 300

timeoutSeconds := 15
if timeoutVal, ok := req.Payload["timeout_seconds"]; ok {
    if seconds, ok := parseTimeoutSeconds(timeoutVal); ok && seconds > 0 {
        timeoutSeconds = min(seconds, maxTimeoutSeconds)
    }
}
```

- [ ] **Step 7: Run all server tests**

Run: `go test ./internal/server/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/server/middleware.go internal/server/handlers.go internal/server/server_test.go
git commit -m "fix(security): add security headers, restrict HTTP methods, cap timeout at 300s"
```

---

### Task 11: Rate Limiting Middleware

**Files:**
- Modify: `internal/server/middleware.go`
- Modify: `internal/server/server_test.go`

- [ ] **Step 1: Write failing test for rate limiting**

Add to `internal/server/server_test.go`:

```go
func TestRateLimiting(t *testing.T) {
	s := newTestServer(t)

	// Send many requests rapidly
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, req)

		if i < 20 {
			// First 20 should succeed (burst)
			if w.Code == http.StatusTooManyRequests {
				t.Errorf("request %d got rate limited too early", i)
			}
		}
	}
	// The 25th should be rate limited
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/server/ -run TestRateLimiting -v`
Expected: FAIL — no rate limiting exists

- [ ] **Step 3: Implement rate limiting middleware**

In `middleware.go`:

```go
import (
	"sync"
	"golang.org/x/time/rate"
)

type rateLimiter struct {
	visitors map[string]*rate.Limiter
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
}

func newRateLimiter(r rate.Limit, burst int) *rateLimiter {
	return &rateLimiter{
		visitors: make(map[string]*rate.Limiter),
		rate:     r,
		burst:    burst,
	}
}

func (rl *rateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if lim, ok := rl.visitors[ip]; ok {
		return lim
	}
	lim := rate.NewLimiter(rl.rate, rl.burst)
	rl.visitors[ip] = lim
	return lim
}

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	rl := newRateLimiter(10, 20) // 10 req/s, burst of 20
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if !rl.getLimiter(ip).Allow() {
			writeJSON(w, http.StatusTooManyRequests, apiResponse{Success: false, Error: "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Update `withMiddleware` to include rate limiter:

```go
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return s.recoverMiddleware(s.securityHeadersMiddleware(s.rateLimitMiddleware(s.loggingMiddleware(s.authMiddleware(next)))))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/server/ -run TestRateLimiting -v`
Expected: PASS

- [ ] **Step 5: Add golang.org/x/time dependency**

Run: `go get golang.org/x/time/rate`

- [ ] **Step 6: Run all server tests**

Run: `go test ./internal/server/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/server/middleware.go internal/server/server_test.go go.mod go.sum
git commit -m "fix(security): add IP-based rate limiting middleware (10 req/s, burst 20)"
```

---

## Phase 4: Data/Config Security

### Task 12: Prometheus Tag Escaping + Default Bind Address

**Files:**
- Modify: `internal/collector/outputs/prometheus/prometheus.go`
- Modify: `internal/collector/outputs/prometheus/prometheus_test.go`

- [ ] **Step 1: Write failing tests for escapeLabelValue**

Add to `internal/collector/outputs/prometheus/prometheus_test.go`:

```go
func TestEscapeLabelValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "hello", "hello"},
		{"double quote", `foo"bar`, `foo\"bar`},
		{"backslash", `foo\bar`, `foo\\bar`},
		{"newline", "foo\nbar", `foo\nbar`},
		{"all special", "\"\\\n", "\"\\\\\\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeLabelValue(tt.input)
			if got != tt.want {
				t.Errorf("escapeLabelValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/collector/outputs/prometheus/ -run TestEscapeLabelValue -v`
Expected: FAIL — `escapeLabelValue` not defined

- [ ] **Step 3: Implement escapeLabelValue**

In `prometheus.go`:

```go
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/collector/outputs/prometheus/ -run TestEscapeLabelValue -v`
Expected: PASS

- [ ] **Step 5: Apply escapeLabelValue in renderPrometheus**

In `prometheus.go`, line 162:

```go
// Before:
sb.WriteString(tags[k])
// After:
sb.WriteString(escapeLabelValue(tags[k]))
```

- [ ] **Step 6: Change default bind address**

In `prometheus.go`, line 21:

```go
// Before:
defaultAddr = ":9100"
// After:
defaultAddr = "127.0.0.1:9100"
```

- [ ] **Step 7: Run all prometheus tests**

Run: `go test ./internal/collector/outputs/prometheus/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/collector/outputs/prometheus/prometheus.go internal/collector/outputs/prometheus/prometheus_test.go
git commit -m "fix(security): escape Prometheus label values, default bind to 127.0.0.1:9100"
```

---

### Task 13: Cache File Permissions + Config Diff Masking + ConfigUpdate Size Limit

**Files:**
- Modify: `internal/grpcclient/persist.go`
- Modify: `internal/grpcclient/client.go`
- Modify: `internal/config/diff.go`
- Modify: `internal/config/reload.go`
- Modify: `internal/config/diff_test.go`

- [ ] **Step 1: Write test for maskSecret**

Add to `internal/config/diff_test.go`:

```go
func TestMaskSecret(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"short", "abc", "***"},
		{"4 chars", "abcd", "***"},
		{"long", "abcdefghijklmnop", "ab***op"},
		{"empty", "", "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskSecret(tt.input)
			if got != tt.want {
				t.Errorf("maskSecret(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -run TestMaskSecret -v`
Expected: FAIL — `maskSecret` not defined

- [ ] **Step 3: Implement maskSecret and apply in diffGRPC**

In `diff.go`:

```go
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "***"
	}
	return s[:2] + "***" + s[len(s)-2:]
}
```

Update `diffGRPC` line 121:

```go
if old.GRPC.EnrollToken != new.GRPC.EnrollToken {
    changes = append(changes, NonReloadableChange{
        Field:  "grpc.enroll_token",
        OldVal: maskSecret(old.GRPC.EnrollToken),
        NewVal: maskSecret(new.GRPC.EnrollToken),
    })
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run TestMaskSecret -v`
Expected: PASS

- [ ] **Step 5: Fix cache file permissions**

In `persist.go` line 39:

```go
// Before:
return os.WriteFile(path, data, 0644)
// After:
return os.WriteFile(path, data, 0600)
```

In `client.go` line 505:

```go
// Before:
if err := os.WriteFile(persistPath, data, 0644); err != nil {
// After:
if err := os.WriteFile(persistPath, data, 0600); err != nil {
```

- [ ] **Step 6: Add ConfigUpdate size limit**

In `reload.go`:

```go
const maxConfigYAMLSize = 1 << 20 // 1 MB

func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error {
	if len(newYAML) > maxConfigYAMLSize {
		return fmt.Errorf("config YAML too large: %d bytes (max %d)", len(newYAML), maxConfigYAMLSize)
	}
	// ... existing code
}
```

- [ ] **Step 7: Run all config and grpcclient tests**

Run: `go test ./internal/config/ ./internal/grpcclient/ -v`
Expected: All PASS

- [ ] **Step 8: Commit**

```bash
git add internal/grpcclient/persist.go internal/grpcclient/client.go internal/config/diff.go internal/config/reload.go internal/config/diff_test.go
git commit -m "fix(security): mask secrets in config diff, 0600 file perms, ConfigUpdate size limit"
```

---

### Task 14: HTTP Output URL Validation + Health Info Disclosure

**Files:**
- Modify: `internal/collector/outputs/http/http.go`
- Modify: `internal/collector/outputs/http/http_test.go`
- Modify: `internal/server/handlers.go`

- [ ] **Step 1: Write failing test for URL validation**

Add to `internal/collector/outputs/http/http_test.go`:

```go
func TestInitURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid http", "http://localhost:8080/metrics", false},
		{"valid https", "https://example.com/metrics", false},
		{"file scheme", "file:///etc/passwd", true},
		{"gopher scheme", "gopher://evil.com", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HTTPOutput{}
			cfg := map[string]interface{}{"url": tt.url}
			err := h.Init(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Init with url %q: error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/collector/outputs/http/ -run TestInitURLValidation -v`
Expected: FAIL — currently accepts any URL scheme

- [ ] **Step 3: Add URL validation in Init**

In `http.go`, update `Init`:

```go
import "net/url"

func (h *HTTPOutput) Init(cfg map[string]interface{}) error {
	urlStr, ok := cfg["url"].(string)
	if !ok || urlStr == "" {
		return fmt.Errorf("http output: url is required")
	}
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("http output: invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("http output: url scheme must be http or https, got %q", parsed.Scheme)
	}
	h.url = urlStr
	// ... rest unchanged
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/collector/outputs/http/ -run TestInitURLValidation -v`
Expected: PASS

- [ ] **Step 5: Add health endpoint auth check for version info**

In `handlers.go`, update `handleHealthz`:

```go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
		return
	}

	subsystems := make(map[string]any)
	overallStatus := "healthy"
	// ... existing subsystem checks ...

	uptime := int(time.Since(s.startedAt).Seconds())
	data := map[string]any{
		"status":     overallStatus,
		"subsystems": subsystems,
	}

	// Only expose version info when auth is enabled and request is authenticated
	if s.options.Auth.Enabled {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		expected := "Bearer " + s.options.Auth.BearerToken
		if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) == 1 {
			data["version"] = s.version
			data["git_commit"] = s.gitCommit
			data["uptime_seconds"] = uptime
		}
	}

	writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: data})
}
```

Note: add `"crypto/subtle"` and `"strings"` to imports.

- [ ] **Step 6: Run all tests**

Run: `go test ./internal/collector/outputs/http/ ./internal/server/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/collector/outputs/http/http.go internal/collector/outputs/http/http_test.go internal/server/handlers.go
git commit -m "fix(security): validate HTTP output URL scheme, hide version info from unauthenticated health endpoint"
```

---

### Task 15: Rust Runtime Security Fixes

**Files:**
- Modify: `rust-runtime/src/handlers/fs_scan.rs`
- Modify: `rust-runtime/src/handlers/local_probe.rs`
- Modify: `rust-runtime/src/main.rs`
- Modify: `rust-runtime/Cargo.toml` (if nix crate needed)

- [ ] **Step 1: Add path allowlist to FsScanPlugin**

In `fs_scan.rs`, add at top:

```rust
const ALLOWED_ROOTS: &[&str] = &["/var/log", "/opt", "/srv", "/tmp"];
```

In `execute` method, after getting `root_path`:

```rust
let canonical = std::fs::canonicalize(root_path)
    .map_err(|_| PluginError::Config(format!("root_path does not exist or is not accessible: {}", root_path)))?;
let is_allowed = ALLOWED_ROOTS.iter().any(|allowed| canonical.starts_with(allowed));
if !is_allowed {
    return Err(PluginError::Config(format!(
        "root_path {} is not under allowed roots: {:?}", root_path, ALLOWED_ROOTS
    )));
}
```

Also add `.follow_links(false)` to the `WalkDir` builder.

- [ ] **Step 2: Add existing test for allowed path**

The existing tests use `TempDir` which creates under `/tmp` — this is in the allowlist, so they should still pass.

- [ ] **Step 3: Limit dmesg output in local_probe.rs**

In `local_probe.rs`, update `check_oom_killer`:

```rust
fn check_oom_killer(_lookback_minutes: u64) -> Result<(Status, String), PluginError> {
    match std::process::Command::new("dmesg")
        .arg("--time-format=iso")
        .output()
    {
        Ok(output) => {
            let stdout = String::from_utf8_lossy(&output.stdout);
            // Only check last 64KB to avoid excessive memory use
            let truncated = if stdout.len() > 65536 {
                &stdout[stdout.len() - 65536..]
            } else {
                &stdout
            };
            let oom_count = truncated.matches("Out of memory").count();
            if oom_count > 0 {
                Ok((Status::Fail, format!("{} OOM events found", oom_count)))
            } else {
                Ok((Status::Pass, "no OOM events".into()))
            }
        }
        Err(_) => Ok((Status::Pass, "dmesg unavailable, skipped".into())),
    }
}
```

- [ ] **Step 4: Remove insecure default socket path in main.rs**

In `main.rs`:

```rust
// Before:
let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
    .unwrap_or_else(|_| "/tmp/opsagent/plugin.sock".to_string());
// After:
let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
    .expect("OPSAGENT_PLUGIN_SOCKET environment variable must be set");
```

- [ ] **Step 5: Run Rust tests**

Run: `cd rust-runtime && cargo test`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add rust-runtime/src/handlers/fs_scan.rs rust-runtime/src/handlers/local_probe.rs rust-runtime/src/main.rs
git commit -m "fix(security): add FsScan path allowlist, limit dmesg output, require socket env var"
```

---

### Task 16: Systemd NoNewPrivileges

**Files:**
- Modify: `scripts/ci-package.sh`

- [ ] **Step 1: Change NoNewPrivileges**

In `scripts/ci-package.sh`, line 56:

```bash
# Before:
NoNewPrivileges=false
# After:
NoNewPrivileges=true
```

- [ ] **Step 2: Commit**

```bash
git add scripts/ci-package.sh
git commit -m "fix(security): set NoNewPrivileges=true in systemd service unit"
```

---

## Final Verification

- [ ] **Run all Go tests:**

```bash
go test ./... -race -count=1
```

- [ ] **Run all Rust tests:**

```bash
cd rust-runtime && cargo test
```

- [ ] **Run go vet:**

```bash
go vet ./...
```

- [ ] **Run Rust clippy:**

```bash
cd rust-runtime && cargo clippy -- -D warnings
```

- [ ] **Verify no regressions in existing tests**

All previously passing tests must still pass.
