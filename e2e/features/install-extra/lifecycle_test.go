//go:build e2e_fleet

package installextra

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// operatorRule is a firewall rule the operator added with a comment of their
// own: `orama node remove` wipes Orama's tagged rules and must leave it.
const (
	operatorPort    = "12345/tcp"
	operatorComment = "e2e-operator"
)

// TestExtraNode_setupJoinRemoveWipe drives one fresh server through its
// whole life as an operator does: setup refuses an unpinned or wrong host
// key before any credential is used; setup joins it as a full member; a
// second setup changes nothing; remove prints its plan, declines without a
// confirmation, then retires the node from every membership view and wipes
// it, leaving the operator's own firewall rule; and a removed node is not a
// target any more.
func TestExtraNode_setupJoinRemoveWipe(t *testing.T) {
	f := harness.Fleet(t)
	extra := infra.NewExtra(t, "extra-install")
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"wrongHostKeyRefused", func(t *testing.T) { wrongHostKeyRefused(t, f, extra) }},
		{"unconfirmedHostKeyRefused", func(t *testing.T) { unconfirmedHostKeyRefused(t, f, extra) }},
		{"joins", func(t *testing.T) { infra.JoinExtra(t, extra) }},
		{"fullMember", func(t *testing.T) { fullMember(t, f, extra) }},
		{"installDryRunChangesNothing", func(t *testing.T) { installDryRunChangesNothing(t, f, extra) }},
		{"installFlagsRefused", func(t *testing.T) { installFlagsRefused(t, f, extra) }},
		{"unsignedArchiveRefused", func(t *testing.T) { unsignedArchiveRefused(t, f, extra) }},
		{"untrustedSignerRefused", func(t *testing.T) { untrustedSignerRefused(t, f, extra) }},
		{"tamperedFileRefused", func(t *testing.T) { tamperedFileRefused(t, f, extra) }},
		{"extraFileRefused", func(t *testing.T) { extraFileRefused(t, f, extra) }},
		{"trustSignersNeverChangesAnchor", func(t *testing.T) { trustSignersNeverChangesAnchor(t, f, extra) }},
		{"setupAgainChangesNothing", func(t *testing.T) { setupAgainChangesNothing(t, f, extra) }},
		{"removeDryRunChangesNothing", func(t *testing.T) { removeDryRunChangesNothing(t, f, extra) }},
		{"oramaRemoveDryRunChangesNothing", func(t *testing.T) { oramaRemoveDryRunChangesNothing(t, f, extra) }},
		{"oramaRemoveRefusesBadFlags", func(t *testing.T) { oramaRemoveRefusesBadFlags(t, f, extra) }},
		{"removeWithoutConfirmationAborts", func(t *testing.T) { removeWithoutConfirmationAborts(t, f, extra) }},
		{"oramaRemoveWithoutConfirmationAborts", func(t *testing.T) { oramaRemoveWithoutConfirmationAborts(t, f, extra) }},
		{"removeRetiresAndWipes", func(t *testing.T) { removeRetiresAndWipes(t, f, extra) }},
		{"removedNodeIsNoTarget", func(t *testing.T) { removedNodeIsNoTarget(t, f, extra) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			t.Fatalf("step %s failed; the later steps depend on it", s.name)
		}
	}
}

func authorizedKeys(t testing.TB, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	return f.MustExec(t, n, "sha256sum /root/.ssh/authorized_keys").Stdout
}

// wrongHostKeyRefused: a --host-key that is not the server's is refused
// before the bootstrap key is used on it (docs/CLI_REFERENCE.md "orama node
// setup" --host-key; production/setup/hostkey.go).
func wrongHostKeyRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	other, err := fleet.HostKeyFingerprint(f.State, f.State.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	before := authorizedKeys(t, f, extra.Node)
	args := infra.SetupArgs(t, extra, infra.RunningArchive(t, f))
	for i, a := range args {
		if a == "--host-key" {
			args[i+1] = other
		}
	}
	res := infra.RunFor(t, harness.CLI(t), infra.InstallBudget, args...)
	infra.ExpectRefused(t, res, "host key fingerprint mismatch", "refusing to use your credential")
	if after := authorizedKeys(t, f, extra.Node); after != before {
		t.Fatal("setup changed authorized_keys on a server whose host key did not match")
	}
	if f.Exec(t, extra.Node, "test -e /opt/orama").Exit == 0 {
		t.Fatal("setup put /opt/orama on a server whose host key did not match")
	}
}

// unconfirmedHostKeyRefused: without --host-key and without a terminal to
// confirm on, setup refuses rather than trusting on first use.
func unconfirmedHostKeyRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	before := authorizedKeys(t, f, extra.Node)
	args := infra.SetupArgs(t, extra, infra.RunningArchive(t, f))
	var kept []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--host-key" {
			i++
			continue
		}
		kept = append(kept, args[i])
	}
	res := infra.RunFor(t, harness.CLI(t), infra.InstallBudget, kept...)
	infra.ExpectRefused(t, res, "host key not confirmed")
	if after := authorizedKeys(t, f, extra.Node); after != before {
		t.Fatal("an unconfirmed setup changed authorized_keys")
	}
}

// fullMember: the joined node is in every membership view: the operator's
// node list, the report with a mesh address of its own, every core node's
// WireGuard peers, and it trusts the cluster's archive signers.
func fullMember(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	r := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	wg, err := infra.WGIPOf(r, extra.PublicIP)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range r.Nodes {
		if n.Host != extra.PublicIP && n.Report != nil && n.Report.WGIP == wg {
			t.Fatalf("%s shares the new node's WireGuard address %s", n.Host, wg)
		}
	}
	if err := r.Forgotten(wg); err == nil {
		t.Fatalf("no view lists %s: it is not a member", wg)
	}
	nodes := harness.CLI(t).MustOK(t, "nodes", "--env", f.State.Env).Stdout
	if !strings.Contains(nodes, extra.PublicIP) {
		t.Errorf("orama nodes does not list %s:\n%s", extra.PublicIP, nodes)
	}
	signers := strings.Fields(string(f.ReadFile(t, extra.Node, infra.ArchiveSigners)))
	if len(signers) != 1 || signers[0] != strings.ToLower(f.State.OperatorAddress) {
		t.Errorf("the joined node trusts %v, want the cluster's [%s]", signers, strings.ToLower(f.State.OperatorAddress))
	}
	infra.RequireStat(t, f, extra.Node, infra.WireGuardConfPath, "root", "root", "600")
	infra.RequireStat(t, f, extra.Node, infra.OramaBinDir, "root", "orama", "750")
}

// setupAgainChangesNothing: running the same setup on a member does not
// disturb it: whatever it answers, the node keeps its mesh address and raft
// id and the cluster stays converged (docs/CLI_REFERENCE.md "orama node
// setup": a node already running this exact build is not re-uploaded).
func setupAgainChangesNothing(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	before := monitor.Fetch(t, harness.CLI(t), f.State.Env)
	entry, err := infra.ReportFor(before, extra.Node)
	if err != nil {
		t.Fatal(err)
	}
	res := infra.RunFor(t, harness.CLI(t), infra.InstallBudget, infra.SetupArgs(t, extra, infra.RunningArchive(t, f))...)
	if !strings.Contains(res.Stdout, "already runs this build") {
		t.Errorf("a second setup re-uploaded the build it already runs:\n%s", res.Stdout)
	}
	after := infra.WaitConverged(t, len(f.State.Nodes)+1, infra.ConvergeBudget, "the cluster after a second setup")
	again, err := infra.ReportFor(after, extra.Node)
	if err != nil {
		t.Fatal(err)
	}
	if again.Report.WGIP != entry.Report.WGIP || again.Report.RQLite.NodeID != entry.Report.RQLite.NodeID {
		t.Fatalf("a second setup changed the node's identity: wg %s -> %s, raft %s -> %s",
			entry.Report.WGIP, again.Report.WGIP, entry.Report.RQLite.NodeID, again.Report.RQLite.NodeID)
	}
}
