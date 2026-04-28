package sandbox

import (
	"fmt"
	"os/exec"
)

// NetworkManager manages network isolation for sandbox tasks.
type NetworkManager struct {
	enabled bool
}

// NewNetworkManager creates a NetworkManager.
// The enabled flag indicates whether network isolation features are active.
func NewNetworkManager(enabled bool) *NetworkManager {
	return &NetworkManager{enabled: enabled}
}

// SetupIsolatedNetwork prepares network isolation for a task.
// In the default mode, nsjail handles isolation via --disable_clone_newnet,
// so this is a no-op.
func (nm *NetworkManager) SetupIsolatedNetwork(taskID string) error {
	// nsjail handles network isolation natively via --disable_clone_newnet.
	return nil
}

// SetupAllowlistNetwork creates a veth pair and iptables rules that only
// allow traffic to the specified IP addresses for the given task.
func (nm *NetworkManager) SetupAllowlistNetwork(taskID string, allowedIPs []string) error {
	if !nm.enabled {
		return fmt.Errorf("network manager is not enabled")
	}

	vethHost := fmt.Sprintf("veth-h-%s", truncateID(taskID, 8))
	vethGuest := fmt.Sprintf("veth-g-%s", truncateID(taskID, 8))

	// Create veth pair.
	if err := runCmd("ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethGuest); err != nil {
		return fmt.Errorf("create veth pair: %w", err)
	}

	// Bring up host-side veth.
	if err := runCmd("ip", "link", "set", vethHost, "up"); err != nil {
		return fmt.Errorf("bring up %s: %w", vethHost, err)
	}

	// Set up iptables rules to allow only the specified IPs.
	for _, ip := range allowedIPs {
		if err := runCmd("iptables", "-A", "OUTPUT", "-o", vethHost, "-d", ip, "-j", "ACCEPT"); err != nil {
			return fmt.Errorf("iptables allow %s: %w", ip, err)
		}
	}

	// Drop everything else on this veth.
	if err := runCmd("iptables", "-A", "OUTPUT", "-o", vethHost, "-j", "DROP"); err != nil {
		return fmt.Errorf("iptables default drop: %w", err)
	}

	return nil
}

// CleanupNetwork removes the veth pair for the given task, which also
// removes associated iptables rules.
func (nm *NetworkManager) CleanupNetwork(taskID string) error {
	vethHost := fmt.Sprintf("veth-h-%s", truncateID(taskID, 8))

	// Deleting one end of the veth pair removes both.
	if err := runCmd("ip", "link", "del", vethHost); err != nil {
		// Ignore "does not exist" errors during cleanup.
		return nil
	}
	return nil
}

// truncateID returns the first n characters of s, or s if shorter.
func truncateID(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// runCmd executes an external command and returns an error if it fails.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %v: %s: %w", name, args, string(out), err)
	}
	return nil
}
