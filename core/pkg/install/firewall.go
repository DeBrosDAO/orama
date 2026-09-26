package install

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// ufwCommand builds an exec.Command for ufw run as root. At install time the
// provisioner is root and runs ufw directly; at runtime orama-node runs as the
// unprivileged "orama" user and goes through orama-privhelper, which allows
// only the TURN rules (pkg/privhelper). Without a root path, runtime
// firewall changes (AddWebRTCRules on `webrtc enable`) silently failed and
// TURN relay ports stayed firewalled.
func ufwCommand(args ...string) *exec.Cmd {
	return privhelper.Command(privhelper.ToolUFW, args...)
}

// defaultTURNRelayPortStart / defaultTURNRelayPortEnd are the full TURN relay
// UDP port range — a superset of every namespace's per-tenant sub-range. Phase
// 6b opens this whole range on any node that hosts a TURN instance (bugboard
// #846) so the firewall reset never closes the relay.
const (
	// ownedRuleComment tags every allow rule Reconcile adds. Reconcile removes
	// only tagged rules, so an operator's own rules — a tailscale0 interface
	// allow, a monitoring port — are never Orama's to delete.
	ownedRuleComment = "orama"

	defaultTURNRelayPortStart = 49152
	defaultTURNRelayPortEnd   = 65535

	// overlayAllowRule admits the WireGuard mesh, on the WireGuard interface
	// only (the argument form `ufw allow` takes).
	overlayAllowRule = "in on " + wireGuardInterface + " from " + constants.WireGuardSubnet

	// wireGuardInterface is the mesh interface wg-quick brings up.
	wireGuardInterface = "wg0"
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

// GenerateRules returns the desired firewall state as a list of commands.
//
// It no longer begins with `ufw --force reset`. The reset ran on every upgrade,
// with every service already up: between it and the closing `ufw --force
// enable` the node was firewalled to nothing and then to default-deny with no
// rules. That window is why the TURN relay range needed a dedicated re-add to
// survive an upgrade (bug #846) — a symptom of resetting a live firewall, not
// of TURN. Reconcile converges to this set instead, adding what is missing and
// removing what is extra.
func (fp *FirewallProvisioner) GenerateRules() []string {
	rules := []string{
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

	// Everything from the mesh, and only when it arrives on the mesh. A source
	// address is not a credential: without `in on wg0`, a packet sourced from
	// 10.0.0.0/24 on the public interface — which another tenant of the same
	// provider network, or a spoofed UDP datagram, can produce — was admitted
	// to every internal port. Arriving through wg0 means WireGuard
	// authenticated the peer's key.
	rules = append(rules, fmt.Sprintf("ufw allow %s", overlayAllowRule))

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

// DesiredAllowRules returns just the `ufw allow` rules this node should have,
// with no reset and no enable. The set Reconcile compares against.
func (fp *FirewallProvisioner) DesiredAllowRules() []string {
	var allows []string
	for _, r := range fp.GenerateRules() {
		if strings.HasPrefix(r, "ufw allow ") {
			allows = append(allows, strings.TrimPrefix(r, "ufw allow "))
		}
	}
	return allows
}

// Reconcile brings the live firewall to the desired rule set without ever
// taking it down: it adds what is missing and removes what is extra.
//
// `ufw allow` is idempotent on its own — re-adding an existing rule is a no-op,
// and re-adding an untagged one tags it — so a correct rule set costs nothing
// and changes nothing, which is the property an upgrade needs.
//
// "Extra" means a rule Orama tagged and no longer wants. Rules without the tag
// belong to someone else: the operator, or the TURN rules orama-node opens at
// runtime through orama-privhelper — with one exception, the exact untagged
// rules older Orama releases added before rules were tagged (legacyAllowRules),
// which nothing else would ever remove.
func (fp *FirewallProvisioner) Reconcile() error {
	if err := fp.Install(); err != nil {
		return err
	}

	status, err := readUFWStatus()
	if err != nil {
		return err
	}
	rows := parseAllowRows(status)

	desired := fp.DesiredAllowRules()
	wanted := make(map[string]bool, len(desired))
	for _, rule := range desired {
		wanted[rule] = true
		if err := runFirewall("ufw", ownedAllowArgs(rule)...); err != nil {
			return fmt.Errorf("add firewall rule %q: %w", rule, err)
		}
	}

	// Everything that is not an allow rule: default policies, IPv6, enable,
	// and the conntrack bypass. All idempotent, none of them a reset. They run
	// before any removal so a rule that cannot be removed never leaves the
	// firewall disabled or its policies unset.
	for _, cmd := range fp.GenerateRules() {
		if strings.HasPrefix(cmd, "ufw allow ") {
			continue
		}
		parts := strings.Fields(cmd)
		if err := runFirewall(parts[0], parts[1:]...); err != nil {
			return fmt.Errorf("apply %q: %w", cmd, err)
		}
	}

	for _, rule := range ownedAllowRules(rows) {
		if wanted[rule] {
			continue
		}
		if err := runFirewall("ufw", append([]string{"delete", "allow"}, strings.Fields(rule)...)...); err != nil {
			return fmt.Errorf("remove firewall rule %q: %w", rule, err)
		}
	}

	for _, rule := range legacyRulesToRemove(rows, fp.config.SSHPort, wanted) {
		if err := runFirewall("ufw", append([]string{"delete", "allow"}, strings.Fields(rule)...)...); err != nil {
			return fmt.Errorf("remove the untagged rule %q an older Orama added: %w", rule, err)
		}
	}

	if err := fp.persistIPv6Disable(); err != nil {
		return fmt.Errorf("failed to persist IPv6 disable: %w", err)
	}
	if err := fp.persistRAMHygiene(); err != nil {
		return fmt.Errorf("failed to persist RAM hygiene: %w", err)
	}
	return nil
}

// readUFWStatus is `ufw status`, which parseAllowRows reads.
func readUFWStatus() (string, error) {
	out, err := exec.Command("ufw", "status").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read ufw status: %w\n%s", err, string(out))
	}
	return string(out), nil
}

// ownedAllowArgs is the `ufw` argument list that adds rule with Orama's tag.
func ownedAllowArgs(rule string) []string {
	args := append([]string{"allow"}, strings.Fields(rule)...)
	return append(args, "comment", ownedRuleComment)
}

func runFirewall(name string, args ...string) error {
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%w\n%s", err, string(output))
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

// AddWebRTCRules dynamically adds TURN port rules without a full firewall reset.
// Used when enabling WebRTC on a namespace.
func (fp *FirewallProvisioner) AddWebRTCRules(relayStart, relayEnd int) error {
	for _, args := range webRTCRuleArgs(relayStart, relayEnd) {
		if output, err := ufwCommand(args...).CombinedOutput(); err != nil {
			return fmt.Errorf("failed to add firewall rule 'ufw %s': %w\n%s", strings.Join(args, " "), err, string(output))
		}
	}
	return nil
}

// webRTCRuleArgs is the ufw argv (after "ufw") of each TURN rule. Built as arg
// slices rather than strings so the privileged helper receives them intact.
func webRTCRuleArgs(relayStart, relayEnd int) [][]string {
	rules := [][]string{
		{"allow", "3478/udp"},
		{"allow", "3478/tcp"},
		{"allow", "5349/tcp"},
	}
	if relayStart > 0 && relayEnd > 0 {
		rules = append(rules, []string{"allow", fmt.Sprintf("%d:%d/udp", relayStart, relayEnd)})
	}
	return rules
}
