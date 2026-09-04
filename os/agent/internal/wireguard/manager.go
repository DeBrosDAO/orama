// Package wireguard manages the WireGuard interface for OramaOS.
//
// On OramaOS, the WireGuard kernel module is built-in (Linux 6.6+).
// Configuration is written during enrollment and persisted on the rootfs.
package wireguard

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const (
	// Interface is the WireGuard interface name.
	Interface = "wg0"

	// ConfigPath is the default WireGuard configuration file.
	ConfigPath = "/etc/wireguard/wg0.conf"

	// PrivateKeyPath stores the WG private key separately for identity derivation.
	PrivateKeyPath = "/etc/wireguard/private.key"
)

// Manager handles WireGuard interface lifecycle.
type Manager struct {
	iface string
}

// NewManager creates a new WireGuard manager.
func NewManager() *Manager {
	return &Manager{iface: Interface}
}

// Configure writes the WireGuard configuration to disk.
// Called during enrollment with config received from the Gateway.
func (m *Manager) Configure(config string) error {
	if err := os.MkdirAll("/etc/wireguard", 0700); err != nil {
		return fmt.Errorf("failed to create wireguard dir: %w", err)
	}

	if err := os.WriteFile(ConfigPath, []byte(config), 0600); err != nil {
		return fmt.Errorf("failed to write WG config: %w", err)
	}

	log.Println("WireGuard configuration written")
	return nil
}

// Up brings the WireGuard interface up using wg-quick.
func (m *Manager) Up() error {
	log.Println("bringing up WireGuard interface")

	cmd := exec.Command("wg-quick", "up", m.iface)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick up failed: %w\n%s", err, string(output))
	}

	log.Println("WireGuard interface is up")
	return nil
}

// Down takes the WireGuard interface down.
func (m *Manager) Down() error {
	cmd := exec.Command("wg-quick", "down", m.iface)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick down failed: %w\n%s", err, string(output))
	}

	log.Println("WireGuard interface is down")
	return nil
}

// GetPeers returns the current WireGuard peer list with their IPs.
func (m *Manager) GetPeers() ([]string, error) {
	cmd := exec.Command("wg", "show", m.iface, "allowed-ips")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wg show failed: %w", err)
	}

	var ips []string
	for _, line := range splitLines(string(output)) {
		if line == "" {
			continue
		}
		// Format: "<pubkey>\t<ip>/32\n"
		parts := splitTabs(line)
		if len(parts) >= 2 {
			ip := parts[1]
			// Strip /32 suffix
			if idx := indexOf(ip, '/'); idx >= 0 {
				ip = ip[:idx]
			}
			ips = append(ips, ip)
		}
	}

	return ips, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitTabs(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// LocalIP returns this node's address on the overlay.
//
// The command receiver binds it rather than every interface, so a node whose
// overlay address cannot be determined does not get a receiver at all. Reading
// it from the interface rather than the config file means it reflects what the
// kernel actually has, not what was written at enrollment.
func (m *Manager) LocalIP() (string, error) {
	out, err := exec.Command("ip", "-4", "-o", "addr", "show", "dev", m.iface).Output()
	if err != nil {
		return "", fmt.Errorf("could not read the address of %s: %w", m.iface, err)
	}

	// "3: wg0    inet 10.0.0.4/24 scope global wg0\..."
	for _, line := range splitLines(string(out)) {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f != "inet" || i+1 >= len(fields) {
				continue
			}
			addr := fields[i+1]
			if idx := indexOf(addr, '/'); idx >= 0 {
				addr = addr[:idx]
			}
			if addr != "" {
				return addr, nil
			}
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address: WireGuard is not up", m.iface)
}
