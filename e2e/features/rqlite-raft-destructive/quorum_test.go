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

// TestQuorum_stopRefusedWhenItWouldBreakQuorum: with one voter down, stopping
// a second is refused as a conflict that names the arithmetic and the way to
// force it, and removing a second is refused before anything changes
// (docs/CLI_REFERENCE.md "orama node stop", "orama node remove").
func TestQuorum_stopRefusedWhenItWouldBreakQuorum(t *testing.T) {
	f := harness.Fleet(t)
	r := infra.RequireHealthy(t)
	followers := infra.Followers(t, r)
	s := newStopped(t)
	first, second := followers[0], followers[1]
	if out := s.stop(t, first, false); out.Exit != 0 {
		t.Fatalf("stopping one voter of three was refused (exit %d):\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
	}
	// Through s.stop, so a stop that regresses into going through is
	// started again by the cleanup.
	out := s.stop(t, second, false)
	if out.Exit != infra.ExitConflict || !strings.Contains(out.Stdout+out.Stderr, "would break RQLite quorum") ||
		!strings.Contains(out.Stdout+out.Stderr, "--force") {
		t.Errorf("stopping a second voter: exit %d, want %d naming the quorum and --force:\n%s", out.Exit, infra.ExitConflict, out.Stdout+out.Stderr)
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
}

// TestLeaderLoss_survivorsElectAndServe: stopping the leader hands raft to a
// survivor: every responsive node agrees on a new leader that is not the old
// one, and when the old leader starts again it rejoins without splitting the
// cluster (docs/CLI_REFERENCE.md "orama node stop": leadership transfers).
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

// TestQuorumLoss_refusesStrongReadsThenRecovers: with two of three voters
// forced down the survivor cannot serve a strong read (no quorum) but still
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
