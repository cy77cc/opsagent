# Security Hardening Design

**Date:** 2026-05-01
**Scope:** 33 findings from security code review (7 CRITICAL, 10 HIGH, 10 MEDIUM, 6 LOW)
**Goal:** Fix all findings across sandbox, plugin, API/auth, and data subsystems

---

## Phase 1: Sandbox Subsystem

### 1.1 TaskID Path Traversal (C1)

**Files:** `internal/sandbox/nsjail.go:128-154`, `stats.go:87`

**Problem:** TaskID is used directly in file paths without sanitization. `../../etc/cron.d/evil` as TaskID writes outside intended directories.

**Fix:** Add `sanitizeTaskID` function that rejects any TaskID containing path separators or `..` sequences. Apply in `WriteScriptFile`, `WriteConfigFile`, and `CreateCgroup`.

```go
func sanitizeTaskID(taskID string) (string, error) {
    cleaned := filepath.Clean(taskID)
    if cleaned == ".." || strings.ContainsAny(cleaned, `/\`) {
        return "", fmt.Errorf("invalid task ID: %q contains path traversal", taskID)
    }
    if cleaned == "" {
        return "", fmt.Errorf("task ID is required")
    }
    return cleaned, nil
}
```

Apply in each function before constructing paths:
```go
taskID, err := sanitizeTaskID(taskID)
if err != nil {
    return "", err
}
```

### 1.2 iptables IP Injection (C2)

**File:** `internal/sandbox/network.go:48-51`

**Problem:** User-controlled `allowedIPs` passed directly to iptables without validation.

**Fix:** Validate each IP/CIDR with `net.ParseIP()` or strict CIDR regex before passing to iptables.

```go
import "net"

func validateIPList(ips []string) error {
    for _, ip := range ips {
        // Handle CIDR notation
        if strings.Contains(ip, "/") {
            _, _, err := net.ParseCIDR(ip)
            if err != nil {
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

Call `validateIPList(allowedIPs)` at the start of `SetupAllowlistNetwork`.

### 1.3 Seccomp Network Syscall Bypass (C3)

**File:** `internal/sandbox/nsjail.go:12-31,86-96`

**Problem:** Seccomp whitelist includes network syscalls regardless of NetworkMode.

**Fix:** Make seccomp policy dynamic. Split into base policy and network policy. Generate `seccompPolicyString` method on `NsjailConfig` that excludes network syscalls when `NetworkMode == "disabled"`.

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

const networkSyscalls = `
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

Replace `ToArgs` line 86 with:
```go
args = append(args, fmt.Sprintf("--seccomp_policy_string=%s", c.seccompPolicyString()))
```

### 1.4 Fork Bomb via clone/fork/vfork (C4)

**File:** `internal/sandbox/nsjail.go:16`

**Problem:** Seccomp allows `clone`, `fork`, `vfork` without cgroup pids limit.

**Fix:** Remove `clone, fork, vfork` from seccomp. nsjail uses `clone` internally to set up the namespace, but that happens before the seccomp filter is applied to the child. The child only needs `execve`.

Change line 16 from:
```
clone, fork, vfork, execve, exit, wait4, kill, uname,
```
to:
```
execve, exit, wait4, kill, uname,
```

### 1.5 Sandbox /etc Mount (H3)

**File:** `internal/sandbox/nsjail.go:75-77`

**Problem:** Mounting `/etc` read-only exposes `passwd`, `hosts`, etc.

**Fix:** Remove `/etc` from the bind mount list. Instead, create a minimal `/etc` inside the sandbox tmpfs with only `resolv.conf` (for DNS) and a minimal `passwd` file.

```go
// Remove "/etc" from the bindmount list
for _, dir := range []string{"/usr", "/lib", "/lib64", "/bin"} {
    args = append(args, fmt.Sprintf("--bindmount_ro=%s", dir))
}

// Create minimal /etc via additional tmpfs mounts
args = append(args, "--tmpfsmount=/etc:tmpfs:size=1048576")
// Copy resolv.conf if needed for network mode
```

### 1.6 Sandbox Resource Override Bounds (M5)

**File:** `internal/sandbox/executor.go:310-328`

**Problem:** SandboxOverride allows unbounded resource escalation.

**Fix:** Add upper-bound validation in `buildNsjailConfig`:

```go
if req.SandboxCfg != nil {
    if req.SandboxCfg.MemoryMB > 0 {
        cfg.MemoryMB = min(req.SandboxCfg.MemoryMB, 1024) // cap at 1GB
    }
    if req.SandboxCfg.CPUPercent > 0 {
        cfg.CPUPercent = min(req.SandboxCfg.CPUPercent, 100)
    }
    if req.SandboxCfg.MaxPIDs > 0 {
        cfg.MaxPIDs = min(req.SandboxCfg.MaxPIDs, 256)
    }
    // NetworkMode override only allowed if global config permits
    if req.SandboxCfg.NetworkMode != "" && e.cfg.NetworkMode != "disabled" {
        cfg.NetworkMode = req.SandboxCfg.NetworkMode
    }
}
```

### 1.7 Command Name Metacharacter Check (M8)

**File:** `internal/sandbox/policy.go:22`

**Problem:** `ValidateCommand` checks args for shell metacharacters but not the command name itself.

**Fix:** Add metacharacter check for `cmdName` after trimming:

```go
cmdName := strings.TrimSpace(command)
if cmdName == "" {
    return fmt.Errorf("command is required")
}
if containsShellMetacharacters(cmdName) {
    return fmt.Errorf("command name contains shell metacharacters: %q", cmdName)
}
```

### 1.8 Audit Log Permissions (H8 partial)

**File:** `internal/sandbox/audit.go:40`

**Fix:** Change `0o644` to `0o600`.

### 1.9 Predictable Script Paths (L1)

**File:** `internal/sandbox/nsjail.go:128-138`

**Fix:** Use `os.CreateTemp` instead of predictable paths:

```go
func (c *NsjailConfig) WriteScriptFile(taskID, scriptContent string) (string, error) {
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
    f.Close()
    return f.Name(), nil
}
```

Apply same pattern to `WriteConfigFile`.

---

## Phase 2: Plugin Subsystem

### 2.1 Plugin Binary Path Traversal (C6)

**File:** `internal/pluginruntime/manifest.go:87-90`

**Problem:** `BinaryPath` resolved without directory boundary check.

**Fix:** After resolving the path, verify it stays within the manifest's directory:

```go
func LoadManifest(path string) (*PluginManifest, error) {
    // ... existing code ...
    m.resolvedDir = filepath.Dir(path)
    if !filepath.IsAbs(m.BinaryPath) {
        m.BinaryPath = filepath.Join(m.resolvedDir, m.BinaryPath)
    }
    // Canonicalize and verify boundary
    cleanBin := filepath.Clean(m.BinaryPath)
    cleanDir := filepath.Clean(m.resolvedDir)
    if !strings.HasPrefix(cleanBin, cleanDir+string(filepath.Separator)) {
        return nil, fmt.Errorf("binary_path %q escapes plugin directory %q", m.BinaryPath, m.resolvedDir)
    }
    m.BinaryPath = cleanBin
    return m, nil
}
```

### 2.2 Plugin Sandbox (C5)

**File:** `internal/pluginruntime/gateway.go:459-483`

**Problem:** Plugins run with full host privileges, no sandbox.

**Fix:** When `manifest.Sandbox.Enabled == true`, launch the plugin through nsjail. Wire up the existing `SandboxConfig` fields.

```go
func startProcess(manifest *PluginManifest, socketPath string) (*ManagedProcess, error) {
    if _, err := os.Stat(manifest.BinaryPath); err != nil {
        return nil, fmt.Errorf("plugin binary not found: %w", err)
    }

    var cmd *exec.Cmd
    if manifest.Sandbox != nil && manifest.Sandbox.Enabled {
        // Use nsjail for sandboxed plugins
        nsCfg := NsjailConfig{
            MemoryMB:   128,
            CPUPercent: 50,
            MaxPIDs:    32,
            NetworkMode: "disabled",
        }
        if manifest.Sandbox.NetworkAccess {
            nsCfg.NetworkMode = "allowlist"
        }
        args := nsCfg.CommandArgs(manifest.Name, manifest.BinaryPath, nil)
        cmd = exec.Command("nsjail", args...)
    } else {
        cmd = exec.Command(manifest.BinaryPath)
    }

    cmd.Env = buildPluginEnv(socketPath, manifest.Env)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    // ... rest unchanged
}
```

### 2.3 Plugin Environment Sanitization (H1)

**File:** `internal/pluginruntime/gateway.go:465`

**Fix:** Build a minimal environment instead of forwarding `os.Environ()`:

```go
func buildPluginEnv(socketPath string, manifestEnv map[string]string) []string {
    env := []string{
        "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "OPSAGENT_PLUGIN_SOCKET=" + socketPath,
    }
    for k, v := range manifestEnv {
        // Block dangerous env vars
        if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "DYLD_INSERT_LIBRARIES" {
            continue
        }
        env = append(env, k+"="+v)
    }
    return env
}
```

### 2.4 Socket Path Injection (H2)

**File:** `internal/pluginruntime/gateway.go:327`

**Fix:** Sanitize plugin name and use a secure socket directory:

```go
var validPluginName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func sanitizePluginName(name string) (string, error) {
    if !validPluginName.MatchString(name) {
        return "", fmt.Errorf("invalid plugin name %q: must be alphanumeric with hyphens/underscores", name)
    }
    return name, nil
}
```

In `loadPlugin`:
```go
safeName, err := sanitizePluginName(manifest.Name)
if err != nil {
    return err
}
socketDir := filepath.Join(os.TempDir(), "opsagent-plugins")
os.MkdirAll(socketDir, 0o700)
socketPath := filepath.Join(socketDir, safeName+".sock")
```

### 2.5 Socket Permissions (M4)

**File:** `sdk/plugin/serve.go:69`

**Fix:** Set socket permissions after creation:

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

### 2.6 Symlink Attack on Socket (H10)

**File:** `internal/pluginruntime/gateway.go:327-328`

**Fix:** Before creating the socket, check if the path is a symlink:

```go
if info, err := os.Lstat(socketPath); err == nil {
    if info.Mode()&os.ModeSymlink != 0 {
        return fmt.Errorf("socket path %q is a symlink, refusing to overwrite", socketPath)
    }
}
_ = os.Remove(socketPath)
```

---

## Phase 3: API/Auth Subsystem

### 3.1 Auth Hot-Reload Disable (H4)

**File:** `internal/server/reload.go:23-29`, `internal/config/diff.go:82-84`

**Problem:** `AuthReloader.Apply` allows disabling auth via hot-reload.

**Fix:** In `AuthReloader.Apply`, reject changes that disable auth or change the token to empty:

```go
func (r *AuthReloader) Apply(newCfg *config.Config) error {
    // Never allow disabling auth via hot-reload
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

### 3.2 Security Headers (M2)

**File:** `internal/server/middleware.go`

**Fix:** Add security headers middleware:

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

### 3.3 Rate Limiting (H7)

**File:** `internal/server/middleware.go`

**Fix:** Add IP-based rate limiter using `golang.org/x/time/rate`:

```go
type rateLimiter struct {
    visitors map[string]*rate.Limiter
    mu       sync.Mutex
    rate     rate.Limit
    burst    int
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
    rl := &rateLimiter{
        visitors: make(map[string]*rate.Limiter),
        rate:     rate.Limit(10), // 10 requests per second
        burst:    20,
    }
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

### 3.4 HTTP Method Restriction (L2/L4)

**File:** `internal/server/handlers.go:21-30`

**Fix:** Use `http.MethodGet` for read-only endpoints:

```go
func (s *Server) registerRoutes(mux *http.ServeMux) {
    mux.HandleFunc("/healthz", s.handleHealthz)
    mux.HandleFunc("/readyz", s.handleReadyz)
    mux.HandleFunc("/api/v1/metrics/latest", s.handleLatestMetrics)
    mux.HandleFunc("/api/v1/exec", s.handleExec)
    mux.HandleFunc("/api/v1/tasks", s.handleTask)
    // ...
}
```

Add method checks to `handleHealthz`, `handleReadyz`, `handleLatestMetrics`:
```go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
        return
    }
    // ... existing code
}
```

### 3.5 Timeout Upper Bound (M6 partial)

**File:** `internal/server/handlers.go:137-142`

**Fix:** Add max timeout validation:

```go
const maxTimeoutSeconds = 300

timeoutSeconds := 15
if timeoutVal, ok := req.Payload["timeout_seconds"]; ok {
    if seconds, ok := parseTimeoutSeconds(timeoutVal); ok && seconds > 0 {
        timeoutSeconds = min(seconds, maxTimeoutSeconds)
    }
}
```

---

## Phase 4: Data/Config Subsystem

### 4.1 Prometheus Tag Value Injection (C7)

**File:** `internal/collector/outputs/prometheus/prometheus.go:161-163`

**Fix:** Add `escapeLabelValue` function and apply to tag values:

```go
func escapeLabelValue(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `"`, `\"`)
    s = strings.ReplaceAll(s, "\n", `\n`)
    return s
}
```

In `renderPrometheus`, replace:
```go
sb.WriteString(tags[k])
```
with:
```go
sb.WriteString(escapeLabelValue(tags[k]))
```

### 4.2 Environment Variable Leak to Sandbox (H1 sandbox)

**File:** `internal/sandbox/executor.go:224`

**Fix:** Build minimal env for sandbox:

```go
cmd.Env = []string{
    "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
    "HOME=/tmp",
    "LANG=C",
}
for k, v := range req.Env {
    // Block dangerous vars
    if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" {
        continue
    }
    cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
}
```

### 4.3 Cache File Permissions (H8)

**Files:** `internal/grpcclient/persist.go:39`, `internal/grpcclient/client.go:505`

**Fix:** Change `0644` to `0600`:

```go
return os.WriteFile(path, data, 0600)
```

### 4.4 Config Diff Sensitive Value Masking (M1)

**File:** `internal/config/diff.go:120-121`

**Fix:** Mask sensitive fields in `NonReloadableChange`:

```go
func diffGRPC(old, new *Config) []NonReloadableChange {
    var changes []NonReloadableChange
    // ... other fields ...
    if old.GRPC.EnrollToken != new.GRPC.EnrollToken {
        changes = append(changes, NonReloadableChange{
            Field:  "grpc.enroll_token",
            OldVal: maskSecret(old.GRPC.EnrollToken),
            NewVal: maskSecret(new.GRPC.EnrollToken),
        })
    }
    // ...
}

func maskSecret(s string) string {
    if len(s) <= 4 {
        return "***"
    }
    return s[:2] + "***" + s[len(s)-2:]
}
```

### 4.5 FsScan Path Allowlist (H5)

**File:** `rust-runtime/src/handlers/fs_scan.rs:38-48`

**Fix:** Add path allowlist and disable symlink following:

```rust
const ALLOWED_ROOTS: &[&str] = &["/var/log", "/opt", "/srv", "/tmp"];

async fn execute(&self, payload: &Value) -> Result<PluginResult, PluginError> {
    let root_path = payload
        .get("root_path")
        .and_then(|v| v.as_str())
        .ok_or_else(|| PluginError::Config("root_path is required".into()))?;

    // Validate path is under allowed roots
    let canonical = std::fs::canonicalize(root_path)
        .map_err(|e| PluginError::Config(format!("invalid root_path: {}", e)))?;
    let is_allowed = ALLOWED_ROOTS.iter().any(|allowed| canonical.starts_with(allowed));
    if !is_allowed {
        return Err(PluginError::Config(format!(
            "root_path {} is not under allowed roots: {:?}", root_path, ALLOWED_ROOTS
        )));
    }

    // ... rest of scan logic, with follow_links(false):
    for entry in WalkDir::new(root_path)
        .max_depth(max_depth)
        .follow_links(false)  // Don't follow symlinks
        .into_iter()
    // ...
}
```

### 4.6 dmesg Output Limiting (H9)

**File:** `rust-runtime/src/handlers/local_probe.rs:133-149`

**Fix:** Limit dmesg output size and avoid reading full kernel log:

```rust
fn check_oom_killer(_lookback_minutes: u64) -> Result<(Status, String), PluginError> {
    match std::process::Command::new("dmesg")
        .arg("--time-format=iso")
        .arg("-T")
        .output()
    {
        Ok(output) => {
            // Only check last 64KB of output
            let stdout = String::from_utf8_lossy(&output.stdout);
            let truncated = if stdout.len() > 65536 {
                &stdout[stdout.len() - 65536..]
            } else {
                &stdout
            };
            let oom_count = truncated.matches("Out of memory").count();
            // ...
        }
        Err(_) => Ok((Status::Pass, "dmesg unavailable, skipped".into())),
    }
}
```

### 4.7 Prometheus Default Bind Address (M10)

**File:** `internal/collector/outputs/prometheus/prometheus.go:21`

**Fix:** Change default from `:9100` to `127.0.0.1:9100`:

```go
const defaultAddr = "127.0.0.1:9100"
```

### 4.8 ConfigUpdate Size Limit (M1)

**File:** `internal/config/reload.go:61`

**Fix:** Add size check before parsing:

```go
const maxConfigYAMLSize = 1 << 20 // 1 MB

func (r *ConfigReloader) Apply(ctx context.Context, newYAML []byte) error {
    if len(newYAML) > maxConfigYAMLSize {
        return fmt.Errorf("config YAML too large: %d bytes (max %d)", len(newYAML), maxConfigYAMLSize)
    }
    // ... existing code
}
```

### 4.9 HTTP Output URL Validation (M3)

**File:** `internal/collector/outputs/http/http.go:52-56`

**Fix:** Validate URL scheme and host:

```go
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

### 4.10 Health Endpoint Info Disclosure (L2)

**File:** `internal/server/handlers.go:69-78`

**Fix:** Only expose version/commit when auth is enabled and request is authenticated:

```go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
    // ... subsystem checks ...
    data := map[string]any{
        "status":     overallStatus,
        "subsystems": subsystems,
    }
    // Only include version info if authenticated
    if s.options.Auth.Enabled && isAuthenticated(r) {
        data["version"] = s.version
        data["git_commit"] = s.gitCommit
        data["uptime_seconds"] = uptime
    }
    writeJSON(w, http.StatusOK, apiResponse{Success: true, Data: data})
}
```

### 4.11 Rust Runtime Socket Security (L4/L5)

**File:** `rust-runtime/src/main.rs:29-42`

**Fix:** Remove insecure default, add peer credential check:

```rust
let socket_path = std::env::var("OPSAGENT_PLUGIN_SOCKET")
    .expect("OPSAGENT_PLUGIN_SOCKET environment variable must be set");

// After accept, verify peer credentials
loop {
    let (stream, _addr) = listener.accept().await?;
    // Verify peer is root or same user
    let cred = stream.peer_cred()?;
    let my_uid = nix::unistd::getuid();
    if cred.uid() != my_uid && cred.uid() != 0 {
        tracing::warn!(peer_uid = ?cred.uid(), "rejecting connection from unauthorized peer");
        continue;
    }
    // ... handle connection
}
```

### 4.12 Systemd NoNewPrivileges (L6)

**File:** `scripts/ci-package.sh:56`

**Fix:** Change to `NoNewPrivileges=true`.

---

## Phase 5: Cross-cutting Concerns

### 5.1 Sandbox Override NetworkMode Restriction

When global `NetworkMode == "disabled"`, per-request overrides must not be able to enable networking. This is handled in Phase 1.6.

### 5.2 File Permission Summary

| File | Current | Fixed |
|------|---------|-------|
| `audit.go:40` | `0644` | `0600` |
| `persist.go:39` | `0644` | `0600` |
| `client.go:505` | `0644` | `0600` |
| `serve.go:69` (socket) | umask default | `0600` |

### 5.3 Env Sanitization Summary

| Location | Current | Fixed |
|----------|---------|-------|
| `sandbox/executor.go:224` | `os.Environ()` | Minimal allowlist |
| `pluginruntime/gateway.go:465` | `os.Environ()` | Minimal allowlist |
| `pluginruntime/runtime.go:102` | `os.Environ()` | Minimal allowlist |

---

## Implementation Order

1. **Sandbox security** (C1, C2, C3, C4, H3, M5, M8, L1) - highest risk, direct RCE vectors
2. **Plugin security** (C5, C6, H1 plugin, H2, H10, M4) - second highest risk
3. **API/Auth security** (H4, H7, M2, M6, L2) - auth bypass and DoS
4. **Data security** (C7, H1 sandbox, H5, H8, H9, M1, M3, M10, L4, L5, L6) - injection and info leak

Each phase produces a testable, reviewable PR.

---

## Testing Strategy

For each fix:
1. **Unit test** the new validation function (sanitizeTaskID, validateIPList, escapeLabelValue, etc.)
2. **Table-driven tests** for edge cases (empty strings, path traversal variants, special chars)
3. **Integration test** that verifies the fix blocks the attack vector
4. **Existing tests pass** - no regressions

Key test cases:
- `sanitizeTaskID("../../etc/passwd")` returns error
- `validateIPList(["0.0.0.0/0 -j ACCEPT"])` returns error
- `escapeLabelValue("foo\"bar\n")` returns `"foo\\\"bar\\n"`
- `LoadManifest` with `binary_path: "../../bin/evil"` returns error
- `AuthReloader.Apply` with `auth.enabled: false` returns error
