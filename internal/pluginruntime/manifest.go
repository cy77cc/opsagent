package pluginruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"gopkg.in/yaml.v3"
)

// PluginManifest represents a parsed plugin.yaml.
type PluginManifest struct {
	Name         string                 `yaml:"name"`
	Version      string                 `yaml:"version"`
	Description  string                 `yaml:"description"`
	Author       string                 `yaml:"author"`
	Runtime      string                 `yaml:"runtime"`
	BinaryPath   string                 `yaml:"binary_path"`
	Env          map[string]string      `yaml:"env"`
	TaskTypes    []string               `yaml:"task_types"`
	ConfigSchema map[string]interface{} `yaml:"config_schema"`
	Config       map[string]interface{} `yaml:"config"`
	Requirements *Requirements          `yaml:"requirements"`
	Limits       *Limits                `yaml:"limits"`
	HealthCheck  *HealthCheckConfig     `yaml:"health_check"`
	Sandbox      *SandboxConfig         `yaml:"sandbox"`

	// resolvedDir is the directory containing the manifest file.
	resolvedDir string
}

// Requirements specifies system requirements for the plugin.
type Requirements struct {
	MinKernelVersion string   `yaml:"min_kernel_version"`
	OS               []string `yaml:"os"`
}

// Limits specifies resource limits for the plugin process.
type Limits struct {
	MaxMemoryMB        int `yaml:"max_memory_mb"`
	MaxCPUPercent      int `yaml:"max_cpu_percent"`
	MaxConcurrentTasks int `yaml:"max_concurrent_tasks"`
	TimeoutSeconds     int `yaml:"timeout_seconds"`
}

// HealthCheckConfig specifies health check parameters.
type HealthCheckConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
	TimeoutSeconds  int `yaml:"timeout_seconds"`
}

// SandboxConfig specifies optional sandbox settings.
type SandboxConfig struct {
	Enabled       bool     `yaml:"enabled"`
	NetworkAccess bool     `yaml:"network_access"`
	AllowedPaths  []string `yaml:"allowed_paths"`
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)

// ParseManifest parses YAML bytes into a PluginManifest and validates it.
func ParseManifest(data []byte) (*PluginManifest, error) {
	var m PluginManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadManifest loads and parses a plugin.yaml from a file path.
// BinaryPath is resolved relative to the manifest directory.
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
	return m, nil
}

// Validate checks required fields and sane values.
func (m *PluginManifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("manifest: version %q is not valid semver", m.Version)
	}
	if m.BinaryPath == "" {
		return fmt.Errorf("manifest: binary_path is required")
	}
	if len(m.TaskTypes) == 0 {
		return fmt.Errorf("manifest: task_types must not be empty")
	}
	if m.Runtime == "" {
		m.Runtime = "process"
	}
	if m.Runtime != "process" {
		return fmt.Errorf("manifest: runtime must be 'process', got %q", m.Runtime)
	}
	if m.Requirements != nil && len(m.Requirements.OS) > 0 {
		currentOS := runtime.GOOS
		found := false
		for _, osName := range m.Requirements.OS {
			if osName == currentOS {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("manifest: unsupported OS %q, plugin requires %v", currentOS, m.Requirements.OS)
		}
	}
	return nil
}

// FullTaskType returns the namespaced task type: "plugin-name/task-type".
func FullTaskType(pluginName, taskType string) string {
	return pluginName + "/" + taskType
}
