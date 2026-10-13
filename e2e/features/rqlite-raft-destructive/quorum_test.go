//go:build e2e_fleet

package rqliteraftdestructive

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// electionBudget: rqlite's election timeout is 5s (core/pkg/rqlite
// instance_spawner.go); the monitor sees the new leader within a telemetry
// round or two.
const electionBudget = 3 * time.Minute

// TestQuorum_stopRefusedWhenItWouldBreakQuorum: with as many voters down as
// the cluster can spare, stopping one more is refused as a conflict that names
// the arithmetic and the way to force it, and removing it is refused before
// anything changes (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node stop", "orama node
// remove"). A cluster of n voters can spare n - (n/2+1): one of three, two of
// five.
func TestQuorum_stopRefusedWhenItWouldBreakQuorum(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	followers := infra.Followers(t, r)
	voters := voterCount(r)
	spare := voters - (voters/2 + 1)
	if spare < 1 || len(followers) < spare+1 {
		t.Fatalf("%d voters and %d followers: no voter can be spared and then one more refused", voters, len(followers))
	}
	s := newStopped(t)
	for _, fo := range followers[:spare] {
		if out := s.stop(t, fo, false); out.Exit != 0 {
			t.Fatalf("stopping %s, one of the %d voters a cluster of %d can spare, was refused (exit %d):\n%s",
				fo.Name, spare, voters, out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
	}
	second := followers[spare]
	// Through s.stop, so a stop that regresses into going through is
	// started again by the cleanup.
	out := s.stop(t, second, false)
	if out.Exit != infra.ExitConflict || !strings.Contains(out.Stdout+out.Stderr, "would break RQLite quorum") ||
		!strings.Contains(out.Stdout+out.Stderr, "--force") {
		t.Errorf("stopping voter %d of %d with %d down: exit %d, want %d naming the quorum and --force:\n%s",
			spare+1, voters, spare, out.Exit, infra.ExitConflict, f.Redact(out.Stdout+out.Stderr))
	}
	if st := f.Unit(t, second, infra.IndexRQLiteUnit); st != "active" {
		t.Errorf("a refused stop left %s %s", infra.IndexRQLiteUnit, st)
	}
	rm := infra.Run(t, harness.CLI(t), "node", "remove", "--env", f.State.Env, "--node", second.PublicIP, "--dry-run")
	infra.ExpectRefused(t, rm, "would cost a cluster its quorum")
	if rm.Exit != infra.ExitConflict {
		t.Errorf("a quorum refusal of remove exited %d, want %d (conflict: the cluster refused, retrying unchanged is refused again)",
			rm.Exit, infra.ExitConflict)
	}
	// orama remove is the same removal with the chain added: the quorum
	// arithmetic comes first and refuses the same way, before anything is asked
	// of the chain or the RootWallet.
	rm = infra.Run(t, harness.CLI(t), "remove", "--env", f.State.Env, "--node", second.PublicIP, "--no-chain", "--dry-run")
	infra.ExpectRefused(t, rm, "would cost a cluster its quorum")
	if rm.Exit != infra.ExitConflict {
		t.Errorf("a quorum refusal of orama remove exited %d, want %d", rm.Exit, infra.ExitConflict)
	}
}

// TestLeaderLoss_survivorsElectAndServe: stopping the leader hands raft to a
// survivor: every responsive node agrees on a new leader that is not the old
// one, and when the old leader starts again it rejoins without splitting the
// cluster (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama node stop": leadership transfers).
func TestLeaderLoss_survivorsElectAndServe(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	old := infra.Leader(t, r)
	s := newStopped(t)
	if out := s.stop(t, old, false); out.Exit != 0 {
		t.Fatalf("stopping the leader was refused (exit %d):\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	eventually.Require(t, infra.PollEvery, electionBudget, "a new leader among the survivors", func() (bool, error) {
		rep, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		if !rep.HasLeader() || rep.Summary.RQLiteLeader == old.PublicIP {
			return false, fmt.Errorf("leader is %q", rep.Summary.RQLiteLeader)
		}
		return true, rep.LeaderAgreement()
	})
	for _, n := range f.State.Nodes {
		if n.Name == old.Name {
			continue
		}
		if q, err := infra.IndexQueryAt(t, f, n, "strong", "SELECT 1"); err != nil || len(q.Values) != 1 {
			t.Errorf("%s cannot read at strong consistency with the old leader down: %v", n.Name, err)
		}
	}
}

// TestQuorumLoss_refusesStrongReadsThenRecovers: with every follower forced
// down the surviving leader cannot serve a strong read (no quorum) but still
// answers a local read, and bringing the voters back (the cleanup) restores a
// converged cluster with the same members.
func TestQuorumLoss_refusesStrongReadsThenRecovers(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	leader := infra.Leader(t, r)
	s := newStopped(t)
	for _, n := range infra.Followers(t, r) {
		if out := s.stop(t, n, true); out.Exit != 0 {
			t.Fatalf("orama node stop --force on %s: exit %d", n.Name, out.Exit)
		}
	}
	eventually.Require(t, infra.PollEvery, electionBudget, "the survivor to lose quorum", func() (bool, error) {
		_, err := infra.IndexQueryAt(t, f, leader, "strong", "SELECT 1")
		if err == nil {
			return false, fmt.Errorf("%s still answers strong reads", leader.Name)
		}
		return true, nil
	})
	if q, err := infra.IndexQueryAt(t, f, leader, "none", "SELECT COUNT(*) FROM node_credentials"); err != nil || len(q.Values) != 1 {
		t.Errorf("a local read (level none) on the survivor failed: %v", err)
	}
}

// voterCount is how many nodes report themselves an rqlite voter.
func voterCount(r *monitor.Report) int {
	n := 0
	for _, e := range r.Nodes {
		if e.Report != nil && e.Report.RQLite != nil && e.Report.RQLite.Voter {
			n++
		}
	}
	return n
}
