package production

import (
	"fmt"
	"os/exec"
	"strings"
)

// FirewallConfig holds the configuration for UFW firewall rules
type FirewallConfig struct {
	SSHPort       int  // default 22
	IsNameserver  bool // enables port 53 TCP+UDP
	AnyoneORPort  int  // 0 = disabled, typically 9001
	WireGuardPort int  // default 51820
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

	// Allow all traffic from WireGuard subnet (inter-node encrypted traffic)
	rules = append(rules, "ufw allow from 10.0.0.0/8")

	// Enable firewall
	rules = append(rules, "ufw --force enable")

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

// GetStatus returns the current UFW status
func (fp *FirewallProvisioner) GetStatus() (string, error) {
	cmd := exec.Command("ufw", "status", "verbose")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ufw status: %w\n%s", err, string(output))
	}
	return string(output), nil
}
