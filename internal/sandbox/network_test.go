package sandbox

import (
	"strings"
	"testing"
)

func TestNewNetworkManager(t *testing.T) {
	nm := NewNetworkManager(true)
	if nm == nil {
		t.Fatal("expected non-nil NetworkManager")
	}
}

func TestSetupIsolatedNetworkIsNoOp(t *testing.T) {
	nm := NewNetworkManager(true)
	if err := nm.SetupIsolatedNetwork("task-001"); err != nil {
		t.Fatalf("expected no-op success, got: %v", err)
	}
}

func TestSetupAllowlistNetworkDisabled(t *testing.T) {
	nm := NewNetworkManager(false)
	err := nm.SetupAllowlistNetwork("task-001", []string{"10.0.0.1"})
	if err == nil {
		t.Fatal("expected error when network manager is disabled")
	}
}

func TestTruncateID(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"abcdefgh", 8, "abcdefgh"},
		{"abcdefghij", 8, "abcdefgh"},
		{"abc", 8, "abc"},
		{"", 8, ""},
	}
	for _, tc := range tests {
		t.Run(tc.s, func(t *testing.T) {
			got := truncateID(tc.s, tc.n)
			if got != tc.want {
				t.Errorf("truncateID(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

func TestCleanupNetwork(t *testing.T) {
	nm := NewNetworkManager(true)
	// Cleanup should succeed even if the veth doesn't exist.
	if err := nm.CleanupNetwork("nonexistent-task"); err != nil {
		t.Fatalf("expected no error for cleanup of nonexistent task, got: %v", err)
	}
}

func TestRunCmd_Success(t *testing.T) {
	// "true" is a simple command that always succeeds.
	if err := runCmd("true"); err != nil {
		t.Fatalf("expected 'true' to succeed, got: %v", err)
	}
}

func TestRunCmd_Error(t *testing.T) {
	// "false" always exits with code 1.
	err := runCmd("false")
	if err == nil {
		t.Fatal("expected 'false' to return an error")
	}
	// Verify error wrapping includes the command name and an ExitError.
	if !strings.Contains(err.Error(), "false") {
		t.Errorf("expected error to contain 'false', got: %v", err)
	}
}

func TestRunCmd_NonexistentCommand(t *testing.T) {
	err := runCmd("definitely-does-not-exist-command-12345")
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
	if !strings.Contains(err.Error(), "definitely-does-not-exist-command-12345") {
		t.Errorf("expected error to contain command name, got: %v", err)
	}
}

func TestRunCmd_WithArgs(t *testing.T) {
	// "echo" with args should succeed.
	if err := runCmd("echo", "hello", "world"); err != nil {
		t.Fatalf("expected 'echo hello world' to succeed, got: %v", err)
	}
}
