//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/cy77cc/opsagent/internal/pluginruntime"
)

// TestCustomPluginGateway_EndToEnd builds the go-echo example plugin,
// sets up a temporary plugins directory, starts a PluginGateway, and
// verifies plugin discovery and task execution.
func TestCustomPluginGateway_EndToEnd(t *testing.T) {
	// 1. Build the echo plugin.
	echoSrc := filepath.Join("..", "..", "sdk", "examples", "go-echo")
	binaryPath := filepath.Join(t.TempDir(), "go-echo")

	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = echoSrc
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build echo plugin: %v\n%s", err, buildOut)
	}
	defer os.Remove(binaryPath)

	// 2. Create a temp plugins directory with a go-echo subdirectory.
	pluginsDir := t.TempDir()
	pluginDir := filepath.Join(pluginsDir, "go-echo")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}

	// 3. Copy the manifest into the temp dir, updating binary_path to the absolute path.
	manifestSrc := filepath.Join(echoSrc, "plugin.yaml")
	manifestData, err := os.ReadFile(manifestSrc)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	updatedManifest := replaceBinaryPath(string(manifestData), binaryPath)
	manifestDst := filepath.Join(pluginDir, "plugin.yaml")
	if err := os.WriteFile(manifestDst, []byte(updatedManifest), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// 4. Copy the binary into the temp dir.
	binaryDst := filepath.Join(pluginDir, "go-echo")
	if err := copyFile(binaryPath, binaryDst); err != nil {
		t.Fatalf("copy binary: %v", err)
	}

	// 5. Create a Gateway with the temp plugins dir.
	cfg := pluginruntime.GatewayConfig{
		PluginsDir:          pluginsDir,
		StartupTimeout:      10 * time.Second,
		HealthCheckInterval: 30 * time.Second,
		MaxRestarts:         3,
		RestartBackoff:      5 * time.Second,
		FileWatchDebounce:   500 * time.Millisecond,
	}
	gw := pluginruntime.NewGateway(cfg, zerolog.Nop())

	// 6. Start the gateway.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := gw.Start(ctx); err != nil {
		t.Fatalf("start gateway: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := gw.Stop(stopCtx); err != nil {
			t.Errorf("stop gateway: %v", err)
		}
	}()

	// 7. Verify plugin is loaded.
	plugins := gw.ListPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	p := plugins[0]
	if p.Name != "go-echo" {
		t.Errorf("plugin name = %q, want %q", p.Name, "go-echo")
	}
	if p.Status != pluginruntime.PluginStatusRunning {
		t.Errorf("plugin status = %q, want %q", p.Status, pluginruntime.PluginStatusRunning)
	}

	// 8. Execute a task via the gateway.
	taskCtx, taskCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer taskCancel()

	resp, err := gw.ExecuteTask(taskCtx, pluginruntime.TaskRequest{
		TaskID:     "test-echo-001",
		Type:       "go-echo/echo",
		DeadlineMS: 5000,
		Payload:    map[string]any{"message": "hello"},
		Chunking: pluginruntime.ChunkingConfig{
			Enabled:       false,
			MaxChunkBytes: 0,
			MaxTotalBytes: 0,
		},
	})
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}

	// 9. Verify the response status is "ok".
	if resp.Status != "ok" {
		t.Errorf("response status = %q, want %q", resp.Status, "ok")
	}
	if resp.TaskID != "test-echo-001" {
		t.Errorf("response task_id = %q, want %q", resp.TaskID, "test-echo-001")
	}
}

// replaceBinaryPath replaces the binary_path value in a plugin.yaml string
// with the given absolute path.
func replaceBinaryPath(manifest string, absPath string) string {
	lines := splitLines(manifest)
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "binary_path:") {
			lines[i] = "binary_path: " + absPath
		}
	}
	return joinLines(lines)
}

// splitLines splits a string into lines, preserving empty trailing elements.
func splitLines(s string) []string {
	return strings.Split(s, "\n")
}

// joinLines joins lines back with newline separators.
func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

// copyFile copies a file from src to dst, preserving the executable bit.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, info.Mode()); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}
