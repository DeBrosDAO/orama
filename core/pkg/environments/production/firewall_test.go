package production

import (
	"strings"
	"testing"
)

func TestFirewallProvisioner_GenerateRules_StandardNode(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{})

	rules := fp.GenerateRules()

	// Should contain defaults
	assertContainsRule(t, rules, "ufw --force reset")
	assertContainsRule(t, rules, "ufw default deny incoming")
	assertContainsRule(t, rules, "ufw default allow outgoing")
	assertContainsRule(t, rules, "ufw allow 22/tcp")
	assertContainsRule(t, rules, "ufw allow 51820/udp")
	assertContainsRule(t, rules, "ufw allow 80/tcp")
	assertContainsRule(t, rules, "ufw allow 443/tcp")
	assertContainsRule(t, rules, "ufw allow from 10.0.0.0/24")
	assertContainsRule(t, rules, "sysctl -w net.ipv6.conf.all.disable_ipv6=1")
	assertContainsRule(t, rules, "sysctl -w net.ipv6.conf.default.disable_ipv6=1")
	assertContainsRule(t, rules, "ufw --force enable")
	assertContainsRule(t, rules, "iptables -I INPUT 1 -i wg0 -s 10.0.0.0/24 -j ACCEPT")

	// Should NOT contain DNS or Anyone relay
	for _, rule := range rules {
		if strings.Contains(rule, "53/") {
			t.Errorf("standard node should not have DNS rule: %s", rule)
		}
		if strings.Contains(rule, "9001") {
			t.Errorf("standard node should not have Anyone relay rule: %s", rule)
		}
	}
}

// TestFirewallProvisioner_GenerateRules_NoTURN_NoRelayPorts confirms a non-TURN
// node never opens the relay ports (the TURN-enabled path is covered by
// TestFirewallProvisioner_GenerateRules_WithTURN). Bugboard #846.
func TestFirewallProvisioner_GenerateRules_NoTURN_NoRelayPorts(t *testing.T) {
	rules := NewFirewallProvisioner(FirewallConfig{}).GenerateRules()
	for _, rule := range rules {
		if strings.Contains(rule, "3478") || strings.Contains(rule, "5349") || strings.Contains(rule, "49152") {
			t.Errorf("non-TURN node should not open relay ports: %s", rule)
		}
	}
}

func TestFirewallProvisioner_GenerateRules_Nameserver(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		IsNameserver: true,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 53/tcp")
	assertContainsRule(t, rules, "ufw allow 53/udp")
}

func TestFirewallProvisioner_GenerateRules_WithAnyoneRelay(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		AnyoneORPort: 9001,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 9001/tcp")
}

func TestFirewallProvisioner_GenerateRules_CustomSSHPort(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort: 2222,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 2222/tcp")

	// Should NOT have default port 22
	for _, rule := range rules {
		if rule == "ufw allow 22/tcp" {
			t.Error("should not have default SSH port 22 when custom port is set")
		}
	}
}

func TestFirewallProvisioner_GenerateRules_WireGuardSubnetAllowed(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow from 10.0.0.0/24")
}

func TestFirewallProvisioner_GenerateRules_FullConfig(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort:       2222,
		IsNameserver:  true,
		AnyoneORPort:  9001,
		WireGuardPort: 51821,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 2222/tcp")
	assertContainsRule(t, rules, "ufw allow 51821/udp")
	assertContainsRule(t, rules, "ufw allow 53/tcp")
	assertContainsRule(t, rules, "ufw allow 53/udp")
	assertContainsRule(t, rules, "ufw allow 9001/tcp")
}

func TestFirewallProvisioner_GenerateRules_WithTURN(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		TURNEnabled:    true,
		TURNRelayStart: 49152,
		TURNRelayEnd:   49951,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 3478/udp")
	assertContainsRule(t, rules, "ufw allow 3478/tcp")
	assertContainsRule(t, rules, "ufw allow 5349/tcp")
	assertContainsRule(t, rules, "ufw allow 49152:49951/udp")
}

func TestFirewallProvisioner_DefaultPorts(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{})

	if fp.config.SSHPort != 22 {
		t.Errorf("default SSHPort = %d, want 22", fp.config.SSHPort)
	}
	if fp.config.WireGuardPort != 51820 {
		t.Errorf("default WireGuardPort = %d, want 51820", fp.config.WireGuardPort)
	}
}

func assertContainsRule(t *testing.T, rules []string, expected string) {
	t.Helper()
	for _, rule := range rules {
		if rule == expected {
			return
		}
	}
	t.Errorf("rules should contain '%s', got: %v", expected, rules)
}
