package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// captureStdout runs fn while capturing os.Stdout and returns the output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = origStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

func TestNewRootCommand_Structure(t *testing.T) {
	cmd := NewRootCommand()

	if cmd.Use != "opsagent" {
		t.Errorf("root Use = %q, want opsagent", cmd.Use)
	}

	// Verify expected subcommands exist.
	expected := map[string]bool{
		"version":  false,
		"run":      false,
		"validate": false,
		"plugins":  false,
	}
	for _, sub := range cmd.Commands() {
		name := sub.Name()
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	// Save and restore build vars.
	origVersion := Version
	origCommit := GitCommit
	origBuild := BuildTime
	defer func() {
		Version = origVersion
		GitCommit = origCommit
		BuildTime = origBuild
	}()

	Version = "1.2.3"
	GitCommit = "abc123"
	BuildTime = "2024-01-01"

	root := NewRootCommand()
	root.SetArgs([]string{"version"})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("version command error: %v", err)
		}
	})

	if !strings.Contains(output, "1.2.3") {
		t.Errorf("output missing version: %q", output)
	}
	if !strings.Contains(output, "abc123") {
		t.Errorf("output missing commit: %q", output)
	}
	if !strings.Contains(output, "2024-01-01") {
		t.Errorf("output missing build time: %q", output)
	}
}

func TestRunCommand_Flags(t *testing.T) {
	root := NewRootCommand()
	runCmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatalf("find run command: %v", err)
	}

	// Verify --config flag exists.
	configFlag := runCmd.Flags().Lookup("config")
	if configFlag == nil {
		t.Fatal("expected --config flag on run command")
	}
	if configFlag.DefValue != "./configs/config.yaml" {
		t.Errorf("config default = %q, want ./configs/config.yaml", configFlag.DefValue)
	}

	// Verify --dry-run flag exists.
	dryRunFlag := runCmd.Flags().Lookup("dry-run")
	if dryRunFlag == nil {
		t.Fatal("expected --dry-run flag on run command")
	}
	if dryRunFlag.DefValue != "false" {
		t.Errorf("dry-run default = %q, want false", dryRunFlag.DefValue)
	}
}

func TestValidateCommand_Flags(t *testing.T) {
	root := NewRootCommand()
	validateCmd, _, err := root.Find([]string{"validate"})
	if err != nil {
		t.Fatalf("find validate command: %v", err)
	}

	configFlag := validateCmd.Flags().Lookup("config")
	if configFlag == nil {
		t.Fatal("expected --config flag on validate command")
	}
}

func TestPluginsCommand_NoConfig(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"plugins"})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("plugins command error: %v", err)
		}
	})

	if !strings.Contains(output, "Built-in plugins:") {
		t.Errorf("expected 'Built-in plugins:' in output, got: %q", output)
	}
}

func TestValidateCommand_BadConfigPath(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"validate", "--config", "/nonexistent/path/config.yaml"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent config path")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRootCommand_NoArgs(t *testing.T) {
	// Running root with no args should print help (no error).
	root := NewRootCommand()
	root.SetArgs([]string{})

	err := root.Execute()
	if err != nil {
		t.Fatalf("root command with no args: %v", err)
	}
}

// findSubcommand is a helper to locate a subcommand by name.
func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, sub := range parent.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}

func TestCommandDescriptions(t *testing.T) {
	root := NewRootCommand()
	for _, tc := range []struct {
		name      string
		wantShort string
	}{
		{"run", "Run telemetry exec agent"},
		{"version", "Print version information"},
		{"validate", "Validate configuration file"},
		{"plugins", "List available plugins"},
	} {
		sub := findSubcommand(root, tc.name)
		if sub == nil {
			t.Errorf("subcommand %q not found", tc.name)
			continue
		}
		if sub.Short != tc.wantShort {
			t.Errorf("%s.Short = %q, want %q", tc.name, sub.Short, tc.wantShort)
		}
	}
}

func TestVersionCommand_Override(t *testing.T) {
	origVersion := Version
	origCommit := GitCommit
	origBuild := BuildTime
	defer func() {
		Version = origVersion
		GitCommit = origCommit
		BuildTime = origBuild
	}()

	Version = "0.0.0-test"
	GitCommit = "deadbeef"
	BuildTime = "never"

	root := NewRootCommand()
	root.SetArgs([]string{"version"})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("version command error: %v", err)
		}
	})

	expected := fmt.Sprintf("opsagent %s (commit: %s, built: %s)\n", "0.0.0-test", "deadbeef", "never")
	if output != expected {
		t.Errorf("version output = %q, want %q", output, expected)
	}
}

func TestValidateCommand_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Write a minimal valid config (no inputs = scheduler disabled, which is ok).
	err := os.WriteFile(configPath, []byte(`
agent:
  id: test-agent
  name: test
server:
  listen_addr: ":0"
grpc:
  server_addr: "localhost:50051"
executor:
  allowed_commands: ["echo"]
  timeout_seconds: 10
  max_output_bytes: 1024
`), 0644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCommand()
	root.SetArgs([]string{"validate", "--config", configPath})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("validate command error: %v", err)
		}
	})

	if !strings.Contains(output, "Config loaded successfully") {
		t.Errorf("expected success message, got: %q", output)
	}
	if !strings.Contains(output, "No inputs configured") {
		t.Errorf("expected 'No inputs configured' in output, got: %q", output)
	}
}

func TestValidateCommand_WithInputs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Write a config with inputs so the scheduler is built (non-nil).
	err := os.WriteFile(configPath, []byte(`
agent:
  id: test-agent
  name: test
  interval_seconds: 10
server:
  listen_addr: ":0"
grpc:
  server_addr: "localhost:50051"
executor:
  allowed_commands: ["echo"]
  timeout_seconds: 10
  max_output_bytes: 1024
collector:
  inputs:
    - type: cpu
      config: {}
`), 0644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCommand()
	root.SetArgs([]string{"validate", "--config", configPath})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("validate command error: %v", err)
		}
	})

	if !strings.Contains(output, "Config loaded successfully") {
		t.Errorf("expected success message, got: %q", output)
	}
	if !strings.Contains(output, "All inputs initialized") {
		t.Errorf("expected 'All inputs initialized' in output, got: %q", output)
	}
	if !strings.Contains(output, "agent.id:") {
		t.Errorf("expected config dump in output, got: %q", output)
	}
}

func TestPluginsCommand_WithConfig_PluginEnabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	// Write a config with plugin enabled.
	err := os.WriteFile(configPath, []byte(`
agent:
  id: test-agent
  name: test
server:
  listen_addr: ":0"
grpc:
  server_addr: "localhost:50051"
executor:
  allowed_commands: ["echo"]
  timeout_seconds: 10
  max_output_bytes: 1024
plugin:
  enabled: true
  socket_path: "/tmp/test.sock"
`), 0644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCommand()
	root.SetArgs([]string{"plugins", "--config", configPath})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("plugins command error: %v", err)
		}
	})

	if !strings.Contains(output, "Built-in plugins:") {
		t.Errorf("expected 'Built-in plugins:' in output, got: %q", output)
	}
	if !strings.Contains(output, "Plugin runtime:") {
		t.Errorf("expected 'Plugin runtime:' in output, got: %q", output)
	}
}

func TestPluginsCommand_WithConfig_BadConfig(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"plugins", "--config", "/nonexistent/path/config.yaml"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent config path")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPluginsCommand_WithConfig_GatewayEnabled(t *testing.T) {
	dir := t.TempDir()
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	// Write a config with plugin gateway enabled.
	err := os.WriteFile(configPath, []byte(fmt.Sprintf(`
agent:
  id: test-agent
  name: test
server:
  listen_addr: ":0"
grpc:
  server_addr: "localhost:50051"
executor:
  allowed_commands: ["echo"]
  timeout_seconds: 10
  max_output_bytes: 1024
plugin_gateway:
  enabled: true
  plugins_dir: %q
`, pluginsDir)), 0644)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCommand()
	root.SetArgs([]string{"plugins", "--config", configPath})

	output := captureStdout(t, func() {
		err := root.Execute()
		if err != nil {
			t.Fatalf("plugins command error: %v", err)
		}
	})

	if !strings.Contains(output, "Built-in plugins:") {
		t.Errorf("expected 'Built-in plugins:' in output, got: %q", output)
	}
}
