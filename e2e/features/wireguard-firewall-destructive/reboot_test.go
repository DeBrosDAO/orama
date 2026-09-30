//go:build e2e_fleet

package wireguardfirewalldestructive

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// wgIfindex is wg0's interface index: it changes exactly when wg0 is torn
// down and brought up again.
func wgIfindex(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	return strings.TrimSpace(f.MustExec(t, n, "cat /sys/class/net/wg0/ifindex").Stdout)
}

// TestReboot_overlayAndHardeningPersist: after a reboot the overlay comes up
// on its own, the firewall, IPv6 off and swap off are all still in force, and
// the node rejoins the cluster (docs/ARCHITECTURE.md "The mesh comes up at
// boot on its own"; docs/SECURITY.md persisted sysctls).
func TestReboot_overlayAndHardeningPersist(t *testing.T) {
	f := harness.Fleet(t)
	// HealthyAround: a reboot or restart that fails still ends with the
	// cluster waited for, before the next test.
	r := infra.HealthyAround(t)
	n := infra.Followers(t, r)[0]
	rulesBefore := infra.UFWRules(t, f, n)
	infra.Reboot(t, f, n)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" to rejoin after its reboot")
	if s := f.Unit(t, n, infra.WireGuardUnit); s != "active" {
		t.Errorf("%s after reboot: %s is %s", n.Name, infra.WireGuardUnit, s)
	}
	if s := f.Unit(t, n, infra.LeftoverWireGuardUnit); s == "active" {
		t.Errorf("%s after reboot: the leftover %s started", n.Name, infra.LeftoverWireGuardUnit)
	}
	after := infra.UFWRules(t, f, n)
	for _, r := range rulesBefore {
		if !infra.HasRule(after, r.To, r.Comment) && strings.HasPrefix(r.Action, "ALLOW") && r.Comment != "" {
			t.Errorf("%s after reboot lost the rule %s (%s)", n.Name, r.To, r.Comment)
		}
	}
	checks := map[string]string{
		"sysctl -n net.ipv6.conf.all.disable_ipv6": "1",
		"sysctl -n fs.suid_dumpable":               "0",
		"swapon --noheadings --show | wc -l":       "0",
		"ufw status | head -1":                     "Status: active",
	}
	for cmd, want := range checks {
		if got := strings.TrimSpace(f.MustExec(t, n, cmd).Stdout); got != want {
			t.Errorf("%s after reboot: %q gave %q, want %q", n.Name, cmd, got, want)
		}
	}
}

// TestNodeRestart_keepsTheOverlayUp: `orama node restart` restarts the
// supervisor and every daemon but not wg0: restarting used to tear the mesh
// down and sever every raft and memberlist on the node (docs/ARCHITECTURE.md
// "PartOf propagates restart").
func TestNodeRestart_keepsTheOverlayUp(t *testing.T) {
	f := harness.Fleet(t)
	// HealthyAround: a reboot or restart that fails still ends with the
	// cluster waited for, before the next test.
	r := infra.HealthyAround(t)
	n := infra.Followers(t, r)[0]
	before := wgIfindex(t, f, n)
	out := infra.OnNode(t, f, n, "node", "restart")
	if out.Exit != infra.ExitOK {
		t.Fatalf("orama node restart on %s: exit %d\n%s", n.Name, out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	if after := wgIfindex(t, f, n); after != before {
		t.Errorf("orama node restart recreated wg0 on %s (ifindex %s -> %s)", n.Name, before, after)
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, n.Name+" to rejoin after orama node restart")
}
