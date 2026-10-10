//go:build e2e_fleet

package wireguardfirewalldestructive

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Rules the reconcile test plants, each with the fate the code gives it
// (core/pkg/install/firewall.go Reconcile, firewall_legacy.go).
const (
	strayOwned    = "12346/tcp" // tagged orama, not wanted: removed
	operatorOwn   = "12347/tcp" // untagged, the operator's: kept
	globalOwned   = "12348/tcp" // tagged orama-global: never touched
	legacyAnon    = "9051/tcp"  // on the legacy list with its old comment: removed
	legacyComment = "Anon ControlPort"
	wantedHTTP    = "80/tcp" // desired, deleted by the test: re-added
)

// plantRules sets the firewall up for the reconcile and registers the
// cleanup that removes every planted rule and puts 80/tcp back, verified.
func plantRules(t testing.TB, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	t.Cleanup(func() { unplant(t, f, n) })
	f.MustExec(t, n, "ufw allow "+strayOwned+" comment "+infra.TagOrama)
	f.MustExec(t, n, "ufw allow "+operatorOwn)
	f.MustExec(t, n, "ufw allow "+globalOwned+" comment "+infra.TagGlobal)
	f.MustExec(t, n, "ufw allow "+legacyAnon+" comment "+fleet.ShellQuote(legacyComment))
	f.MustExec(t, n, "ufw delete allow "+wantedHTTP)
}

func unplant(t testing.TB, f *fleet.Fleet, n fleet.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	sh := f.SSH(ctx, n)
	cmd := "for r in " + strayOwned + " " + operatorOwn + " " + globalOwned + " " + legacyAnon +
		"; do while ufw status | grep -q \"^$r \"; do ufw --force delete allow $r >/dev/null || break; done; done; " +
		"ufw allow " + wantedHTTP + " comment " + infra.TagOrama + " >/dev/null && ufw status"
	out, err := sh.Run(ctx, cmd)
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup: restoring %s's firewall failed (exit %d): %v %s", n.Name, out.Exit, err, out.Stderr)
		return
	}
	rules := infra.ParseUFWRules(out.Stdout)
	if !infra.HasRule(rules, wantedHTTP, infra.TagOrama) {
		t.Errorf("cleanup: %s has no tagged %s rule after the restore", n.Name, wantedHTTP)
	}
	for _, r := range rules {
		switch r.To {
		case strayOwned, operatorOwn, globalOwned, legacyAnon:
			t.Errorf("cleanup: %s still has the planted rule %s", n.Name, r.To)
		}
	}
}

// TestFirewallReconcile_upgradeConvergesOnlyOwnedRules: an upgrade reconciles
// the firewall without resetting it: a missing wanted rule comes back, a
// tagged rule Orama no longer wants goes, an exact legacy rule goes, and the
// operator's untagged rule and the global node's orama-global rule stay
// (docs/whitepaper/technical-reference/vol1/30-install-and-upgrade.md "Firewall: only Orama's rules, including the old ones").
func TestFirewallReconcile_upgradeConvergesOnlyOwnedRules(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	n := infra.Followers(t, r)[0]
	plantRules(t, f, n)
	res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "node", "upgrade", "--env", f.State.Env, "--node", n.PublicIP, "--yes")
	infra.ExpectExit(t, res, infra.ExitOK)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after the reconciling upgrade")
	rules := infra.UFWRules(t, f, n)
	has := func(to string) bool {
		for _, r := range rules {
			if r.To == to && !r.V6 {
				return true
			}
		}
		return false
	}
	if !infra.HasRule(rules, wantedHTTP, infra.TagOrama) {
		t.Errorf("the reconcile did not put the wanted %s back", wantedHTTP)
	}
	if has(strayOwned) {
		t.Errorf("the reconcile kept the unwanted orama rule %s", strayOwned)
	}
	if has(legacyAnon) {
		t.Errorf("the reconcile kept the legacy rule %s (%s)", legacyAnon, legacyComment)
	}
	if !has(operatorOwn) {
		t.Errorf("the reconcile deleted the operator's untagged rule %s", operatorOwn)
	}
	if !infra.HasRule(rules, globalOwned, infra.TagGlobal) {
		t.Errorf("the reconcile touched the orama-global rule %s", globalOwned)
	}
	if out := f.MustExec(t, n, "ufw status"); !strings.Contains(out.Stdout, "Status: active") {
		t.Error("the firewall is not active after the reconcile")
	}
}
