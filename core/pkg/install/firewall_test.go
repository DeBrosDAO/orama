package install

import (
	"strings"
	"testing"
)

func TestFirewallProvisioner_GenerateRules_StandardNode(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{})

	rules := fp.GenerateRules()

	// Never a reset. This runs on every upgrade with every service up; between
	// the reset and the closing enable the node is firewalled to nothing and
	// then to default-deny with no rules. Reconcile converges to this set
	// instead.
	for _, rule := range rules {
		if strings.Contains(rule, "reset") {
			t.Fatalf("GenerateRules still resets a live firewall: %q", rule)
		}
	}

	// Should contain defaults
	assertContainsRule(t, rules, "ufw default deny incoming")
	assertContainsRule(t, rules, "ufw default allow outgoing")
	assertContainsRule(t, rules, "ufw allow 22/tcp")
	assertContainsRule(t, rules, "ufw allow 51820/udp")
	assertContainsRule(t, rules, "ufw allow 80/tcp")
	assertContainsRule(t, rules, "ufw allow 443/tcp")
	assertContainsRule(t, rules, "ufw allow in on wg0 from 10.0.0.0/24")
	assertContainsRule(t, rules, "sysctl -w net.ipv6.conf.all.disable_ipv6=1")
	assertContainsRule(t, rules, "sysctl -w net.ipv6.conf.default.disable_ipv6=1")
	assertContainsRule(t, rules, "ufw --force enable")
	assertContainsRule(t, rules, "iptables -I INPUT 1 -i wg0 -s 10.0.0.0/24 -j ACCEPT")

	// Should NOT contain DNS or a Tor relay ORPort (the Tor client needs no inbound port)
	for _, rule := range rules {
		if strings.Contains(rule, "53/") {
			t.Errorf("standard node should not have DNS rule: %s", rule)
		}
		if strings.Contains(rule, "9001") {
			t.Errorf("standard node should not open a relay ORPort: %s", rule)
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

	assertContainsRule(t, rules, "ufw allow in on wg0 from 10.0.0.0/24")
}

func TestFirewallProvisioner_GenerateRules_FullConfig(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort:       2222,
		IsNameserver:  true,
		WireGuardPort: 51821,
	})

	rules := fp.GenerateRules()

	assertContainsRule(t, rules, "ufw allow 2222/tcp")
	assertContainsRule(t, rules, "ufw allow 51821/udp")
	assertContainsRule(t, rules, "ufw allow 53/tcp")
	assertContainsRule(t, rules, "ufw allow 53/udp")
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

// A correct live rule set must reconcile to no changes. That is the property
// an upgrade needs: running the firewall phase on a healthy node changes
// nothing and drops no packets.
func TestParseOwnedAllowRules_roundTrips_the_desired_set(t *testing.T) {
	fp := NewFirewallProvisioner(FirewallConfig{
		SSHPort:        22,
		WireGuardPort:  51820,
		IsNameserver:   true,
		TURNEnabled:    true,
		TURNRelayStart: 49152,
		TURNRelayEnd:   65535,
	})
	desired := fp.DesiredAllowRules()

	// The ufw status a node with exactly this rule set reports.
	var status strings.Builder
	status.WriteString("Status: active\n\nTo                         Action      From\n--                         ------      ----\n")
	for _, rule := range desired {
		switch {
		case strings.HasPrefix(rule, "in on wg0 from "):
			status.WriteString("Anywhere on wg0            ALLOW       " + strings.TrimPrefix(rule, "in on wg0 from ") + "                # orama\n")
		case strings.HasPrefix(rule, "from "):
			status.WriteString("Anywhere                   ALLOW       " + strings.TrimPrefix(rule, "from ") + "                # orama\n")
		default:
			status.WriteString(rule + "                   ALLOW       Anywhere                   # orama\n")
		}
	}

	live := parseOwnedAllowRules(status.String())

	liveSet := map[string]bool{}
	for _, r := range live {
		liveSet[r] = true
	}
	for _, want := range desired {
		if !liveSet[want] {
			t.Errorf("desired rule %q was not recognised in live status; reconcile would re-add it forever", want)
		}
	}

	desiredSet := map[string]bool{}
	for _, r := range desired {
		desiredSet[r] = true
	}
	for _, got := range live {
		if !desiredSet[got] {
			t.Errorf("live rule %q is not in the desired set; reconcile would delete a rule it just added", got)
		}
	}
}

// IPv6 mirror rows must be ignored. ufw creates one for every v4 rule; counting
// them as extra would make Reconcile delete rules it had just added, forever.
func TestParseOwnedAllowRules_ignores_v6_mirrors(t *testing.T) {
	status := `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere                   # orama
22/tcp (v6)                ALLOW       Anywhere (v6)              # orama
Anywhere                   ALLOW       10.0.0.0/24                # orama
`
	got := parseOwnedAllowRules(status)
	if len(got) != 2 {
		t.Fatalf("got %d rules %v, want 2 (the v6 mirror must be ignored)", len(got), got)
	}
	if got[0] != "22/tcp" || got[1] != "from 10.0.0.0/24" {
		t.Fatalf("got %v", got)
	}
}

// A DENY row is not an allow rule and must not be reported as one — Reconcile
// would then try to `ufw delete allow` a rule that does not exist.
func TestParseOwnedAllowRules_ignores_non_allow_rows(t *testing.T) {
	status := `To                         Action      From
--                         ------      ----
25/tcp                     DENY        Anywhere                   # orama
22/tcp                     ALLOW       Anywhere                   # orama
`
	got := parseOwnedAllowRules(status)
	if len(got) != 1 || got[0] != "22/tcp" {
		t.Fatalf("got %v, want just [22/tcp]", got)
	}
}

func TestParseOwnedAllowRules_empty_status(t *testing.T) {
	if got := parseOwnedAllowRules("Status: inactive\n"); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// The live status of a node running tailscale. Reading "Anywhere on tailscale0"
// as a port made Reconcile run `ufw delete allow Anywhere on tailscale0`, which
// ufw refuses, and every install on such a node failed its firewall phase. The
// operator's rules are not Orama's to delete even when they parse.
func TestParseOwnedAllowRules_leaves_the_operators_rules_alone(t *testing.T) {
	status := `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere                   # orama
Anywhere on tailscale0     ALLOW IN    Anywhere
9100/tcp                   ALLOW IN    Anywhere
3478/udp                   ALLOW IN    Anywhere
Anywhere                   ALLOW IN    10.0.0.0/24                # orama
Anywhere (v6) on tailscale0 ALLOW IN    Anywhere (v6)
`
	got := parseOwnedAllowRules(status)
	want := []string{"22/tcp", "from 10.0.0.0/24"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v: only tagged rules are Orama's", got, want)
	}
}

// A rule whose comment merely contains the tag is someone else's.
func TestParseOwnedAllowRules_needs_the_exact_tag(t *testing.T) {
	status := `22/tcp                     ALLOW       Anywhere                   # orama-legacy
80/tcp                     ALLOW       Anywhere                   # not orama either
`
	if got := parseOwnedAllowRules(status); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

func TestOwnedAllowArgs_tags_the_rule(t *testing.T) {
	for rule, want := range map[string]string{
		"22/tcp":           "allow 22/tcp comment orama",
		"from 10.0.0.0/24": "allow from 10.0.0.0/24 comment orama",
		"49152:65535/udp":  "allow 49152:65535/udp comment orama",
	} {
		if got := strings.Join(ownedAllowArgs(rule), " "); got != want {
			t.Errorf("ownedAllowArgs(%q) = %q, want %q", rule, got, want)
		}
	}
}

// The mesh is admitted on wg0 only. `ufw allow from 10.0.0.0/24` admitted a
// packet sourced from the overlay on the public interface too, where the
// source address is whatever the sender wrote.
func TestGenerateRules_admitsTheOverlayOnlyOnWireGuard(t *testing.T) {
	rules := NewFirewallProvisioner(FirewallConfig{}).GenerateRules()
	assertContainsRule(t, rules, "ufw allow in on wg0 from 10.0.0.0/24")
	for _, r := range rules {
		if r == "ufw allow from 10.0.0.0/24" {
			t.Fatal("the overlay is admitted on every interface")
		}
	}
}

// A live `in on wg0` rule reads back as the rule that added it, so Reconcile
// neither re-adds it forever nor deletes it as extra.
func TestParseOwnedAllowRules_readsTheInterfaceRule(t *testing.T) {
	status := `To                         Action      From
--                         ------      ----
Anywhere on wg0            ALLOW IN    10.0.0.0/24                # orama
Anywhere on tailscale0     ALLOW IN    Anywhere
22/tcp on eth1             ALLOW IN    Anywhere                   # orama
`
	got := parseOwnedAllowRules(status)
	if strings.Join(got, "|") != "in on wg0 from 10.0.0.0/24" {
		t.Fatalf("got %v, want just the wg0 overlay rule", got)
	}
	if args := strings.Join(ownedAllowArgs(got[0]), " "); args != "allow in on wg0 from 10.0.0.0/24 comment orama" {
		t.Errorf("ownedAllowArgs = %q", args)
	}
}
