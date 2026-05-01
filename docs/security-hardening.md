# OpsAgent 安全加固手册

本文档全面记录 OpsAgent 的安全架构与加固措施。OpsAgent 是一个宿主机侧的指标采集与沙箱执行代理，采用纵深防御策略，在网络层、进程层、文件系统层和 API 层均实施了安全控制。

---

## 1. 安全架构概述

OpsAgent 采用纵深防御（Defense-in-Depth）策略，在多个层面实施安全控制：

| 层级 | 安全措施 |
|------|----------|
| **网络层** | TLS 1.2+ 强制、本地回环绑定、网络隔离（nsjail net namespace） |
| **进程层** | nsjail 命名空间隔离、seccomp 系统调用白名单、cgroup v2 资源限制 |
| **文件系统层** | 路径穿越防护、文件权限控制（0600）、不可预测临时路径 |
| **API 层** | Bearer Token 认证、速率限制、输入验证、安全响应头 |

---

## 2. 沙箱安全（Sandbox）

沙箱子系统基于 nsjail 实现，提供多层隔离保护。沙箱默认关闭，需在配置中显式启用。

### 2.1 nsjail 隔离

nsjail 提供以下命名空间隔离：

- **PID 命名空间**：沙箱内进程无法看到或信号宿主机进程
- **NET 命名空间**：默认禁用网络访问（`NetworkMode: "disabled"`），可选白名单模式
- **MNT 命名空间**：只读绑定挂载 `/usr`、`/lib`、`/lib64`、`/bin`，`/etc` 使用 tmpfs 替代

**UID/GID 映射**：沙箱内以 nobody（65534）身份运行：

```go
// internal/sandbox/nsjail.go
args = append(args,
    "--uid_mapping=0:65534:1",
    "--gid_mapping=0:65534:1",
)
```

**seccomp 系统调用白名单**（动态策略：基础 + 网络）：

```go
// internal/sandbox/nsjail.go
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
```

**Fork 炸弹防护**：`clone`、`fork`、`vfork` 被有意排除在白名单之外，防止 fork 炸弹攻击。测试用例验证了这一点：

```go
// internal/sandbox/sanitize_test.go
if strings.Contains(policy, "clone") || strings.Contains(policy, "fork") || strings.Contains(policy, "vfork") {
    t.Errorf("seccomp policy should not allow clone/fork/vfork")
}
```

**网络系统调用**仅在网络模式为 `allowlist` 时追加：

```go
// internal/sandbox/nsjail.go
const networkSyscalls = `,
    socket, connect, bind, listen, accept, accept4,
    sendto, recvfrom, sendmsg, recvmsg, shutdown,
    setsockopt, getsockopt, socketpair, eventfd2`
```

### 2.2 cgroup v2 资源限制

每个沙箱任务创建独立 cgroup，实施以下资源限制：

| 资源 | 默认限制 | 配置字段 |
|------|----------|----------|
| 内存上限 | 128 MB | `memory.max` |
| CPU 配额 | 50% | `cpu.max`（格式：`$MAX $PERIOD`） |
| 进程数上限 | 32 | `pids.max` |

资源限制通过 cgroup v2 文件接口写入：

```go
// internal/sandbox/stats.go
func SetCgroupLimits(cgroupPath string, memoryMB, cpuPercent, maxPIDs int) error {
    if memoryMB > 0 {
        memBytes := memoryMB * 1024 * 1024
        os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.Itoa(memBytes)), 0o644)
    }
    if cpuPercent > 0 {
        period := 100000
        max := cpuPercent * period / 100
        val := fmt.Sprintf("%d %d", max, period)
        os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(val), 0o644)
    }
    if maxPIDs > 0 {
        os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(strconv.Itoa(maxPIDs)), 0o644)
    }
    return nil
}
```

**资源边界验证**：请求级覆盖设有硬上限，防止资源滥用：

```go
// internal/sandbox/executor.go
if req.SandboxCfg.MemoryMB > 0 {
    cfg.MemoryMB = min(req.SandboxCfg.MemoryMB, 1024)   // 最大 1GB
}
if req.SandboxCfg.CPUPercent > 0 {
    cfg.CPUPercent = min(req.SandboxCfg.CPUPercent, 100) // 最大 100%
}
if req.SandboxCfg.MaxPIDs > 0 {
    cfg.MaxPIDs = min(req.SandboxCfg.MaxPIDs, 256)       // 最大 256 个进程
}
```

任务执行完成后，cgroup 中的所有进程会被终止，cgroup 目录被清理：

```go
// internal/sandbox/executor.go
defer func() {
    KillCgroupProcesses(cgroupPath)
    RemoveCgroup(cgroupPath)
}()
```

### 2.3 安全策略引擎

策略引擎位于 `internal/sandbox/policy.go`，对命令和脚本执行多层验证。

**命令验证流程**（`ValidateCommand`）：

1. **Shell 元字符检测**：命令名中不得包含以下危险字符：
   ```go
   // internal/sandbox/policy.go
   var shellMetachars = []string{
       ";", "&&", "||", "|", "`", "$(", "${", ">", "<",
       "\n", "'", "\"", "\\", "*", "?", "#", "~",
   }
   ```
2. **黑名单检查**：`blocked_commands` 中的命令始终被拒绝（优先级最高）
3. **白名单检查**：若 `allowed_commands` 非空，命令必须在白名单中
4. **参数元字符检查**：所有命令参数同样经过元字符检测
5. **Sudo 拦截**：除非显式设置 `allow_sudo: true`，否则拒绝 sudo

**脚本验证流程**（`ValidateScript`）：

1. **解释器白名单**：脚本解释器必须在 `allowed_interpreters` 中
2. **脚本大小限制**：默认 64KB（`script_max_bytes: 65536`），最大 1MB
3. **关键字黑名单**（不区分大小写）：脚本内容不得包含 `blocked_keywords` 中的关键字

**未知解释器拒绝**：解释器名称映射到绝对路径，未知名称直接报错：

```go
// internal/sandbox/nsjail.go
func interpreterToPath(interpreter string) (string, error) {
    switch interpreter {
    case "bash":  return "/bin/bash", nil
    case "sh":    return "/bin/sh", nil
    case "python3": return "/usr/bin/python3", nil
    // ...
    default:
        return "", fmt.Errorf("unsupported interpreter %q", interpreter)
    }
}
```

### 2.4 输入验证

**TaskID 路径穿越防护**：所有入口点均对 TaskID 进行净化，防止路径穿越攻击：

```go
// internal/sandbox/nsjail.go
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
```

此函数应用于：
- nsjail 参数构建（`ToArgs`、`CommandArgs`、`ScriptArgs`）
- 脚本文件写入（`WriteScriptFile`）
- 配置文件写入（`WriteConfigFile`）
- cgroup 创建（`CreateCgroup`）
- 网络管理（`SetupAllowlistNetwork`、`CleanupNetwork`）

**IP 地址验证**：在创建 iptables 规则前验证 IP 地址，防止命令注入：

```go
// internal/sandbox/network.go
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

**命令名元字符检查**：即使命令在白名单中，仍检查其是否包含 shell 元字符：

```go
// internal/sandbox/policy.go
func (p *Policy) ValidateCommand(command string, args []string) error {
    cmdName := strings.TrimSpace(command)
    if containsShellMetacharacters(cmdName) {
        return fmt.Errorf("command name contains shell metacharacters: %q", cmdName)
    }
    // ... 后续检查
}
```

### 2.5 环境变量安全

沙箱和插件进程使用最小化环境变量，阻止库注入攻击：

```go
// internal/sandbox/executor.go
func buildSandboxEnv(reqEnv map[string]string) []string {
    env := []string{
        "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "HOME=/tmp",
        "LANG=C",
    }
    for k, v := range reqEnv {
        if k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" || k == "DYLD_INSERT_LIBRARIES" {
            continue  // 阻止动态库注入
        }
        env = append(env, k+"="+v)
    }
    return env
}
```

**阻止的环境变量**：
- `LD_PRELOAD`：Linux 动态库预加载注入
- `LD_LIBRARY_PATH`：动态库搜索路径劫持
- `DYLD_INSERT_LIBRARIES`：macOS 动态库注入

插件环境变量同样适用相同过滤规则：

```go
// internal/pluginruntime/gateway.go
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

---

## 3. API 安全

### 3.1 认证

API 认证基于 Bearer Token，使用常量时间比较防止时序攻击：

```go
// internal/server/middleware.go
func (s *Server) authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !s.requiresAuth(r.URL.Path) {
            next.ServeHTTP(w, r)
            return
        }
        auth := strings.TrimSpace(r.Header.Get("Authorization"))
        expected := "Bearer " + s.options.Auth.BearerToken
        if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) != 1 {
            writeJSON(w, http.StatusUnauthorized, apiResponse{Success: false, Error: "unauthorized"})
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**认证默认启用**：

```go
// internal/config/config.go
v.SetDefault("auth.enabled", true)
```

**最小 Token 长度要求**（32 字符）：

```go
// internal/config/config.go
if c.Auth.Enabled {
    token := strings.TrimSpace(c.Auth.BearerToken)
    if token == "" {
        return fmt.Errorf("auth.bearer_token is required when auth.enabled=true")
    }
    if len(token) < 32 {
        return fmt.Errorf("auth.bearer_token must be at least 32 characters when auth.enabled=true")
    }
}
```

**热重载禁止关闭认证**：

```go
// internal/server/reload.go
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
    // ... 应用更新
}
```

**版本信息隐藏**：未认证请求的健康检查端点不暴露版本信息：

```go
// internal/server/handlers.go
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
    // ...
    data := map[string]any{
        "status":     overallStatus,
        "subsystems": subsystems,
    }
    // 仅在认证启用且请求已认证时暴露版本信息
    if s.options.Auth.Enabled {
        auth := strings.TrimSpace(r.Header.Get("Authorization"))
        expected := "Bearer " + s.options.Auth.BearerToken
        if subtle.ConstantTimeCompare([]byte(auth), []byte(expected)) == 1 {
            data["version"] = s.version
            data["git_commit"] = s.gitCommit
            data["uptime_seconds"] = int(time.Since(s.startedAt).Seconds())
        }
    }
    // ...
}
```

需要认证的路径：
- 所有 `/api/v1/` 前缀的端点
- Prometheus 指标端点（当 `protect_with_auth: true` 时）

```go
// internal/server/middleware.go
func (s *Server) requiresAuth(path string) bool {
    if !s.options.Auth.Enabled {
        return false
    }
    if strings.HasPrefix(path, "/api/v1/") {
        return true
    }
    if s.options.Prometheus.Enabled && s.options.Prometheus.ProtectWithAuth && path == s.options.Prometheus.Path {
        return true
    }
    return false
}
```

### 3.2 速率限制

基于 IP 的速率限制，每个访问者独立限流：

```go
// internal/server/middleware.go
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
    rl := newRateLimiter(10, 20) // 10 请求/秒，突发容量 20
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

每个 IP 维护独立的令牌桶限流器：

```go
// internal/server/middleware.go
type rateLimiter struct {
    visitors map[string]*rate.Limiter
    mu       sync.Mutex
    rate     rate.Limit
    burst    int
}
```

### 3.3 输入验证

**请求体大小限制**：exec 和 task 端点限制请求体为 1MB：

```go
// internal/server/handlers.go
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
    // ...
    r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
    // ...
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
    // ...
    r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB
    // ...
}
```

**HTTP 方法限制**：所有端点严格限制 HTTP 方法：

```go
// internal/server/handlers.go
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Success: false, Error: "method not allowed"})
        return
    }
    // ...
}
```

**超时上限**：任务超时被限制在 300 秒以内：

```go
// internal/server/handlers.go
const maxTimeoutSeconds = 300

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
    // ...
    timeoutSeconds := 15
    if timeoutVal, ok := req.Payload["timeout_seconds"]; ok {
        if seconds, ok := parseTimeoutSeconds(timeoutVal); ok && seconds > 0 {
            timeoutSeconds = min(seconds, maxTimeoutSeconds)
        }
    }
    // ...
}
```

### 3.4 输出安全

**错误信息净化**：API 响应不泄露内部实现细节：

```go
// internal/server/handlers.go
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
    // ...
    res, err := s.executor.Execute(r.Context(), req)
    if err != nil {
        s.logger.Error().Err(err).Msg("exec request failed")
        writeJSON(w, http.StatusBadRequest, apiResponse{Success: false, Error: "command execution failed"})
        return  // 不返回 err.Error()，避免泄露内部细节
    }
    // ...
}
```

**Prometheus 标签值转义**：防止标签注入：

```go
// internal/collector/outputs/prometheus/prometheus.go
func escapeLabelValue(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `"`, `\"`)
    s = strings.ReplaceAll(s, "\n", `\n`)
    return s
}
```

### 3.5 HTTP 安全头

所有响应自动附加安全头：

```go
// internal/server/middleware.go
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("Cache-Control", "no-store")
        next.ServeHTTP(w, r)
    })
}
```

中间件链顺序（从外到内）：

```
recover -> securityHeaders -> rateLimit -> logging -> auth -> handler
```

---

## 4. 通信安全

### 4.1 gRPC

gRPC 客户端强制 TLS 1.2+，拒绝不安全连接：

```go
// internal/grpcclient/client.go
func (c *Client) buildTLSCredentials() (credentials.TransportCredentials, error) {
    if c.cfg.CertPath == "" && c.cfg.KeyPath == "" && c.cfg.CAPath == "" {
        return nil, fmt.Errorf("no TLS certificates configured; refusing insecure connection (set grpc.mtls.cert_file, key_file, and ca_file)")
    }

    tlsCfg := &tls.Config{
        MinVersion: tls.VersionTLS12,
        ServerName: extractServerName(c.cfg.ServerAddr),
    }
    // ...
}
```

关键安全特性：
- **TLS 1.2 最低版本**：`MinVersion: tls.VersionTLS12`
- **ServerName 验证**：自动从服务端地址提取主机名用于 TLS 验证
- **拒绝不安全连接**：未配置任何证书时返回错误，不允许降级到明文

### 4.2 mTLS（可选）

支持双向 TLS 认证，需配置以下三个字段：

```yaml
grpc:
  mtls:
    cert_file: "/path/to/client.crt"   # 客户端证书
    key_file: "/path/to/client.key"    # 客户端私钥
    ca_file: "/path/to/ca.crt"         # CA 证书
```

当仅配置 `ca_file` 而未配置客户端证书时，使用系统 CA 进行服务端验证。

---

## 5. 插件安全

### 5.1 二进制路径穿越防护

插件 manifest 中的 `binary_path` 经过路径穿越检查，确保解析后的路径不逃逸出插件目录：

```go
// internal/pluginruntime/manifest.go
func LoadManifest(path string) (*PluginManifest, error) {
    // ...
    cleanBin := filepath.Clean(m.BinaryPath)
    cleanDir := filepath.Clean(m.resolvedDir)
    if !strings.HasPrefix(cleanBin, cleanDir+string(filepath.Separator)) {
        return nil, fmt.Errorf("binary_path %q escapes plugin directory %q", m.BinaryPath, m.resolvedDir)
    }
    // ...
}
```

### 5.2 插件名称净化

插件名称仅允许字母、数字、连字符和下划线，且不得以连字符开头：

```go
// internal/pluginruntime/gateway.go
var validPluginName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func sanitizePluginName(name string) (string, error) {
    if !validPluginName.MatchString(name) {
        return "", fmt.Errorf("invalid plugin name %q: must be alphanumeric with hyphens/underscores", name)
    }
    return name, nil
}
```

### 5.3 Socket 路径安全

插件 socket 使用专用目录，权限为 0700，并防御符号链接攻击：

```go
// internal/pluginruntime/gateway.go
socketDir := filepath.Join(os.TempDir(), "opsagent-plugins")
os.MkdirAll(socketDir, 0o700)

socketPath := filepath.Join(socketDir, safeName+".sock")

// 防御符号链接/TOCTOU 攻击
if fi, err := os.Lstat(socketPath); err == nil {
    if fi.Mode()&os.ModeSymlink != 0 {
        return fmt.Errorf("socket path %q is a symlink, refusing to remove", socketPath)
    }
    _ = os.Remove(socketPath)
}
```

### 5.4 FsScan 路径白名单

文件系统扫描插件限制扫描路径在安全目录内：

```rust
// rust-runtime/src/handlers/fs_scan.rs
const ALLOWED_ROOTS: &[&str] = &["/var/log", "/opt", "/srv", "/tmp"];

// 验证路径
let canonical = std::fs::canonicalize(root_path)?;
let is_allowed = ALLOWED_ROOTS.iter().any(|allowed| canonical.starts_with(allowed));
if !is_allowed {
    return Err(PluginError::Config(format!(
        "root_path {} is not under allowed roots: {:?}", root_path, ALLOWED_ROOTS
    )));
}
```

---

## 6. 数据安全

### 6.1 配置差异中的密钥脱敏

配置热重载时，差异报告中的敏感字段自动脱敏：

```go
// internal/config/diff.go
func maskSecret(s string) string {
    if len(s) <= 4 {
        return "***"
    }
    return s[:2] + "***" + s[len(s)-2:]
}

// 使用示例：gRPC 注册令牌
changes = append(changes, NonReloadableChange{
    Field:  "grpc.enroll_token",
    OldVal: maskSecret(old.GRPC.EnrollToken),
    NewVal: maskSecret(new.GRPC.EnrollToken),
})
```

### 6.2 文件权限

所有敏感文件使用严格权限：

| 文件类型 | 权限 | 代码位置 |
|----------|------|----------|
| 配置文件 | 0600 | 打包脚本 `scripts/ci-package.sh` |
| 审计日志 | 0600 | `internal/sandbox/audit.go`、`internal/app/audit.go` |
| 临时脚本文件 | 0600 | `internal/sandbox/nsjail.go` |
| 临时配置文件 | 0600 | `internal/sandbox/nsjail.go` |
| 缓存持久化文件 | 0600 | `internal/grpcclient/client.go` |

### 6.3 不可预测临时路径

脚本和配置临时文件使用随机名称，防止攻击者预测路径：

```go
// internal/sandbox/nsjail.go
func (c *NsjailConfig) WriteScriptFile(taskID, scriptContent string) (string, error) {
    scriptDir := filepath.Join(os.TempDir(), "nsjail-scripts")
    os.MkdirAll(scriptDir, 0o700)
    f, err := os.CreateTemp(scriptDir, "task-*.sh")  // 随机后缀
    // ...
    os.Chmod(f.Name(), 0o600)
    // ...
}
```

测试验证路径不可预测性：

```go
// internal/sandbox/sanitize_test.go
func TestWriteScriptFileUnpredictablePath(t *testing.T) {
    path1, _ := cfg.WriteScriptFile("task-1", "echo hello")
    path2, _ := cfg.WriteScriptFile("task-1", "echo hello")
    if path1 == path2 {
        t.Errorf("expected unpredictable paths, got same path twice")
    }
}
```

### 6.4 dmesg 输出限制

本地探测插件读取 dmesg 时限制输出大小为 64KB，防止内存耗尽：

```rust
// rust-runtime/src/handlers/local_probe.rs
let truncated = if stdout.len() > 65536 {
    &stdout[stdout.len() - 65536..]  // 仅检查最后 64KB
} else {
    &stdout
};
```

---

## 7. 系统安全

### 7.1 systemd 服务加固

systemd 服务单元实施以下安全限制：

```ini
# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/log/opsagent /tmp/opsagent
ProtectHome=true
PrivateTmp=true
```

| 指令 | 作用 |
|------|------|
| `NoNewPrivileges=true` | 禁止进程提升权限（如 setuid、setgid） |
| `ProtectSystem=strict` | 文件系统只读，仅 `ReadWritePaths` 可写 |
| `ProtectHome=true` | 隐藏 `/home`、`/root`、`/run/user` |
| `PrivateTmp=true` | 使用私有 `/tmp`，隔离其他进程的临时文件 |

### 7.2 本地回环绑定

HTTP 服务器和 Prometheus 输出默认绑定到 `127.0.0.1`，仅接受本地连接：

```go
// internal/config/config.go
v.SetDefault("server.listen_addr", "127.0.0.1:18080")
```

```go
// internal/collector/outputs/prometheus/prometheus.go
const defaultAddr = "127.0.0.1:9100"
```

### 7.3 HTTP ReadHeaderTimeout

HTTP 服务器设置 `ReadHeaderTimeout` 防止 Slowloris 攻击：

```go
// internal/server/server.go
s.httpServer = &http.Server{
    Addr:              listenAddr,
    Handler:           s.withMiddleware(mux),
    ReadHeaderTimeout: 5 * time.Second,
}
```

---

## 8. 审计日志

### 8.1 应用级审计日志

应用级审计日志使用 JSON-lines 格式，支持日志轮转：

```go
// internal/app/audit.go
type AuditEvent struct {
    Timestamp time.Time              `json:"timestamp"`
    EventType string                 `json:"event_type"`
    Component string                 `json:"component"`
    Action    string                 `json:"action"`
    Status    string                 `json:"status"`
    Details   map[string]interface{} `json:"details,omitempty"`
    Error     string                 `json:"error,omitempty"`
}
```

日志轮转使用 lumberjack：

```go
// internal/app/audit.go
func NewAuditLogger(path string, maxSizeMB, maxBackups int) (*AuditLogger, error) {
    lj := &lumberjack.Logger{
        Filename:   path,
        MaxSize:    maxSizeMB,
        MaxBackups: maxBackups,
        Compress:   true,
    }
    return &AuditLogger{logger: lj}, nil
}
```

**审计事件类型**：

| 事件类型 | 组件 | 说明 |
|----------|------|------|
| `config.loaded` | agent | 配置加载 |
| `config.reloaded` | agent | 配置热重载成功 |
| `config.rejected` | agent | 配置热重载失败 |
| `agent.started` | agent | 代理启动 |
| `agent.shutting_down` | agent | 代理开始关闭 |
| `agent.stopped` | agent | 代理停止 |
| `plugin.started` | pluginruntime | 插件运行时启动 |
| `plugin.stopped` | pluginruntime | 插件运行时停止 |
| `grpc.connected` | grpcclient | gRPC 连接建立 |
| `grpc.disconnected` | grpcclient | gRPC 连接断开 |
| `task.started` | dispatcher | 任务开始执行 |
| `task.completed` | dispatcher | 任务执行完成 |
| `task.failed` | dispatcher | 任务执行失败 |
| `task.cancelled` | dispatcher | 任务被取消 |
| `sandbox.executed` | sandbox | 沙箱执行完成 |

### 8.2 沙箱审计日志

沙箱子系统维护独立的审计日志，记录每次执行的详细信息：

```go
// internal/sandbox/audit.go
type AuditEvent struct {
    TaskID      string        `json:"task_id"`
    Timestamp   time.Time     `json:"timestamp"`
    TriggeredBy string        `json:"triggered_by"`
    Type        string        `json:"type"`
    Command     string        `json:"command"`
    Interpreter string        `json:"interpreter,omitempty"`
    ExitCode    int           `json:"exit_code"`
    Duration    time.Duration `json:"duration"`
    TimedOut    bool          `json:"timed_out"`
    Truncated   bool          `json:"truncated"`
    Killed      bool          `json:"killed"`
    Stats       *Stats        `json:"stats,omitempty"`
    Error       string        `json:"error,omitempty"`
}
```

沙箱审计日志文件权限为 0600：

```go
// internal/sandbox/audit.go
f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
```

---

## 9. 安全配置参考

以下是 `config.yaml` 中与安全相关的关键字段：

```yaml
agent:
  id: "agent-local-001"
  name: "local-dev-agent"
  interval_seconds: 10
  shutdown_timeout_seconds: 30
  audit_log:
    enabled: false                    # 生产环境建议开启
    path: "/var/log/opsagent/audit.jsonl"
    max_size_mb: 100
    max_backups: 5

server:
  listen_addr: "127.0.0.1:18080"     # 默认仅监听本地回环

auth:
  enabled: true                       # 默认启用
  bearer_token: ""                    # 生产环境必须设置 32+ 字符的强 Token

prometheus:
  enabled: true
  path: "/metrics"
  protect_with_auth: false            # 如需保护指标端点，设为 true

executor:
  timeout_seconds: 10
  max_output_bytes: 65536
  allowed_commands:
    - uptime
    - df
    - free
    - whoami
    - hostname
    - ip
    - ss

sandbox:
  enabled: false                      # 默认关闭，需显式启用
  nsjail_path: "/usr/bin/nsjail"
  base_workdir: "/tmp/opsagent/sandbox"
  default_timeout_seconds: 30
  max_concurrent_tasks: 4
  cgroup_base_path: "/sys/fs/cgroup/opsagent"
  audit_log_path: "/var/log/opsagent/audit.log"
  policy:
    allowed_commands:
      - echo
      - ls
      - cat
      - grep
      - wc
    blocked_commands:
      - rm
      - mkfs
      - dd
    blocked_keywords:
      - "rm -rf /"
    allowed_interpreters:
      - bash
      - python3
    script_max_bytes: 65536
    shell_injection_check: true        # 默认启用

grpc:
  server_addr: "platform.example.com:443"
  enroll_token: ""
  mtls:
    cert_file: ""                     # 客户端证书路径
    key_file: ""                      # 客户端私钥路径
    ca_file: ""                       # CA 证书路径
  heartbeat_interval_seconds: 15
  reconnect_initial_backoff_ms: 1000
  reconnect_max_backoff_ms: 30000

plugin:
  enabled: false
  runtime_path: "./rust-runtime/target/release/opsagent-rust-runtime"
  socket_path: "/tmp/opsagent/plugin.sock"
  auto_start: true
  startup_timeout_seconds: 5
  request_timeout_seconds: 30
  max_concurrent_tasks: 4
  max_result_bytes: 8388608           # 8MB
  chunk_size_bytes: 262144            # 256KB
  sandbox_profile: "strict"

plugin_gateway:
  enabled: false
  plugins_dir: "/etc/opsagent/plugins"
  startup_timeout_seconds: 10
  health_check_interval_seconds: 30
  max_restarts: 3
  restart_backoff_seconds: 5
  file_watch_debounce_seconds: 2
```

---

## 10. 安全检查清单

部署前请逐项确认以下安全配置：

- [ ] `auth.enabled` 设为 `true`
- [ ] `bearer_token` 设置为 32 字符以上的强随机值
- [ ] `server.listen_addr` 绑定到 `127.0.0.1`（或特定网卡地址）
- [ ] 生产环境配置 gRPC mTLS（`grpc.mtls.cert_file`、`key_file`、`ca_file`）
- [ ] `sandbox.policy.allowed_commands` 采用最小权限原则
- [ ] `sandbox.policy.blocked_commands` 包含 `rm`、`mkfs`、`dd` 等危险命令
- [ ] `agent.audit_log.enabled` 设为 `true`
- [ ] 配置文件权限为 `0600`
- [ ] systemd 服务使用 `NoNewPrivileges=true`
- [ ] systemd 服务使用 `ProtectSystem=strict` 和 `ProtectHome=true`
- [ ] 插件二进制来自可信来源
- [ ] 定期执行 `make security`（gosec 静态安全扫描）
- [ ] 定期执行 `make test-race`（竞态条件检测）
- [ ] `prometheus.protect_with_auth` 在需要时设为 `true`
- [ ] 审计日志路径可写且权限正确
- [ ] 沙箱 cgroup 基础路径存在且可写
