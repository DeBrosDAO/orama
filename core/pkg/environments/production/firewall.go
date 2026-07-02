package production

import (
	"fmt"
	"os/exec"
	"strings"
)

// defaultTURNRelayPortStart / defaultTURNRelayPortEnd are the full TURN relay
// UDP port range — a superset of every namespace's per-tenant sub-range. Phase
// 6b opens this whole range on any node that hosts a TURN instance (bugboard
// #846) so the firewall reset never closes the relay.
const (
	defaultTURNRelayPortStart = 49152
	defaultTURNRelayPortEnd   = 65535
)

// FirewallConfig holds the configuration for UFW firewall rules
type FirewallConfig struct {
	SSHPort       int  // default 22
	IsNameserver  bool // enables port 53 TCP+UDP
	AnyoneORPort  int  // 0 = disabled, typically 9001
	WireGuardPort int  // default 51820
	TURNEnabled   bool // enables TURN relay ports (3478/udp+tcp, 5349/tcp, relay range)
	TURNRelayStart int // start of TURN relay port range (default 49152)
	TURNRelayEnd   int // end of TURN relay port range (default 65535)
}

// FirewallProvisioner manages UFW firewall setup
type FirewallProvisioner struct {
	config FirewallConfig
}

// NewFirewallProvisioner creates a new firewall provisioner
func NewFirewallProvisioner(config FirewallConfig) *FirewallProvisioner {
	if config.SSHPort == 0 {
		config.SSHPort = 22
	}
	if config.WireGuardPort == 0 {
		config.WireGuardPort = 51820
	}
	return &FirewallProvisioner{
		config: config,
	}
}

// IsInstalled checks if UFW is available
func (fp *FirewallProvisioner) IsInstalled() bool {
	_, err := exec.LookPath("ufw")
	return err == nil
}

// Install installs UFW if not present
func (fp *FirewallProvisioner) Install() error {
	if fp.IsInstalled() {
		return nil
	}

	cmd := exec.Command("apt-get", "install", "-y", "ufw")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install ufw: %w\n%s", err, string(output))
	}

	return nil
}

// GenerateRules returns the list of UFW commands to apply
func (fp *FirewallProvisioner) GenerateRules() []string {
	rules := []string{
		// Reset to clean state
		"ufw --force reset",

		// Default policies
		"ufw default deny incoming",
		"ufw default allow outgoing",

		// SSH (always required)
		fmt.Sprintf("ufw allow %d/tcp", fp.config.SSHPort),

		// WireGuard (always required for mesh)
		fmt.Sprintf("ufw allow %d/udp", fp.config.WireGuardPort),

		// Public web services
		"ufw allow 80/tcp",  // ACME / HTTP redirect
		"ufw allow 443/tcp", // HTTPS (Caddy → Gateway)
	}

	// DNS (only for nameserver nodes)
	if fp.config.IsNameserver {
		rules = append(rules, "ufw allow 53/tcp")
		rules = append(rules, "ufw allow 53/udp")
	}

	// Anyone relay ORPort
	if fp.config.AnyoneORPort > 0 {
		rules = append(rules, fmt.Sprintf("ufw allow %d/tcp", fp.config.AnyoneORPort))
	}

	// TURN relay (only for nodes running TURN servers)
	if fp.config.TURNEnabled {
		rules = append(rules, "ufw allow 3478/udp")  // TURN standard port (UDP)
		rules = append(rules, "ufw allow 3478/tcp")  // TURN standard port (TCP fallback)
		rules = append(rules, "ufw allow 5349/tcp")  // TURNS (TURN over TLS/TCP)
		if fp.config.TURNRelayStart > 0 && fp.config.TURNRelayEnd > 0 {
			rules = append(rules, fmt.Sprintf("ufw allow %d:%d/udp", fp.config.TURNRelayStart, fp.config.TURNRelayEnd))
		}
	}

	// Allow all traffic from WireGuard subnet (inter-node encrypted traffic)
	rules = append(rules, "ufw allow from 10.0.0.0/24")

	// Disable IPv6 — no ip6tables rules exist, so services bound to 0.0.0.0
	// may be reachable via IPv6. Disable it entirely at the kernel level.
	rules = append(rules, "sysctl -w net.ipv6.conf.all.disable_ipv6=1")
	rules = append(rules, "sysctl -w net.ipv6.conf.default.disable_ipv6=1")

	// Enable firewall
	rules = append(rules, "ufw --force enable")

	// Accept all WireGuard traffic before conntrack can classify it as "invalid".
	// UFW's built-in "ct state invalid → DROP" runs before user rules like
	// "allow from 10.0.0.0/8". Packets arriving through the WireGuard tunnel
	// can be misclassified as "invalid" by conntrack due to reordering/jitter
	// (especially between high-latency peers), causing silent packet drops.
	// Inserting at position 1 in INPUT ensures this runs before UFW chains.
	rules = append(rules, "iptables -I INPUT 1 -i wg0 -s 10.0.0.0/24 -j ACCEPT")

	return rules
}

// Setup applies all firewall rules. Idempotent — safe to call multiple times.
func (fp *FirewallProvisioner) Setup() error {
	if err := fp.Install(); err != nil {
		return err
	}

	rules := fp.GenerateRules()

	for _, rule := range rules {
		parts := strings.Fields(rule)
		cmd := exec.Command(parts[0], parts[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to apply firewall rule '%s': %w\n%s", rule, err, string(output))
		}
	}

	// Persist IPv6 disable across reboots
	if err := fp.persistIPv6Disable(); err != nil {
		return fmt.Errorf("failed to persist IPv6 disable: %w", err)
	}

	return nil
}

// persistIPv6Disable writes a sysctl config to disable IPv6 on boot.
func (fp *FirewallProvisioner) persistIPv6Disable() error {
	content := "# Orama Network: disable IPv6 (no ip6tables rules configured)\nnet.ipv6.conf.all.disable_ipv6 = 1\nnet.ipv6.conf.default.disable_ipv6 = 1\n"
	cmd := exec.Command("tee", "/etc/sysctl.d/99-orama-disable-ipv6.conf")
	cmd.Stdin = strings.NewReader(content)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write sysctl config: %w\n%s", err, string(output))
	}
	return nil
}

// IsActive checks if UFW is active
func (fp *FirewallProvisioner) IsActive() bool {
	cmd := exec.Command("ufw", "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "Status: active")
}

// AddWebRTCRules dynamically adds TURN port rules without a full firewall reset.
// Used when enabling WebRTC on a namespace.
func (fp *FirewallProvisioner) AddWebRTCRules(relayStart, relayEnd int) error {
	rules := []string{
		"ufw allow 3478/udp",
		"ufw allow 3478/tcp",
		"ufw allow 5349/tcp",
	}
	if relayStart > 0 && relayEnd > 0 {
		rules = append(rules, fmt.Sprintf("ufw allow %d:%d/udp", relayStart, relayEnd))
	}

	for _, rule := range rules {
		parts := strings.Fields(rule)
		cmd := exec.Command(parts[0], parts[1:]...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to add firewall rule '%s': %w\n%s", rule, err, string(output))
		}
	}
	return nil
}

// RemoveWebRTCRules dynamically removes TURN port rules without a full firewall reset.
// Used when disabling WebRTC on a namespace.
func (fp *FirewallProvisioner) RemoveWebRTCRules(relayStart, relayEnd int) error {
	rules := []string{
		"ufw delete allow 3478/udp",
		"ufw delete allow 3478/tcp",
		"ufw delete allow 5349/tcp",
	}
	if relayStart > 0 && relayEnd > 0 {
		rules = append(rules, fmt.Sprintf("ufw delete allow %d:%d/udp", relayStart, relayEnd))
	}

	for _, rule := range rules {
		parts := strings.Fields(rule)
		cmd := exec.Command(parts[0], parts[1:]...)
		// Ignore errors on delete — rule may not exist
		cmd.CombinedOutput()
	}
	return nil
}

// GetStatus returns the current UFW status
func (fp *FirewallProvisioner) GetStatus() (string, error) {
	cmd := exec.Command("ufw", "status", "verbose")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ufw status: %w\n%s", err, string(output))
	}
	return string(output), nil
}
