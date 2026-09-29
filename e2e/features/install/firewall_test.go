//go:build e2e_fleet

package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestInstall_firewallDefaultDeny: ufw is active, denies incoming by default
// and allows outgoing (core/pkg/install/firewall.go GenerateRules).
func TestInstall_firewallDefaultDeny(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, "ufw status verbose").Stdout
		if !strings.Contains(out, "Status: active") {
			t.Errorf("%s: ufw is not active:\n%s", n.Name, out)
		}
		if !strings.Contains(out, "deny (incoming)") || !strings.Contains(out, "allow (outgoing)") {
			t.Errorf("%s: ufw defaults are not deny incoming / allow outgoing:\n%s", n.Name, out)
		}
	}
}

// TestInstall_firewallRulesAreExactlyOramas: every allow rule is tagged
// `orama` (Reconcile owns only tagged rules, docs/SECURITY.md "Firewall: only
// Orama's rules"), the desired set is all there, and nothing outside it is
// open: SSH, WireGuard, HTTP(S), DNS on nameservers only, TURN only while the
// host relays, and the mesh only on wg0.
func TestInstall_firewallRulesAreExactlyOramas(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		turn := infra.HostRunsTURN(t, f, n)
		want := infra.DesiredPublicRules(n, turn)
		permitted := infra.DesiredPublicRules(n, true)
		rules := infra.UFWRules(t, f, n)
		for to := range want {
			if !infra.HasRule(rules, to, infra.TagOrama) {
				t.Errorf("%s: no allow rule for %s tagged %q", n.Name, to, infra.TagOrama)
			}
		}
		for _, r := range rules {
			if r.V6 || !strings.HasPrefix(r.Action, "ALLOW") {
				continue
			}
			if r.Comment != infra.TagOrama && r.Comment != infra.TagGlobal {
				t.Errorf("%s: untagged rule %s %s from %s (comment %q)", n.Name, r.To, r.Action, r.From, r.Comment)
			}
			if r.Comment == infra.TagOrama && !permitted[r.To] {
				t.Errorf("%s: an orama rule opens %s, which the desired set does not have", n.Name, r.To)
			}
		}
		for _, r := range rules {
			if r.To == infra.OverlayRuleTo && r.From != infra.WireGuardSubnet {
				t.Errorf("%s: the mesh rule admits %s, want %s", n.Name, r.From, infra.WireGuardSubnet)
			}
			if r.To != infra.OverlayRuleTo && strings.HasPrefix(r.From, "10.") {
				t.Errorf("%s: %s admits %s on any interface: a source address is not a credential", n.Name, r.To, r.From)
			}
		}
	}
}

// TestInstall_wireguardConntrackBypass: WireGuard traffic is accepted before
// ufw's conntrack "invalid" drop (core/pkg/install/firewall.go: the rule is
// inserted at position 1 of INPUT).
func TestInstall_wireguardConntrackBypass(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, "iptables -S INPUT").Stdout
		wgAt, ufwAt := -1, -1
		for i, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if wgAt < 0 && strings.Contains(l, "-i wg0") && strings.Contains(l, "-s 10.0.0.0/24") && strings.HasSuffix(l, "-j ACCEPT") {
				wgAt = i
			}
			if ufwAt < 0 && strings.Contains(l, "-j ufw-") {
				ufwAt = i
			}
		}
		if wgAt < 0 || (ufwAt >= 0 && wgAt > ufwAt) {
			t.Errorf("%s: the wg0 10.0.0.0/24 ACCEPT is missing or after ufw's chains (at %d, ufw at %d):\n%s", n.Name, wgAt, ufwAt, out)
		}
	}
}
