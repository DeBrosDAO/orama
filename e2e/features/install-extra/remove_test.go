//go:build e2e_fleet

package installextra

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

func requireMember(t testing.TB, f *fleet.Fleet, extra harness.Extra, want int) {
	t.Helper()
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	if len(r.Nodes) != want {
		t.Fatalf("the cluster has %d nodes, want %d", len(r.Nodes), want)
	}
	if _, err := infra.ReportFor(r, extra.Node); err != nil {
		t.Fatal(err)
	}
}

// removeDryRunChangesNothing: --dry-run prints the quorum impact of every
// raft cluster the node votes in and the statements it would run, and
// changes nothing (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node remove").
func removeDryRunChangesNothing(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	res := harness.CLI(t).MustOK(t, "node", "remove", "--env", f.State.Env, "--node", extra.PublicIP, "--dry-run")
	for _, want := range []string{"Quorum after removing " + extra.PublicIP, "--dry-run, so nothing was changed",
		"remove raft member", "wipe " + extra.PublicIP} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("the dry run does not say %q:\n%s", want, res.Stdout)
		}
	}
	requireMember(t, f, extra, len(f.State.Nodes)+1)
	if f.Exec(t, extra.Node, "test -d /opt/orama/.orama").Exit != 0 {
		t.Fatal("a dry run wiped the node")
	}
}

// removeWithoutConfirmationAborts: without --force and with nobody to type
// "yes", the removal is declined: nothing happens, and the CLI says so with
// the aborted exit code (e2e/README.md exit codes: 7 aborted;
// core/cmd/orama/internal/clierr CodeAborted "the operator declined a
// confirmation").
func removeWithoutConfirmationAborts(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	res := infra.Run(t, harness.CLI(t), "node", "remove", "--env", f.State.Env, "--node", extra.PublicIP)
	if !strings.Contains(res.Stdout, "Aborted.") {
		t.Errorf("an unconfirmed removal did not say it aborted:\n%s", res.Stdout)
	}
	requireMember(t, f, extra, len(f.State.Nodes)+1)
	if res.Exit != infra.ExitAborted {
		t.Errorf("a declined removal exited %d, want %d (aborted): a script cannot tell it from a removal", res.Exit, infra.ExitAborted)
	}
}

// removeRetiresAndWipes: remove takes the node out of raft and the mesh on
// every survivor, then wipes it: no /opt/orama, no units, no wg0, no trust
// anchor, no orama-tagged firewall rule beyond SSH, and the operator's own
// rule untouched (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node remove", "orama node wipe").
func removeRetiresAndWipes(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	f.MustExec(t, extra.Node, "ufw allow "+operatorPort+" comment "+operatorComment)
	rep := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	wg, err := infra.WGIPOf(rep, extra.PublicIP)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := infra.ReportFor(rep, extra.Node)
	if err != nil {
		t.Fatal(err)
	}
	raftID := entry.Report.RQLite.NodeID
	res := infra.RunFor(t, harness.CLI(t), infra.InstallBudget, "node", "remove", "--env", f.State.Env, "--node", extra.PublicIP, "--force")
	infra.ExpectExit(t, res, infra.ExitOK, "tombstoned", "decommissioned and wiped")
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the core nodes without the removed node")
	eventually.Require(t, infra.PollEvery, infra.ConvergeBudget, "every view to forget "+wg, func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		return true, r.Forgotten(wg)
	})
	tomb := infra.IndexQuery(t, f, f.State.Nodes[0], "SELECT COUNT(*) FROM raft_evicted_nodes WHERE node_id = ?", raftID)
	if c, _ := tomb.Values[0][0].(float64); c != 1 {
		t.Errorf("no eviction tombstone for raft id %s: nothing stops it being re-added", raftID)
	}
	for _, gone := range []string{"/opt/orama", infra.ArchiveSigners, infra.WireGuardConfPath, "/etc/systemd/system/" + infra.NodeUnit} {
		if f.Exec(t, extra.Node, "test -e "+gone).Exit == 0 {
			t.Errorf("the wiped node still has %s", gone)
		}
	}
	if f.Exec(t, extra.Node, "ip link show wg0").Exit == 0 {
		t.Error("the wiped node still has wg0")
	}
	rules := infra.UFWRules(t, f, extra.Node)
	if !infra.HasRule(rules, operatorPort, operatorComment) {
		t.Errorf("the wipe removed the operator's own rule %s", operatorPort)
	}
	for _, r := range rules {
		if r.Comment == infra.TagOrama && r.To != "22/tcp" {
			t.Errorf("the wipe left the orama rule %s", r.To)
		}
	}
}

// removedNodeIsNoTarget: a removed node is gone from the environment, so
// remove and wipe refuse it by name, and the deprecated `clean` (which now
// runs wipe) says it is deprecated (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node clean").
func removedNodeIsNoTarget(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	cli := harness.CLI(t)
	notFound := "not found in the " + f.State.Env + " environment"
	infra.ExpectRefused(t, infra.Run(t, cli, "node", "remove", "--env", f.State.Env, "--node", extra.PublicIP, "--force"), notFound)
	infra.ExpectRefused(t, infra.Run(t, cli, "node", "wipe", "--env", f.State.Env, "--node", extra.PublicIP, "--force"), notFound)
	clean := infra.Run(t, cli, "node", "clean", "--env", f.State.Env, "--node", extra.PublicIP, "--force")
	infra.ExpectRefused(t, clean, "deprecated", notFound)
	infra.ExpectRefused(t, infra.Run(t, cli, "node", "remove", "--env", f.State.Env), "--node is required")
	infra.ExpectRefused(t, infra.Run(t, cli, "node", "remove", "--node", extra.PublicIP), "--env is required")
	infra.ExpectRefused(t, infra.Run(t, cli, "node", "wipe", "--node", extra.PublicIP), "--env is required")
}
