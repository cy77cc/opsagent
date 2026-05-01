package sandbox

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizeTaskID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid simple", "task-123", "task-123", false},
		{"valid with dots", "task-1.2.3", "task-1.2.3", false},
		{"empty", "", "", true},
		{"path traversal dotdot", "../../etc/passwd", "", true},
		{"path traversal slash", "etc/passwd", "", true},
		{"path traversal backslash", `etc\passwd`, "", true},
		{"just dotdot", "..", "", true},
		{"dot only", ".", "", true},
		{"embedded dotdot", "task-../evil", "", true},
		{"null byte", "task\x00evil", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeTaskID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeTaskID(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("sanitizeTaskID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateCommandNameMetacharacters(t *testing.T) {
	// Use an empty allowed list so only the metacharacter check can catch injection.
	p := Policy{}
	tests := []struct {
		name    string
		command string
		wantErr bool
	}{
		{"valid command", "ls", false},
		{"semicolon injection", "ls;rm -rf /", true},
		{"pipe injection", "ls|cat", true},
		{"backtick injection", "ls`whoami`", true},
		{"dollar injection", "ls$(whoami)", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.ValidateCommand(tt.command, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCommand(%q) error = %v, wantErr %v", tt.command, err, tt.wantErr)
			}
		})
	}
}

func TestSeccompPolicyString(t *testing.T) {
	tests := []struct {
		name        string
		networkMode string
		wantNetwork bool
	}{
		{"disabled mode excludes network syscalls", "disabled", false},
		{"allowlist mode includes network syscalls", "allowlist", true},
		{"default excludes network syscalls", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NsjailConfig{NetworkMode: tt.networkMode}
			policy := cfg.seccompPolicyString()
			if tt.wantNetwork && !strings.Contains(policy, "socket") {
				t.Errorf("expected network syscalls in allowlist mode")
			}
			if !tt.wantNetwork && strings.Contains(policy, "socket") {
				t.Errorf("expected no network syscalls in %q mode", tt.networkMode)
			}
			// Verify fork bomb protection
			if strings.Contains(policy, "clone") || strings.Contains(policy, "fork") || strings.Contains(policy, "vfork") {
				t.Errorf("seccomp policy should not allow clone/fork/vfork")
			}
		})
	}
}

func TestBuildSandboxEnv(t *testing.T) {
	reqEnv := map[string]string{
		"MY_VAR":          "value",
		"LD_PRELOAD":      "/evil.so",
		"LD_LIBRARY_PATH": "/evil",
	}
	env := buildSandboxEnv(reqEnv)

	// Should contain PATH
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			found = true
		}
		if strings.HasPrefix(e, "LD_PRELOAD=") {
			t.Error("LD_PRELOAD should be blocked")
		}
		if strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			t.Error("LD_LIBRARY_PATH should be blocked")
		}
	}
	if !found {
		t.Error("PATH should be present")
	}
}

func TestBuildSandboxEnvBlocksDYLD(t *testing.T) {
	reqEnv := map[string]string{
		"DYLD_INSERT_LIBRARIES": "/evil.dylib",
		"SAFE_VAR":              "ok",
	}
	env := buildSandboxEnv(reqEnv)

	for _, e := range env {
		if strings.HasPrefix(e, "DYLD_INSERT_LIBRARIES=") {
			t.Error("DYLD_INSERT_LIBRARIES should be blocked")
		}
	}
	// SAFE_VAR should be present
	found := false
	for _, e := range env {
		if e == "SAFE_VAR=ok" {
			found = true
		}
	}
	if !found {
		t.Error("SAFE_VAR should be present")
	}
}

func TestWriteScriptFileUnpredictablePath(t *testing.T) {
	cfg := NsjailConfig{}
	path1, err := cfg.WriteScriptFile("task-1", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path1)

	path2, err := cfg.WriteScriptFile("task-1", "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path2)

	// Paths should be different even for same taskID (unpredictable)
	if path1 == path2 {
		t.Errorf("expected unpredictable paths, got same path twice: %s", path1)
	}

	// Verify file permissions
	info, err := os.Stat(path1)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}
