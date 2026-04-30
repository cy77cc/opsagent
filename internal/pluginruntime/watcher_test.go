package pluginruntime

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestWatcher_Debounce(t *testing.T) {
	w := &watcherState{
		debounce: 100 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
	}

	var called atomic.Bool
	w.debounceEvent("test-plugin", func() {
		called.Store(true)
	})

	if called.Load() {
		t.Fatal("should not be called before debounce")
	}

	time.Sleep(200 * time.Millisecond)
	if !called.Load() {
		t.Fatal("should be called after debounce")
	}
}

func TestWatcher_Debounce_MultipleEvents(t *testing.T) {
	w := &watcherState{
		debounce: 100 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
	}

	var callCount atomic.Int32
	w.debounceEvent("test-plugin", func() { callCount.Add(1) })
	w.debounceEvent("test-plugin", func() { callCount.Add(1) })
	w.debounceEvent("test-plugin", func() { callCount.Add(1) })

	time.Sleep(200 * time.Millisecond)
	if n := callCount.Load(); n != 1 {
		t.Errorf("callCount = %d, want 1 (debounced)", n)
	}
}

func TestWatcher_Debounce_IndependentKeys(t *testing.T) {
	w := &watcherState{
		debounce: 100 * time.Millisecond,
		timers:   make(map[string]*time.Timer),
	}

	var callsA, callsB atomic.Int32
	w.debounceEvent("plugin-a", func() { callsA.Add(1) })
	w.debounceEvent("plugin-b", func() { callsB.Add(1) })

	time.Sleep(200 * time.Millisecond)
	if n := callsA.Load(); n != 1 {
		t.Errorf("plugin-a calls = %d, want 1", n)
	}
	if n := callsB.Load(); n != 1 {
		t.Errorf("plugin-b calls = %d, want 1", n)
	}
}

func TestIsPluginManifest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/etc/opsagent/plugins/my-plugin/plugin.yaml", true},
		{"/etc/opsagent/plugins/plugin.yaml", true},
		{"/etc/opsagent/plugins/my-plugin/main.go", false},
		{"plugin.yaml", true},
		{"/tmp/some-other.yaml", false},
		{"/plugins/plugin.yml", false},
	}
	for _, tt := range tests {
		got := isPluginManifest(tt.path)
		if got != tt.want {
			t.Errorf("isPluginManifest(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtractPluginName(t *testing.T) {
	tests := []struct {
		name         string
		pluginsDir   string
		manifestPath string
		want         string
	}{
		{
			name:         "plugin in subdirectory",
			pluginsDir:   "/etc/opsagent/plugins",
			manifestPath: "/etc/opsagent/plugins/my-plugin/plugin.yaml",
			want:         "my-plugin",
		},
		{
			name:         "manifest at plugins root",
			pluginsDir:   "/etc/opsagent/plugins",
			manifestPath: "/etc/opsagent/plugins/plugin.yaml",
			want:         "",
		},
		{
			name:         "different plugins dir",
			pluginsDir:   "/tmp/plugins",
			manifestPath: "/tmp/plugins/foo/plugin.yaml",
			want:         "foo",
		},
		{
			name:         "manifest at different root",
			pluginsDir:   "/tmp/plugins",
			manifestPath: "/tmp/plugins/plugin.yaml",
			want:         "",
		},
		{
			name:         "relative path outside plugins dir",
			pluginsDir:   "/etc/opsagent/plugins",
			manifestPath: "/var/other/my-plugin/plugin.yaml",
			want:         "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPluginName(tt.pluginsDir, tt.manifestPath)
			if got != tt.want {
				t.Errorf("extractPluginName(%q, %q) = %q, want %q", tt.pluginsDir, tt.manifestPath, got, tt.want)
			}
		})
	}
}

func TestExtractPluginName_WithTempDir(t *testing.T) {
	dir := t.TempDir()

	// Create a plugin subdirectory.
	pluginDir := filepath.Join(dir, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(pluginDir, "plugin.yaml")

	got := extractPluginName(dir, manifestPath)
	if got != "my-plugin" {
		t.Errorf("extractPluginName = %q, want %q", got, "my-plugin")
	}

	// Root-level manifest should return "".
	rootManifest := filepath.Join(dir, "plugin.yaml")
	got = extractPluginName(dir, rootManifest)
	if got != "" {
		t.Errorf("extractPluginName = %q, want %q", got, "")
	}
}

func TestStartWatcher_NoPluginsDir(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{}, logger)
	g.ctx, g.cancel = context.WithCancel(context.Background())

	// Should return nil without error when PluginsDir is empty.
	if err := g.startWatcher(); err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
}

func TestStartWatcher_WithPluginsDir(t *testing.T) {
	dir := t.TempDir()
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{
		PluginsDir:        dir,
		FileWatchDebounce: 50 * time.Millisecond,
	}, logger)
	g.ctx, g.cancel = context.WithCancel(context.Background())

	if err := g.startWatcher(); err != nil {
		t.Fatalf("startWatcher: %v", err)
	}

	// Cancel and wait for goroutine to exit.
	g.cancel()
	g.wg.Wait()
}

func TestStartWatcher_NonExistentDir(t *testing.T) {
	logger := zerolog.Nop()
	g := NewGateway(GatewayConfig{
		PluginsDir: "/nonexistent/path/to/plugins",
	}, logger)
	g.ctx, g.cancel = context.WithCancel(context.Background())

	// fsnotify.Watcher.Add fails for nonexistent directories.
	err := g.startWatcher()
	if err == nil {
		t.Fatal("expected error for nonexistent plugins directory")
	}
}
