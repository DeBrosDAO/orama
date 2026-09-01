package production

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ufwCommand builds an exec.Command for ufw, prepending sudo when the current
// process is not running as root. At install time the provisioner runs as root;
// at runtime the orama-node/gateway process runs as the unprivileged "orama"
// user and relies on the NOPASSWD sudoers rule for ufw (see provisioner.go).
// Without this, runtime firewall changes (AddWebRTCRules on `webrtc enable`)
// silently failed and TURN relay ports stayed firewalled.
func ufwCommand(args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("ufw", args...)
	}
	return exec.Command("sudo", append([]string{"ufw"}, args...)...)
}

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
	SSHPort        int  // default 22
	IsNameserver   bool // enables port 53 TCP+UDP
	WireGuardPort  int  // default 51820
	TURNEnabled    bool // enables TURN relay ports (3478/udp+tcp, 5349/tcp, relay range)
	TURNRelayStart int  // start of TURN relay port range (default 49152)
	TURNRelayEnd   int  // end of TURN relay port range (default 65535)
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
		"ufw allow 443/tcp", // HTTPS (SNI router when installed; otherwise Caddy)
	}

	// DNS (only for nameserver nodes)
	if fp.config.IsNameserver {
		rules = append(rules, "ufw allow 53/tcp")
		rules = append(rules, "ufw allow 53/udp")
	}

	// TURN relay (only for nodes running TURN servers)
	if fp.config.TURNEnabled {
		rules = append(rules, "ufw allow 3478/udp") // TURN standard port (UDP)
		rules = append(rules, "ufw allow 3478/tcp") // TURN standard port (TCP fallback)
		rules = append(rules, "ufw allow 5349/tcp") // TURNS (TURN over TLS/TCP)
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
	if err := fp.persistRAMHygiene(); err != nil {
		return fmt.Errorf("failed to persist RAM hygiene: %w", err)
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

// persistRAMHygiene turns off swap, disables suid core dumps, and stops
// systemd-coredump from writing crash images to disk (bugboard #233).
func (fp *FirewallProvisioner) persistRAMHygiene() error {
	_ = exec.Command("swapoff", "-a").Run()
	_ = exec.Command("systemctl", "mask", "swap.target").Run()

	sysctl := "# Orama: keep secret-bearing pages off the block device\nfs.suid_dumpable = 0\n"
	cmd := exec.Command("tee", "/etc/sysctl.d/99-orama-ram-hygiene.conf")
	cmd.Stdin = strings.NewReader(sysctl)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write ram-hygiene sysctl: %w\n%s", err, string(output))
	}
	_ = exec.Command("sysctl", "--system").Run()

	if err := exec.Command("mkdir", "-p", "/etc/systemd/coredump.conf.d").Run(); err != nil {
		return fmt.Errorf("mkdir coredump.conf.d: %w", err)
	}
	coredump := "[Coredump]\nStorage=none\nProcessSizeMax=0\n"
	cmd = exec.Command("tee", "/etc/systemd/coredump.conf.d/orama.conf")
	cmd.Stdin = strings.NewReader(coredump)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write coredump.conf: %w\n%s", err, string(output))
	}
	return nil
}

// IsActive checks if UFW is active
func (fp *FirewallProvisioner) IsActive() bool {
	cmd := ufwCommand("status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "Status: active")
}

// AddWebRTCRules dynamically adds TURN port rules without a full firewall reset.
// Used when enabling WebRTC on a namespace.
func (fp *FirewallProvisioner) AddWebRTCRules(relayStart, relayEnd int) error {
	// ufw argv (the "ufw" binary is supplied by ufwCommand). Built as arg
	// slices rather than strings so the sudo-aware helper runs them directly.
	rules := [][]string{
		{"allow", "3478/udp"},
		{"allow", "3478/tcp"},
		{"allow", "5349/tcp"},
	}
	if relayStart > 0 && relayEnd > 0 {
		rules = append(rules, []string{"allow", fmt.Sprintf("%d:%d/udp", relayStart, relayEnd)})
	}

	for _, args := range rules {
		if output, err := ufwCommand(args...).CombinedOutput(); err != nil {
			return fmt.Errorf("failed to add firewall rule 'ufw %s': %w\n%s", strings.Join(args, " "), err, string(output))
		}
	}
	return nil
}

// RemoveWebRTCRules dynamically removes TURN port rules without a full firewall reset.
// Used when disabling WebRTC on a namespace.
func (fp *FirewallProvisioner) RemoveWebRTCRules(relayStart, relayEnd int) error {
	rules := [][]string{
		{"delete", "allow", "3478/udp"},
		{"delete", "allow", "3478/tcp"},
		{"delete", "allow", "5349/tcp"},
	}
	if relayStart > 0 && relayEnd > 0 {
		rules = append(rules, []string{"delete", "allow", fmt.Sprintf("%d:%d/udp", relayStart, relayEnd)})
	}

	for _, args := range rules {
		// Ignore errors on delete — rule may not exist
		ufwCommand(args...).CombinedOutput()
	}
	return nil
}

// GetStatus returns the current UFW status
func (fp *FirewallProvisioner) GetStatus() (string, error) {
	cmd := ufwCommand("status", "verbose")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ufw status: %w\n%s", err, string(output))
	}
	return string(output), nil
}
