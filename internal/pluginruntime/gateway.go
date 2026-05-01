package pluginruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cy77cc/opsagent/internal/health"
	"github.com/rs/zerolog"
)

// validPluginName matches plugin names that are safe for use in file paths.
var validPluginName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// sanitizePluginName validates that a plugin name contains only safe characters
// (alphanumeric, hyphens, underscores) and does not start with a hyphen.
func sanitizePluginName(name string) (string, error) {
	if !validPluginName.MatchString(name) {
		return "", fmt.Errorf("invalid plugin name %q: must be alphanumeric with hyphens/underscores", name)
	}
	return name, nil
}

// PluginStatus represents the state of a managed plugin.
type PluginStatus string

const (
	PluginStatusStarting PluginStatus = "starting"
	PluginStatusRunning  PluginStatus = "running"
	PluginStatusStopped  PluginStatus = "stopped"
	PluginStatusError    PluginStatus = "error"
	PluginStatusDisabled PluginStatus = "disabled"
)

// PluginInfo is the runtime status of a managed plugin.
type PluginInfo struct {
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Status       PluginStatus  `json:"status"`
	TaskTypes    []string      `json:"task_types"`
	SocketPath   string        `json:"socket_path"`
	RestartCount int           `json:"restart_count"`
	LastHealth   time.Time     `json:"last_health"`
	Uptime       time.Duration `json:"uptime"`
}

// GatewayConfig configures the PluginGateway.
type GatewayConfig struct {
	PluginsDir          string
	StartupTimeout      time.Duration
	HealthCheckInterval time.Duration
	MaxRestarts         int
	RestartBackoff      time.Duration
	FileWatchDebounce   time.Duration
	PluginConfigs       map[string]map[string]interface{}
}

// Gateway manages custom plugin processes.
type Gateway struct {
	cfg     GatewayConfig
	logger  zerolog.Logger
	plugins map[string]*ManagedPlugin
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	onLoaded   func(name string, taskTypes []string)
	onUnloaded func(name string, taskTypes []string)
}

// ManagedPlugin represents a running plugin process.
type ManagedPlugin struct {
	Manifest     *PluginManifest
	Process      *ManagedProcess
	Client       *RPCClient
	SocketPath   string
	Status       PluginStatus
	RestartCount int
	LastHealth   time.Time
	StartedAt    time.Time
	mu           sync.Mutex
}

// ManagedProcess wraps an os.Process with lifecycle management.
type ManagedProcess struct {
	pid    int
	waitCh chan error
}

// RPCClient is a JSON-RPC client over a Unix domain socket.
type RPCClient struct {
	socketPath string
	dial       func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewGateway creates a new PluginGateway with defaults.
func NewGateway(cfg GatewayConfig, logger zerolog.Logger) *Gateway {
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 10 * time.Second
	}
	if cfg.HealthCheckInterval <= 0 {
		cfg.HealthCheckInterval = 30 * time.Second
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 3
	}
	if cfg.RestartBackoff <= 0 {
		cfg.RestartBackoff = 5 * time.Second
	}
	if cfg.FileWatchDebounce <= 0 {
		cfg.FileWatchDebounce = 500 * time.Millisecond
	}
	return &Gateway{
		cfg:     cfg,
		logger:  logger,
		plugins: make(map[string]*ManagedPlugin),
	}
}

// Start discovers plugin manifests and loads all plugins.
func (g *Gateway) Start(ctx context.Context) error {
	g.ctx, g.cancel = context.WithCancel(ctx)

	if g.cfg.PluginsDir == "" {
		g.logger.Warn().Msg("no plugins directory configured, skipping plugin discovery")
		return nil
	}

	manifests, err := DiscoverManifests(g.cfg.PluginsDir)
	if err != nil {
		return fmt.Errorf("discover plugins: %w", err)
	}

	for _, manifest := range manifests {
		if err := g.loadPlugin(manifest); err != nil {
			g.logger.Error().Err(err).Str("plugin", manifest.Name).Msg("failed to load plugin")
		}
	}

	g.startHealthCheckLoop()

	if err := g.startWatcher(); err != nil {
		g.logger.Error().Err(err).Msg("failed to start file watcher")
	}

	return nil
}

// Stop stops all managed plugins.
func (g *Gateway) Stop(ctx context.Context) error {
	if g.cancel != nil {
		g.cancel()
	}

	g.mu.RLock()
	plugins := make(map[string]*ManagedPlugin, len(g.plugins))
	for k, v := range g.plugins {
		plugins[k] = v
	}
	g.mu.RUnlock()

	for name, p := range plugins {
		g.stopPlugin(name, p)
	}

	g.wg.Wait()

	g.mu.Lock()
	g.plugins = make(map[string]*ManagedPlugin)
	g.mu.Unlock()

	return nil
}

// HealthStatus reports the gateway's running state and loaded plugins.
func (g *Gateway) HealthStatus() health.Status {
	g.mu.RLock()
	started := g.cancel != nil
	names := make([]string, 0, len(g.plugins))
	for name := range g.plugins {
		names = append(names, name)
	}
	g.mu.RUnlock()
	status := "stopped"
	if started {
		status = "running"
	}
	return health.Status{
		Status:  status,
		Details: map[string]any{"plugins_loaded": names},
	}
}

// ExecuteTask routes a task to the appropriate plugin.
func (g *Gateway) ExecuteTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	pluginName, taskType, err := ParseFullTaskType(req.Type)
	if err != nil {
		return nil, err
	}

	g.mu.RLock()
	p, ok := g.plugins[pluginName]
	g.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", pluginName)
	}

	p.mu.Lock()
	status := p.Status
	client := p.Client
	p.mu.Unlock()

	if status != PluginStatusRunning {
		return nil, fmt.Errorf("plugin %s is not running (status: %s)", pluginName, status)
	}
	if client == nil {
		return nil, fmt.Errorf("plugin %s has no RPC client", pluginName)
	}

	// Rewrite the task type to the unqualified form for the plugin.
	pluginReq := req
	pluginReq.Type = taskType

	resp, err := client.ExecuteTask(ctx, pluginReq)
	if err != nil {
		return nil, fmt.Errorf("plugin %s execute task: %w", pluginName, err)
	}

	p.mu.Lock()
	p.LastHealth = time.Now()
	p.mu.Unlock()

	return resp, nil
}

// ListPlugins returns info for all managed plugins.
func (g *Gateway) ListPlugins() []PluginInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	infos := make([]PluginInfo, 0, len(g.plugins))
	for _, p := range g.plugins {
		infos = append(infos, g.pluginInfo(p))
	}
	return infos
}

// GetPlugin returns info for a single plugin, or nil if not found.
func (g *Gateway) GetPlugin(name string) *PluginInfo {
	g.mu.RLock()
	defer g.mu.RUnlock()

	p, ok := g.plugins[name]
	if !ok {
		return nil
	}
	info := g.pluginInfo(p)
	return &info
}

// ReloadPlugin stops and restarts a plugin.
func (g *Gateway) ReloadPlugin(name string) error {
	g.mu.RLock()
	p, ok := g.plugins[name]
	g.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	g.stopPlugin(name, p)

	return g.loadPlugin(p.Manifest)
}

// EnablePlugin enables a disabled plugin.
func (g *Gateway) EnablePlugin(name string) error {
	g.mu.RLock()
	p, ok := g.plugins[name]
	g.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	p.mu.Lock()
	status := p.Status
	p.mu.Unlock()

	if status != PluginStatusDisabled {
		return fmt.Errorf("plugin %s is not disabled (status: %s)", name, status)
	}

	return g.loadPlugin(p.Manifest)
}

// DisablePlugin stops and disables a plugin.
func (g *Gateway) DisablePlugin(name string) error {
	g.mu.RLock()
	p, ok := g.plugins[name]
	g.mu.RUnlock()
	if !ok {
		return fmt.Errorf("plugin not found: %s", name)
	}

	g.stopPlugin(name, p)

	p.mu.Lock()
	p.Status = PluginStatusDisabled
	p.mu.Unlock()

	return nil
}

// OnPluginLoaded registers a callback for when a plugin is loaded.
func (g *Gateway) OnPluginLoaded(fn func(name string, taskTypes []string)) {
	g.onLoaded = fn
}

// OnPluginUnloaded registers a callback for when a plugin is unloaded.
func (g *Gateway) OnPluginUnloaded(fn func(name string, taskTypes []string)) {
	g.onUnloaded = fn
}

// loadPlugin starts a plugin process and establishes RPC connection.
func (g *Gateway) loadPlugin(manifest *PluginManifest) error {
	safeName, err := sanitizePluginName(manifest.Name)
	if err != nil {
		return fmt.Errorf("invalid plugin name: %w", err)
	}

	mergedCfg := mergePluginConfig(manifest.Config, g.cfg.PluginConfigs[manifest.Name])

	// Apply merged config back to manifest for this session.
	manifest.Config = mergedCfg

	// Use a dedicated directory with restricted permissions for plugin sockets.
	socketDir := filepath.Join(os.TempDir(), "opsagent-plugins")
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	socketPath := filepath.Join(socketDir, safeName+".sock")

	// Defend against symlink/TOCTOU attacks: if the socket file exists,
	// verify it is not a symlink before removing.
	if fi, err := os.Lstat(socketPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("socket path %q is a symlink, refusing to remove", socketPath)
		}
		_ = os.Remove(socketPath)
	}

	proc, err := startProcess(manifest, socketPath)
	if err != nil {
		return fmt.Errorf("start plugin process: %w", err)
	}

	client, err := waitForPlugin(socketPath, g.cfg.StartupTimeout)
	if err != nil {
		killProcess(proc)
		return fmt.Errorf("wait for plugin socket: %w", err)
	}

	mp := &ManagedPlugin{
		Manifest:   manifest,
		Process:    proc,
		Client:     client,
		SocketPath: socketPath,
		Status:     PluginStatusRunning,
		LastHealth: time.Now(),
		StartedAt:  time.Now(),
	}

	g.mu.Lock()
	// If there was a previous entry, preserve restart count.
	if existing, ok := g.plugins[manifest.Name]; ok {
		mp.RestartCount = existing.RestartCount
	}
	g.plugins[manifest.Name] = mp
	g.mu.Unlock()

	g.logger.Info().
		Str("plugin", manifest.Name).
		Str("version", manifest.Version).
		Strs("task_types", manifest.TaskTypes).
		Msg("plugin loaded")

	if g.onLoaded != nil {
		g.onLoaded(manifest.Name, manifest.TaskTypes)
	}

	return nil
}

// stopPlugin stops a running plugin process and cleans up resources.
func (g *Gateway) stopPlugin(name string, p *ManagedPlugin) {
	p.mu.Lock()
	if p.Status == PluginStatusStopped || p.Status == PluginStatusDisabled {
		p.mu.Unlock()
		return
	}
	taskTypes := p.Manifest.TaskTypes
	client := p.Client
	proc := p.Process
	p.Status = PluginStatusStopped
	p.Client = nil
	p.Process = nil
	p.mu.Unlock()

	if client != nil {
		_ = client.Close()
	}
	if proc != nil {
		killProcess(proc)
	}

	_ = os.Remove(p.SocketPath)

	g.logger.Info().Str("plugin", name).Msg("plugin stopped")

	if g.onUnloaded != nil {
		g.onUnloaded(name, taskTypes)
	}
}

// ParseFullTaskType splits a "plugin-name/task-type" string.
func ParseFullTaskType(fullType string) (pluginName, taskType string, err error) {
	idx := strings.Index(fullType, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("invalid task type format %q: expected 'plugin-name/task-type'", fullType)
	}
	pluginName = fullType[:idx]
	taskType = fullType[idx+1:]
	if pluginName == "" || taskType == "" {
		return "", "", fmt.Errorf("invalid task type format %q: plugin name and task type must not be empty", fullType)
	}
	return pluginName, taskType, nil
}

// DiscoverManifests scans a directory for plugin.yaml files in subdirectories.
func DiscoverManifests(dir string) ([]*PluginManifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read plugins directory: %w", err)
	}

	var manifests []*PluginManifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, entry.Name(), "plugin.yaml")
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}
		m, err := LoadManifest(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("load manifest %s: %w", manifestPath, err)
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

// mergePluginConfig merges manifest-level and agent-level plugin configs.
// Agent-level config overrides manifest-level config.
func mergePluginConfig(manifestCfg, agentCfg map[string]interface{}) map[string]interface{} {
	if len(manifestCfg) == 0 && len(agentCfg) == 0 {
		return nil
	}
	merged := make(map[string]interface{})
	for k, v := range manifestCfg {
		merged[k] = v
	}
	for k, v := range agentCfg {
		merged[k] = v
	}
	return merged
}

// startProcess launches a plugin binary with the socket path env var.
func startProcess(manifest *PluginManifest, socketPath string) (*ManagedProcess, error) {
	if _, err := os.Stat(manifest.BinaryPath); err != nil {
		return nil, fmt.Errorf("plugin binary not found: %w", err)
	}

	cmd := exec.Command(manifest.BinaryPath)
	cmd.Env = append(os.Environ(), "OPSAGENT_PLUGIN_SOCKET="+socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin binary: %w", err)
	}

	mp := &ManagedProcess{
		pid:    cmd.Process.Pid,
		waitCh: make(chan error, 1),
	}

	go func() {
		mp.waitCh <- cmd.Wait()
	}()

	return mp, nil
}

// waitForPlugin polls for the socket to appear and establishes an RPC client.
func waitForPlugin(socketPath string, timeout time.Duration) (*RPCClient, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for plugin socket %s", socketPath)
		}
		if _, err := os.Stat(socketPath); err == nil {
			// Socket exists, try to connect.
			conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
			if err == nil {
				conn.Close()
				return NewRPCClient(socketPath), nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// killProcess sends SIGTERM, then SIGKILL after a timeout.
func killProcess(proc *ManagedProcess) {
	p, err := os.FindProcess(proc.pid)
	if err != nil {
		return
	}
	_ = p.Signal(os.Interrupt)

	select {
	case <-proc.waitCh:
		return
	case <-time.After(3 * time.Second):
		_ = p.Kill()
		<-proc.waitCh
	}
}

// pluginInfo builds a PluginInfo snapshot from a ManagedPlugin.
func (g *Gateway) pluginInfo(p *ManagedPlugin) PluginInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	var uptime time.Duration
	if p.Status == PluginStatusRunning && !p.StartedAt.IsZero() {
		uptime = time.Since(p.StartedAt)
	}

	return PluginInfo{
		Name:         p.Manifest.Name,
		Version:      p.Manifest.Version,
		Status:       p.Status,
		TaskTypes:    append([]string{}, p.Manifest.TaskTypes...),
		SocketPath:   p.SocketPath,
		RestartCount: p.RestartCount,
		LastHealth:   p.LastHealth,
		Uptime:       uptime,
	}
}

// startHealthCheckLoop periodically pings running plugins.
func (g *Gateway) startHealthCheckLoop() {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(g.cfg.HealthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-g.ctx.Done():
				return
			case <-ticker.C:
				g.healthCheckAll()
			}
		}
	}()
}

// shouldRestart checks if a plugin can be restarted.
func (g *Gateway) shouldRestart(p *ManagedPlugin) bool {
	return p.RestartCount < g.cfg.MaxRestarts
}

// restartBackoff returns the backoff duration for a given restart count.
// The backoff doubles with each attempt and is capped at 60 seconds.
func (g *Gateway) restartBackoff(restartCount int) time.Duration {
	backoff := g.cfg.RestartBackoff
	for i := 0; i < restartCount; i++ {
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
			break
		}
	}
	return backoff
}

// healthCheckAll pings all running plugins and updates their status.
// If a plugin fails the health check and is restartable, it will be
// restarted with exponential backoff. Otherwise its status is set to error.
func (g *Gateway) healthCheckAll() {
	g.mu.RLock()
	plugins := make(map[string]*ManagedPlugin, len(g.plugins))
	for k, v := range g.plugins {
		plugins[k] = v
	}
	g.mu.RUnlock()

	for name, p := range plugins {
		p.mu.Lock()
		if p.Status != PluginStatusRunning || p.Client == nil {
			p.mu.Unlock()
			continue
		}
		client := p.Client
		p.mu.Unlock()

		ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
		err := client.Ping(ctx)
		cancel()

		if err != nil {
			g.logger.Warn().Err(err).Str("plugin", name).Msg("plugin health check failed")

			p.mu.Lock()
			restartable := g.shouldRestart(p)
			manifest := p.Manifest
			restartCount := p.RestartCount
			p.mu.Unlock()

			if restartable {
				backoff := g.restartBackoff(restartCount)
				g.logger.Info().
					Str("plugin", name).
					Int("restart_count", restartCount).
					Dur("backoff", backoff).
					Msg("restarting plugin after health check failure")

				g.stopPlugin(name, p)

				select {
				case <-g.ctx.Done():
					return
				case <-time.After(backoff):
				}

				p.mu.Lock()
				p.RestartCount = restartCount + 1
				p.mu.Unlock()

				if err := g.loadPlugin(manifest); err != nil {
					g.logger.Error().Err(err).Str("plugin", name).Msg("failed to restart plugin")
					p.mu.Lock()
					p.Status = PluginStatusError
					p.mu.Unlock()
				}
			} else {
				g.logger.Error().
					Str("plugin", name).
					Int("restart_count", restartCount).
					Int("max_restarts", g.cfg.MaxRestarts).
					Msg("plugin exceeded max restarts, marking as error")
				p.mu.Lock()
				p.Status = PluginStatusError
				p.mu.Unlock()
			}
		} else {
			p.mu.Lock()
			p.LastHealth = time.Now()
			p.mu.Unlock()
		}
	}
}

// --- RPCClient implementation ---

// NewRPCClient creates a new RPCClient for the given socket path.
func NewRPCClient(socketPath string) *RPCClient {
	return &RPCClient{
		socketPath: socketPath,
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

// Ping sends a ping request to check plugin health.
func (c *RPCClient) Ping(ctx context.Context) error {
	conn, err := c.dial(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial plugin: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req := rpcRequest{
		ID:     "ping",
		Method: "ping",
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("encode ping request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read ping response: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decode ping response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("ping error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	return nil
}

// ExecuteTask sends a task to the plugin via JSON-RPC.
func (c *RPCClient) ExecuteTask(ctx context.Context, req TaskRequest) (*TaskResponse, error) {
	conn, err := c.dial(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial plugin: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	rpcReq := rpcRequest{
		ID:     req.TaskID,
		Method: "execute_task",
		Params: req,
	}
	if err := json.NewEncoder(conn).Encode(rpcReq); err != nil {
		return nil, fmt.Errorf("encode task request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read task response: %w", err)
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode task response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("plugin rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("empty task response")
	}
	return resp.Result, nil
}

// Close is a no-op; connections are per-request.
func (c *RPCClient) Close() error {
	return nil
}
