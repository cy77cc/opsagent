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
