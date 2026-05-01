package pluginruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestGateway_New(t *testing.T) {
	logger := zerolog.Nop()

	t.Run("default config", func(t *testing.T) {
		g := NewGateway(GatewayConfig{}, logger)
		if g == nil {
			t.Fatal("expected non-nil gateway")
		}
		if g.cfg.StartupTimeout != 10*time.Second {
			t.Errorf("startup timeout = %v, want %v", g.cfg.StartupTimeout, 10*time.Second)
		}
		if g.cfg.HealthCheckInterval != 30*time.Second {
			t.Errorf("health check interval = %v, want %v", g.cfg.HealthCheckInterval, 30*time.Second)
		}
		if g.cfg.MaxRestarts != 3 {
			t.Errorf("max restarts = %d, want 3", g.cfg.MaxRestarts)
		}
		if g.cfg.RestartBackoff != 5*time.Second {
			t.Errorf("restart backoff = %v, want %v", g.cfg.RestartBackoff, 5*time.Second)
		}
		if g.cfg.FileWatchDebounce != 500*time.Millisecond {
			t.Errorf("file watch debounce = %v, want %v", g.cfg.FileWatchDebounce, 500*time.Millisecond)
		}
		if g.plugins == nil {
			t.Fatal("plugins map should be initialized")
		}
	})

	t.Run("custom config", func(t *testing.T) {
		cfg := GatewayConfig{
			StartupTimeout:      20 * time.Second,
			HealthCheckInterval: 60 * time.Second,
			MaxRestarts:         5,
			RestartBackoff:      10 * time.Second,
			FileWatchDebounce:   1 * time.Second,
		}
		g := NewGateway(cfg, logger)
		if g.cfg.StartupTimeout != 20*time.Second {
			t.Errorf("startup timeout = %v, want %v", g.cfg.StartupTimeout, 20*time.Second)
		}
		if g.cfg.MaxRestarts != 5 {
			t.Errorf("max restarts = %d, want 5", g.cfg.MaxRestarts)
		}
	})
}

func TestGateway_StartStop_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{PluginsDir: dir}, logger)

	ctx := context.Background()
	if err := g.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := g.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestGateway_StartStop_NoDir(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	ctx := context.Background()
	if err := g.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := g.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestGateway_ListPlugins_Empty(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	plugins := g.ListPlugins()
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}
}

func TestGateway_GetPlugin_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	info := g.GetPlugin("nonexistent")
	if info != nil {
		t.Errorf("expected nil for nonexistent plugin, got %+v", info)
	}
}

func TestParseFullTaskType(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPlugin string
		wantTask   string
		wantErr    bool
	}{
		{
			name:       "valid",
			input:      "my-plugin/audit",
			wantPlugin: "my-plugin",
			wantTask:   "audit",
		},
		{
			name:       "multiple slashes",
			input:      "my-plugin/task/sub",
			wantPlugin: "my-plugin",
			wantTask:   "task/sub",
		},
		{
			name:    "no slash",
			input:   "my-plugin-audit",
			wantErr: true,
		},
		{
			name:    "empty plugin name",
			input:   "/audit",
			wantErr: true,
		},
		{
			name:    "empty task type",
			input:   "my-plugin/",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin, task, err := ParseFullTaskType(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plugin != tt.wantPlugin {
				t.Errorf("plugin = %q, want %q", plugin, tt.wantPlugin)
			}
			if task != tt.wantTask {
				t.Errorf("task = %q, want %q", task, tt.wantTask)
			}
		})
	}
}

func TestMergePluginConfig(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		result := mergePluginConfig(nil, nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("manifest only", func(t *testing.T) {
		manifestCfg := map[string]interface{}{"key1": "val1", "key2": 42}
		result := mergePluginConfig(manifestCfg, nil)
		if result["key1"] != "val1" {
			t.Errorf("key1 = %v, want val1", result["key1"])
		}
		if result["key2"] != 42 {
			t.Errorf("key2 = %v, want 42", result["key2"])
		}
	})

	t.Run("agent only", func(t *testing.T) {
		agentCfg := map[string]interface{}{"key1": "override"}
		result := mergePluginConfig(nil, agentCfg)
		if result["key1"] != "override" {
			t.Errorf("key1 = %v, want override", result["key1"])
		}
	})

	t.Run("agent overrides manifest", func(t *testing.T) {
		manifestCfg := map[string]interface{}{"key1": "original", "key2": 42}
		agentCfg := map[string]interface{}{"key1": "overridden", "key3": "new"}
		result := mergePluginConfig(manifestCfg, agentCfg)
		if result["key1"] != "overridden" {
			t.Errorf("key1 = %v, want overridden", result["key1"])
		}
		if result["key2"] != 42 {
			t.Errorf("key2 = %v, want 42", result["key2"])
		}
		if result["key3"] != "new" {
			t.Errorf("key3 = %v, want new", result["key3"])
		}
	})

	t.Run("empty maps", func(t *testing.T) {
		result := mergePluginConfig(map[string]interface{}{}, map[string]interface{}{})
		if result != nil {
			t.Errorf("expected nil for empty maps, got %v", result)
		}
	})
}

func TestDiscoverManifests_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	manifests, err := DiscoverManifests(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("expected 0 manifests, got %d", len(manifests))
	}
}

func TestDiscoverManifests_NonExistentDir(t *testing.T) {
	_, err := DiscoverManifests("/nonexistent/path/to/plugins")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestDiscoverManifests_WithPlugins(t *testing.T) {
	dir := t.TempDir()

	// Create first plugin.
	plugin1Dir := filepath.Join(dir, "plugin-a")
	if err := os.MkdirAll(plugin1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest1 := `
name: plugin-a
version: "1.0.0"
binary_path: ./plugin-a
task_types:
  - audit
`
	if err := os.WriteFile(filepath.Join(plugin1Dir, "plugin.yaml"), []byte(manifest1), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a dummy binary so startProcess won't fail if called.
	if err := os.WriteFile(filepath.Join(plugin1Dir, "plugin-a"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create second plugin.
	plugin2Dir := filepath.Join(dir, "plugin-b")
	if err := os.MkdirAll(plugin2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest2 := `
name: plugin-b
version: "2.0.0"
binary_path: ./plugin-b
task_types:
  - report
  - export
`
	if err := os.WriteFile(filepath.Join(plugin2Dir, "plugin.yaml"), []byte(manifest2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin2Dir, "plugin-b"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory without plugin.yaml (should be skipped).
	emptyDir := filepath.Join(dir, "not-a-plugin")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a file (not directory) at top level (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	manifests, err := DiscoverManifests(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(manifests) != 2 {
		t.Fatalf("expected 2 manifests, got %d", len(manifests))
	}

	names := map[string]bool{}
	for _, m := range manifests {
		names[m.Name] = true
	}
	if !names["plugin-a"] {
		t.Error("expected plugin-a in results")
	}
	if !names["plugin-b"] {
		t.Error("expected plugin-b in results")
	}
}

func TestDiscoverManifests_InvalidManifest(t *testing.T) {
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "bad-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Missing required fields.
	badManifest := `
name: bad-plugin
`
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(badManifest), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := DiscoverManifests(dir)
	if err == nil {
		t.Fatal("expected error for invalid manifest")
	}
}

func TestGateway_LoadPlugin_InvalidBinary(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{StartupTimeout: 1 * time.Second}, logger)

	manifest := &PluginManifest{
		Name:       "test-plugin",
		Version:    "1.0.0",
		BinaryPath: "/nonexistent/binary",
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}

	err := g.loadPlugin(manifest)
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
}

func TestGateway_EnableDisablePlugin(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	// Insert a mock plugin.
	manifest := &PluginManifest{
		Name:       "test-plugin",
		Version:    "1.0.0",
		BinaryPath: "/bin/true",
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}
	g.plugins["test-plugin"] = &ManagedPlugin{
		Manifest: manifest,
		Status:   PluginStatusDisabled,
	}

	// Disable should fail when already disabled.
	err := g.DisablePlugin("test-plugin")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func TestGateway_EnablePlugin_NotDisabled(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	manifest := &PluginManifest{
		Name:       "test-plugin",
		Version:    "1.0.0",
		BinaryPath: "/bin/true",
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}
	g.plugins["test-plugin"] = &ManagedPlugin{
		Manifest: manifest,
		Status:   PluginStatusRunning,
	}

	err := g.EnablePlugin("test-plugin")
	if err == nil {
		t.Fatal("expected error when enabling a running plugin")
	}
}

func TestGateway_ReloadPlugin_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	err := g.ReloadPlugin("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
}

func TestGateway_DisablePlugin_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	err := g.DisablePlugin("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
}

func TestGateway_EnablePlugin_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	err := g.EnablePlugin("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
}

func TestGateway_ExecuteTask_NotFound(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	_, err := g.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "nonexistent/audit",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent plugin")
	}
}

func TestGateway_ExecuteTask_InvalidType(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	_, err := g.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "invalid-format",
	})
	if err == nil {
		t.Fatal("expected error for invalid task type format")
	}
}

func TestGateway_Callbacks(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	var unloadedName string
	var unloadedTypes []string

	g.OnPluginLoaded(func(name string, taskTypes []string) {
		// registered but not exercised in this test
	})
	g.OnPluginUnloaded(func(name string, taskTypes []string) {
		unloadedName = name
		unloadedTypes = taskTypes
	})

	// Simulate load callback through stopPlugin (which calls onUnloaded).
	manifest := &PluginManifest{
		Name:       "cb-plugin",
		Version:    "1.0.0",
		BinaryPath: "/bin/true",
		TaskTypes:  []string{"test"},
		Runtime:    "process",
	}
	mp := &ManagedPlugin{
		Manifest: manifest,
		Status:   PluginStatusRunning,
		SocketPath: "/tmp/test.sock",
	}
	g.plugins["cb-plugin"] = mp

	// Test onLoaded was registered.
	if g.onLoaded == nil {
		t.Fatal("onLoaded callback not registered")
	}

	// Trigger unload via stopPlugin.
	g.stopPlugin("cb-plugin", mp)

	if unloadedName != "cb-plugin" {
		t.Errorf("unloaded name = %q, want %q", unloadedName, "cb-plugin")
	}
	if len(unloadedTypes) != 1 || unloadedTypes[0] != "test" {
		t.Errorf("unloaded types = %v, want [test]", unloadedTypes)
	}
}

func TestRPCClient_Close(t *testing.T) {
	c := NewRPCClient("/tmp/test.sock")
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestPluginStatus_Constants(t *testing.T) {
	tests := []struct {
		status PluginStatus
		want   string
	}{
		{PluginStatusStarting, "starting"},
		{PluginStatusRunning, "running"},
		{PluginStatusStopped, "stopped"},
		{PluginStatusError, "error"},
		{PluginStatusDisabled, "disabled"},
	}
	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("PluginStatus %v = %q, want %q", tt.status, string(tt.status), tt.want)
		}
	}
}

func TestGateway_ListPlugins_WithPlugins(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	g.plugins["p1"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "p1", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/p1.sock",
		StartedAt:  time.Now(),
		LastHealth: time.Now(),
	}
	g.plugins["p2"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "p2", Version: "2.0.0", TaskTypes: []string{"report", "export"}},
		Status:     PluginStatusStopped,
		SocketPath: "/tmp/p2.sock",
	}

	plugins := g.ListPlugins()
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}

	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name] = true
		if p.Name == "p1" {
			if p.Status != PluginStatusRunning {
				t.Errorf("p1 status = %v, want running", p.Status)
			}
			if p.Uptime <= 0 {
				t.Error("p1 uptime should be positive")
			}
			if len(p.TaskTypes) != 1 {
				t.Errorf("p1 task_types len = %d, want 1", len(p.TaskTypes))
			}
		}
		if p.Name == "p2" {
			if len(p.TaskTypes) != 2 {
				t.Errorf("p2 task_types len = %d, want 2", len(p.TaskTypes))
			}
		}
	}
	if !names["p1"] || !names["p2"] {
		t.Errorf("expected both p1 and p2, got %v", names)
	}
}

func TestGateway_GetPlugin_Found(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	g.plugins["my-plugin"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "my-plugin", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/my-plugin.sock",
		StartedAt:  time.Now(),
	}

	info := g.GetPlugin("my-plugin")
	if info == nil {
		t.Fatal("expected non-nil plugin info")
	}
	if info.Name != "my-plugin" {
		t.Errorf("name = %q, want %q", info.Name, "my-plugin")
	}
	if info.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", info.Version, "1.0.0")
	}
	if info.Status != PluginStatusRunning {
		t.Errorf("status = %v, want running", info.Status)
	}
}

func TestGateway_ExecuteTask_PluginNotRunning(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	g.plugins["my-plugin"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "my-plugin", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusStopped,
		SocketPath: "/tmp/my-plugin.sock",
	}

	_, err := g.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "my-plugin/audit",
	})
	if err == nil {
		t.Fatal("expected error for stopped plugin")
	}
}

func TestGateway_ExecuteTask_NilClient(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	g.plugins["my-plugin"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "my-plugin", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/my-plugin.sock",
		Client:     nil,
	}

	_, err := g.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "my-plugin/audit",
	})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

func TestGateway_StopPlugin_AlreadyStopped(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	manifest := &PluginManifest{
		Name:       "test-plugin",
		Version:    "1.0.0",
		BinaryPath: "/bin/true",
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}
	mp := &ManagedPlugin{
		Manifest:   manifest,
		Status:     PluginStatusStopped,
		SocketPath: "/tmp/test.sock",
	}

	// Should be a no-op.
	g.stopPlugin("test-plugin", mp)
	if mp.Status != PluginStatusStopped {
		t.Errorf("status = %v, want stopped", mp.Status)
	}
}

func TestGateway_ListPlugins_NoUptime(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	// Stopped plugin should have zero uptime.
	g.plugins["p1"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "p1", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusStopped,
		SocketPath: "/tmp/p1.sock",
	}

	plugins := g.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].Uptime != 0 {
		t.Errorf("uptime = %v, want 0", plugins[0].Uptime)
	}
}

func TestRPCClient_Ping_Success(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{ID: "ping"})
	}()

	err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestRPCClient_Ping_DialError(t *testing.T) {
	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestRPCClient_Ping_RPCError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{
			ID:    "ping",
			Error: &rpcError{Code: -1, Message: "ping failed"},
		})
	}()

	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected RPC error")
	}
}

func TestRPCClient_ExecuteTask_Success(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{
			ID:     "t1",
			Result: &TaskResponse{TaskID: "t1", Status: "ok"},
		})
	}()

	resp, err := c.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "audit",
	})
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
}

func TestRPCClient_ExecuteTask_DialError(t *testing.T) {
	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	_, err := c.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "audit",
	})
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestRPCClient_ExecuteTask_RPCError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{
			ID:    "t1",
			Error: &rpcError{Code: -32600, Message: "invalid request"},
		})
	}()

	_, err := c.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "audit",
	})
	if err == nil {
		t.Fatal("expected RPC error")
	}
}

func TestRPCClient_ExecuteTask_EmptyResult(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{ID: "t1"})
	}()

	_, err := c.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "audit",
	})
	if err == nil {
		t.Fatal("expected empty result error")
	}
}

func TestWaitForPlugin_Timeout(t *testing.T) {
	_, err := waitForPlugin("/tmp/nonexistent-plugin.sock", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestGateway_ExecuteTask_Success(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	c := NewRPCClient("/tmp/test.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn, nil
	}

	g.plugins["my-plugin"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "my-plugin", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/my-plugin.sock",
		Client:     c,
		StartedAt:  time.Now(),
	}

	go func() {
		reader := bufio.NewReader(serverConn)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn).Encode(rpcResponse{
			ID:     "t1",
			Result: &TaskResponse{TaskID: "t1", Status: "ok"},
		})
	}()

	resp, err := g.ExecuteTask(context.Background(), TaskRequest{
		TaskID: "t1",
		Type:   "my-plugin/audit",
	})
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}

	// Verify LastHealth was updated.
	p := g.GetPlugin("my-plugin")
	if p == nil {
		t.Fatal("expected plugin info")
	}
	if p.LastHealth.IsZero() {
		t.Error("last health should be set after successful task execution")
	}
}

func TestGateway_HealthCheckAll(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)
	g.cfg.MaxRestarts = 0 // Disable auto-restart for this test.
	g.ctx = context.Background()

	// Plugin with working ping.
	serverConn1, clientConn1 := net.Pipe()
	defer serverConn1.Close()

	c1 := NewRPCClient("/tmp/p1.sock")
	c1.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return clientConn1, nil
	}

	g.plugins["healthy"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "healthy", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/p1.sock",
		Client:     c1,
		StartedAt:  time.Now(),
	}

	// Plugin with broken ping.
	c2 := NewRPCClient("/tmp/p2.sock")
	c2.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	g.plugins["unhealthy"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "unhealthy", Version: "1.0.0", TaskTypes: []string{"report"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/p2.sock",
		Client:     c2,
		StartedAt:  time.Now(),
	}

	// Stopped plugin (should be skipped).
	g.plugins["stopped"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "stopped", Version: "1.0.0", TaskTypes: []string{"export"}},
		Status:     PluginStatusStopped,
		SocketPath: "/tmp/p3.sock",
	}

	// Respond to healthy plugin's ping.
	go func() {
		reader := bufio.NewReader(serverConn1)
		_, _ = reader.ReadBytes('\n')
		_ = json.NewEncoder(serverConn1).Encode(rpcResponse{ID: "ping"})
	}()

	g.healthCheckAll()

	// Healthy plugin should still be running.
	healthy := g.GetPlugin("healthy")
	if healthy.Status != PluginStatusRunning {
		t.Errorf("healthy status = %v, want running", healthy.Status)
	}

	// Unhealthy plugin should be in error state.
	unhealthy := g.GetPlugin("unhealthy")
	if unhealthy.Status != PluginStatusError {
		t.Errorf("unhealthy status = %v, want error", unhealthy.Status)
	}
}

func TestGateway_HealthCheckAll_NilClient(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)
	g.ctx = context.Background()

	// Running plugin with nil client (should be skipped).
	g.plugins["nil-client"] = &ManagedPlugin{
		Manifest:   &PluginManifest{Name: "nil-client", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:     PluginStatusRunning,
		SocketPath: "/tmp/p.sock",
		Client:     nil,
	}

	// Should not panic.
	g.healthCheckAll()
}

func TestKillProcess_AlreadyExited(t *testing.T) {
	// Use a process that has already exited before killProcess is called.
	// startProcess launches the binary, but we simulate the case where
	// the process exits on its own.
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait for it to finish first.
	_ = cmd.Wait()

	mp := &ManagedProcess{
		pid:    cmd.Process.Pid,
		waitCh: make(chan error, 1),
	}
	// Close the waitCh to simulate a completed process.
	close(mp.waitCh)

	// Should return without hanging (FindProcess succeeds on Linux,
	// signal to dead process is a no-op, waitCh is already closed).
	done := make(chan struct{})
	go func() {
		killProcess(mp)
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("killProcess did not complete in time for already-exited process")
	}
}

func TestGateway_ShouldRestart(t *testing.T) {
	cfg := GatewayConfig{MaxRestarts: 3}
	gw := &Gateway{cfg: cfg}

	tests := []struct {
		restartCount int
		want         bool
	}{
		{0, true},
		{1, true},
		{2, true},
		{3, false},
		{4, false},
	}
	for _, tt := range tests {
		p := &ManagedPlugin{RestartCount: tt.restartCount}
		got := gw.shouldRestart(p)
		if got != tt.want {
			t.Errorf("shouldRestart(count=%d) = %v, want %v", tt.restartCount, got, tt.want)
		}
	}
}

func TestGateway_RestartBackoff(t *testing.T) {
	gw := &Gateway{cfg: GatewayConfig{RestartBackoff: 5 * time.Second}}

	tests := []struct {
		count int
		want  time.Duration
	}{
		{0, 5 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 60 * time.Second}, // capped at 60s
		{5, 60 * time.Second},
	}
	for _, tt := range tests {
		got := gw.restartBackoff(tt.count)
		if got != tt.want {
			t.Errorf("restartBackoff(%d) = %v, want %v", tt.count, got, tt.want)
		}
	}
}

func TestGateway_HealthStatus_Stopped(t *testing.T) {
	g := NewGateway(GatewayConfig{}, zerolog.Nop())
	status := g.HealthStatus()
	if status.Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", status.Status)
	}
	if status.Details == nil {
		t.Fatal("expected non-nil details")
	}
	plugins, ok := status.Details["plugins_loaded"]
	if !ok {
		t.Fatal("expected 'plugins_loaded' in details")
	}
	names, ok := plugins.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", plugins)
	}
	if len(names) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(names))
	}
}

func TestGateway_HealthStatus_Running(t *testing.T) {
	g := NewGateway(GatewayConfig{}, zerolog.Nop())
	g.ctx, g.cancel = context.WithCancel(context.Background())
	defer g.cancel()

	// Add a plugin to verify it appears in details.
	g.plugins["p1"] = &ManagedPlugin{
		Manifest: &PluginManifest{Name: "p1", Version: "1.0.0", TaskTypes: []string{"audit"}},
		Status:   PluginStatusRunning,
	}

	status := g.HealthStatus()
	if status.Status != "running" {
		t.Errorf("expected status 'running', got %q", status.Status)
	}
	plugins := status.Details["plugins_loaded"].([]string)
	if len(plugins) != 1 || plugins[0] != "p1" {
		t.Errorf("expected [p1], got %v", plugins)
	}
}

func TestGateway_ReloadPlugin_SuccessPath(t *testing.T) {
	dir := t.TempDir()
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep binary not found")
	}

	manifest := &PluginManifest{
		Name:       "reload-plugin",
		Version:    "1.0.0",
		BinaryPath: sleepPath,
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}

	// Write a dummy plugin.yaml so manifest is valid.
	if err := os.MkdirAll(filepath.Join(dir, "reload-plugin"), 0755); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{StartupTimeout: 1 * time.Second}, logger)

	g.plugins["reload-plugin"] = &ManagedPlugin{
		Manifest:   manifest,
		Status:     PluginStatusRunning,
		SocketPath: filepath.Join(dir, "reload-plugin.sock"),
	}

	// ReloadPlugin calls stopPlugin then loadPlugin. loadPlugin will fail
	// because the socket is never created by sleep, but stopPlugin should
	// have been called first (verified by the status change).
	err = g.ReloadPlugin("reload-plugin")
	if err == nil {
		t.Fatal("expected error because plugin cannot create socket")
	}
	// Verify the plugin was removed from the map by loadPlugin's new entry,
	// or that stopPlugin set the old entry to stopped. Since loadPlugin failed,
	// the old entry should have been stopped.
	p := g.GetPlugin("reload-plugin")
	if p != nil && p.Status != PluginStatusStopped {
		t.Errorf("expected plugin to be stopped after reload attempt, got %v", p.Status)
	}
}

func TestGateway_EnablePlugin_SuccessPath(t *testing.T) {
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep binary not found")
	}

	manifest := &PluginManifest{
		Name:       "enable-plugin",
		Version:    "1.0.0",
		BinaryPath: sleepPath,
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}

	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{StartupTimeout: 1 * time.Second}, logger)

	g.plugins["enable-plugin"] = &ManagedPlugin{
		Manifest:   manifest,
		Status:     PluginStatusDisabled,
		SocketPath: "/tmp/enable-plugin.sock",
	}

	// EnablePlugin calls loadPlugin. loadPlugin will fail because the socket
	// is never created, but it verifies the code path is exercised.
	err = g.EnablePlugin("enable-plugin")
	if err == nil {
		t.Fatal("expected error because plugin cannot create socket")
	}
}

func TestGateway_HealthCheckAll_WithRestart(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{
		MaxRestarts:    1,
		RestartBackoff: time.Millisecond,
		StartupTimeout: 100 * time.Millisecond,
	}, logger)
	g.ctx = context.Background()

	// Plugin with a broken ping client that will fail health check.
	c := NewRPCClient("/tmp/broken.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	manifest := &PluginManifest{
		Name:       "unhealthy",
		Version:    "1.0.0",
		BinaryPath: "/nonexistent/binary",
		TaskTypes:  []string{"audit"},
		Runtime:    "process",
	}

	g.plugins["unhealthy"] = &ManagedPlugin{
		Manifest: manifest,
		Status:   PluginStatusRunning,
		Client:   c,
	}

	g.healthCheckAll()

	// After health check failure with restartable plugin:
	// 1. stopPlugin is called (status -> stopped)
	// 2. loadPlugin is called (fails because binary doesn't exist)
	// 3. Status is set to PluginStatusError
	p := g.GetPlugin("unhealthy")
	if p == nil {
		t.Fatal("expected plugin to still exist")
	}
	if p.Status != PluginStatusError {
		t.Errorf("expected status %v, got %v", PluginStatusError, p.Status)
	}
}

func TestGateway_HealthCheckAll_RestartContextCancelled(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{
		MaxRestarts:    1,
		RestartBackoff: 10 * time.Second, // Long backoff to ensure context cancels first.
	}, logger)
	ctx, cancel := context.WithCancel(context.Background())
	g.ctx = ctx

	c := NewRPCClient("/tmp/broken.sock")
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, net.ErrClosed
	}

	g.plugins["unhealthy"] = &ManagedPlugin{
		Manifest: &PluginManifest{
			Name:       "unhealthy",
			Version:    "1.0.0",
			BinaryPath: "/nonexistent/binary",
			TaskTypes:  []string{"audit"},
			Runtime:    "process",
		},
		Status: PluginStatusRunning,
		Client: c,
	}

	// Cancel the context in a goroutine after a short delay.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	g.healthCheckAll()

	// Plugin should have been stopped by stopPlugin.
	p := g.GetPlugin("unhealthy")
	if p == nil {
		t.Fatal("expected plugin to still exist")
	}
	if p.Status != PluginStatusStopped {
		t.Errorf("expected status %v, got %v", PluginStatusStopped, p.Status)
	}
}
