package sandbox

import "testing"

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
