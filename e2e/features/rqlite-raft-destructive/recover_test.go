//go:build e2e_fleet

package rqliteraftdestructive

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// TestRecoverRaft_refusalsChangeNothing: recover-raft refuses a missing
// --env, a node that is not in the environment and a malformed raft address,
// and without --force it prints its plan and declines with the aborted exit
// code, leaving the cluster as it was (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node
// recover-raft"; clierr CodeAborted).
func TestRecoverRaft_refusalsChangeNothing(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	leader := infra.Leader(t, r)
	cli := harness.CLI(t)
	infra.ExpectRefused(t, infra.Run(t, cli, "maint", "node", "recover-raft"), "--env is required")
	// No --force on the refusals: should the validation regress, the empty
	// stdin declines at the prompt instead of running a recovery.
	infra.ExpectRefused(t, infra.Run(t, cli, "maint", "node", "recover-raft", "--env", f.State.Env, "--leader", "192.0.2.1"), "192.0.2.1")
	infra.ExpectRefused(t, infra.Run(t, cli, "maint", "node", "recover-raft", "--env", f.State.Env, "--leader", leader.PublicIP,
		"--leader-raft-addr", "not-an-address"), "--leader-raft-addr")
	res := infra.Run(t, cli, "maint", "node", "recover-raft", "--env", f.State.Env, "--leader", leader.PublicIP)
	if !strings.Contains(res.Stdout, "Aborted.") || !strings.Contains(res.Stdout, "DATA PRESERVED") {
		t.Errorf("an unconfirmed recover-raft did not print its plan and abort:\n%s", res.Stdout)
	}
	after := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after refused recoveries")
	if infra.Leader(t, after).Name != leader.Name {
		t.Errorf("a refused recovery moved the leadership from %s", leader.Name)
	}
	if res.Exit != infra.ExitAborted {
		t.Errorf("a declined recover-raft exited %d, want %d (aborted): a script cannot tell it from a recovery", res.Exit, infra.ExitAborted)
	}
}

// TestRecoverRaft_afterQuorumLossKeepsTheLeadersData: with quorum lost
// (both followers down) the operator reforms the cluster around the
// surviving leader with --leader-raft-addr: the followers are wiped and
// re-sync from it, every node keeps its raft id, all of them converge again,
// and data written before the loss (an invite, a namespace) is still there
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama maint node recover-raft": use --leader-raft-addr
// when quorum is already lost).
func TestRecoverRaft_afterQuorumLossKeepsTheLeadersData(t *testing.T) {
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	r := raiseTerm(t, f, termElections)
	leader := infra.Leader(t, r)
	leaderEntry, err := infra.ReportFor(r, leader)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range r.Nodes {
		ids[e.Host] = e.Report.RQLite.NodeID
	}
	termBefore := leaderEntry.Report.RQLite.Term
	token := mintInvite(t, leader)
	loseQuorum(t, r)
	addr := fmt.Sprintf("%s:%d", leaderEntry.Report.WGIP, infra.IndexRQLiteRaft)
	res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "maint", "node", "recover-raft", "--env", f.State.Env,
		"--leader", leader.PublicIP, "--leader-raft-addr", addr, "--force")
	infra.ExpectExit(t, res, infra.ExitOK)
	after := infra.WaitConverged(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster reformed around "+leader.Name)
	for _, e := range after.Nodes {
		if ids[e.Host] != e.Report.RQLite.NodeID {
			t.Errorf("%s changed raft id %s -> %s in the recovery", e.Host, ids[e.Host], e.Report.RQLite.NodeID)
		}
		// The recovery keeps the leader's raft term: a cluster restarted below
		// the term of its own recovery snapshot serves that snapshot as the
		// newest forever, and a lagging node never catches up
		// (website/src/docs/operator/troubleshooting.mdx #21).
		if rq := e.Report.RQLite; rq.LastSnapshotTerm > rq.Term {
			t.Errorf("%s holds a snapshot of term %d above its raft term %d after the recovery", e.Host, rq.LastSnapshotTerm, rq.Term)
		}
		if e.Report.RQLite.Term < termBefore {
			t.Errorf("%s is at raft term %d after the recovery, below the leader's %d before it: the recovery reset the term",
				e.Host, e.Report.RQLite.Term, termBefore)
		}
	}
	for _, node := range f.State.Nodes {
		if row := infra.ReadInvite(t, f, node, token); !row.Found {
			t.Errorf("%s lost the invite written before the recovery", node.Name)
		}
	}
	requireServes(t, n)
}

// mintInvite writes an invite through the leader and returns its token: data
// the recovery must keep.
func mintInvite(t testing.TB, leader fleet.Node) string {
	t.Helper()
	var m struct {
		Invite string `json:"invite"`
	}
	f := harness.Fleet(t)
	if err := oramacli.DecodeJSON(harness.CLI(t).MustOK(t, "maint", "invite", "--env", f.State.Env, "--node", leader.PublicIP, "--json"), &m); err != nil {
		t.Fatal(err)
	}
	return infra.DecodeInvite(t, m.Invite).Token
}

// loseQuorum stops orama-node on both followers (the index units go with it,
// PartOf) with a plain systemctl stop: `orama node stop` masks the units, and
// recover-raft starts them with a plain `systemctl start` (recover.go
// phase3StartLeader, phase4StartFollowers), which a masked unit refuses. The
// test goes no further unless both really stopped. The cleanups start them
// again should the recovery not, and wait for the cluster to converge.
func loseQuorum(t testing.TB, r *monitor.Report) {
	t.Helper()
	f := harness.Fleet(t)
	t.Cleanup(func() {
		infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ColdStartBudget, "the cluster after the recovery test")
	})
	for _, fo := range infra.Followers(t, r) {
		f.StopService(t, fo, infra.NodeUnit)
		if st := f.Unit(t, fo, infra.IndexRQLiteUnit); st == unitActive {
			t.Fatalf("%s still runs %s after stopping %s: quorum is not lost, refusing to force a recovery", fo.Name, infra.IndexRQLiteUnit, infra.NodeUnit)
		}
	}
}

// termElections is how many elections raiseTerm forces before the recovery:
// enough that a recovery restarting the cluster at term 1 lands below the term
// of its own recovery snapshot, which the test then sees.
const termElections = 2

// raiseTerm kills the leader's index rqlite elections times, waiting for the
// cluster to converge after each, and returns the healthy report after the
// last: each kill is an election, so the term ends at least elections above
// where it started.
func raiseTerm(t testing.TB, f *fleet.Fleet, elections int) *monitor.Report {
	t.Helper()
	r := infra.RequireHealthy(t)
	start := maxTerm(r)
	for i := 0; i < elections; i++ {
		f.Kill(t, infra.Leader(t, r), infra.IndexRQLiteUnit)
		r = infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after election "+strconv.Itoa(i+1))
	}
	if end := maxTerm(r); end < start+uint64(elections) {
		t.Fatalf("the raft term went from %d to %d over %d forced elections", start, end, elections)
	}
	return r
}

// maxTerm is the highest raft term any node reports: a follower can briefly
// trail the leader by one.
func maxTerm(r *monitor.Report) uint64 {
	var hi uint64
	for _, e := range r.Nodes {
		if e.Report != nil && e.Report.RQLite != nil && e.Report.RQLite.Term > hi {
			hi = e.Report.RQLite.Term
		}
	}
	return hi
}

// requireServes waits until n answers through its own gateway.
func requireServes(t testing.TB, n *ns.Namespace) {
	t.Helper()
	eventually.Require(t, infra.PollEvery, infra.ConvergeBudget, "the namespace to serve after the recovery", func() (bool, error) {
		resp, err := n.Client.Send(t.Context(), gw.Req{Path: "/v1/health"})
		if err != nil {
			return false, err
		}
		if resp.Status != http.StatusOK {
			return false, fmt.Errorf("namespace health HTTP %d", resp.Status)
		}
		return true, nil
	})
	if err := monitor.Fetch(t, harness.CLI(t), harness.Fleet(t).State.Env).LeaderAgreement(); err != nil {
		t.Error(err)
	}
}
