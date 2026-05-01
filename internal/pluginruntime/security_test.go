package pluginruntime

import (
	"os"
	"path/filepath"
	"strings"
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

func TestBuildPluginEnv(t *testing.T) {
	manifestEnv := map[string]string{
		"MY_CONFIG":  "value",
		"LD_PRELOAD": "/evil.so",
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
