//go:build lifecycle

// Package lifecycle_test drives a real 3-node cluster through the orama CLI.
//
// Every scenario here corresponds to a finding from the change-287 stability
// audit that no existing test could see: e2e/cluster tests read-consistency
// levels and e2e/production stops a deployment process, so nothing in the repo
// reboots a node, kills a voter, joins one, or upgrades one. That is why the
// audit's findings all had to be established by reading code.
//
// Build tag `lifecycle`, so `go test ./...` never picks these up. Run with
// `make test-lifecycle`.
package lifecycle_test

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/lifecycle"
)

// One node reboots while the other two hold quorum.
//
// Covers: bug-310 (post-install/restart verification), chg-309 (the readiness
// gate), and the leftover-unit oscillation — a node that comes back with
// orama-olric and orama-namespace-olric@index both running shows up here as a
// crash-loop or a failed unit, which nothing else would have caught.
func TestReboot_oneNode_quorumIntact(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	target := c.AnyFollower()
	t.Logf("restarting %s", target)
	c.MustCLI("node", "restart", "--env", c.Env, "--node", target)

	c.WaitConverged(lifecycle.ConvergeBudget, "the cluster reconverged after one node restarted",
		func(r *lifecycle.Report) error {
			if err := r.Converged(len(c.Nodes)); err != nil {
				return err
			}
			return r.LeaderAgreement()
		})
}

// Every node restarts at once.
//
// Two assertions, in this order and for a reason: the public surfaces must come
// back long before raft does. A cluster still holding an election should be
// serving DNS and TLS — losing the zone because no leader has been chosen yet is
// a different and much worse failure than a slow election.
//
// Covers: bug-311 (fail-fast boot ordering) and the CoreDNS serve-stale work.
func TestReboot_allNodes_serveBeforeQuorum(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	for _, host := range c.Nodes {
		t.Logf("stopping %s", host)
		c.MustCLI("node", "stop", "--env", c.Env, "--node", host)
	}
	for _, host := range c.Nodes {
		t.Logf("starting %s", host)
		c.MustCLI("node", "start", "--env", c.Env, "--node", host)
	}

	c.WaitConverged(lifecycle.ServingBudget, "every node serves DNS and the gateway before quorum returns",
		func(r *lifecycle.Report) error { return r.Serving() })

	c.WaitConverged(lifecycle.ColdStartBudget, "the cluster reconverged from a cold start",
		func(r *lifecycle.Report) error {
			if err := r.Converged(len(c.Nodes)); err != nil {
				return err
			}
			return r.LeaderAgreement()
		})
}

// A voter is destroyed with no clean shutdown.
//
// The assertion that matters is Forgotten: a dead node is not gone because it
// stopped answering, it is gone when no surviving node still lists it as a
// voter or a WireGuard peer. A node evicted from raft but left in the mesh is
// the shape of failure that survives a restart and comes back as a phantom.
//
// Covers: the dead-voter eviction finding and "WG peers never persisted".
//
// Destroying the VM is infrastructure-specific, so this scenario needs a
// destroy hook rather than a CLI call — and it is the one place the harness
// reaches outside the CLI, because there is no `orama node destroy-abruptly`.
func TestKillVoter_survivorsKeepWritingAndForgetIt(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	victim := c.AnyFollower()
	victimWG := lifecycle.WireGuardIPOf(t, c, victim)

	lifecycle.DestroyNode(t, c, victim)

	// Quorum is 2 of 3, so the survivors must keep committing writes. Anything
	// less means the cluster stopped serving on a single node loss.
	c.WaitConverged(lifecycle.ConvergeBudget, "the survivors kept quorum",
		func(r *lifecycle.Report) error {
			if err := r.Converged(len(c.Nodes) - 1); err != nil {
				return err
			}
			return r.LeaderAgreement()
		})

	c.WaitConverged(lifecycle.ConvergeBudget, "every membership view forgot the dead node",
		func(r *lifecycle.Report) error { return r.Forgotten(victimWG) })
}

// A rolling upgrade with one node forced to fail.
//
// The property under test is that the rollout STOPS: the remaining voters must
// be untouched and the cluster must still be serving. Before chg-309 the gate
// printed "Cluster health check warning ... Continuing" and restarted the next
// voter regardless, which is how a rolling upgrade takes out a quorum.
func TestRollingUpgrade_haltsOnAFailingNode(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	before, err := c.Report()
	if err != nil {
		t.Fatal(err)
	}
	leader := before.Summary.RQLiteLeader

	victim := c.AnyFollower()
	lifecycle.BreakNode(t, c, victim)

	out, err := c.CLI("node", "upgrade", "--env", c.Env, "--yes")
	if err == nil {
		t.Fatalf("the rollout completed with a broken node in it:\n%s", out)
	}
	if !strings.Contains(out, victim) {
		t.Errorf("the rollout failed but did not name the node that failed:\n%s", out)
	}
	if !strings.Contains(out, "Stopping rollout") {
		t.Errorf("the rollout failed without saying it stopped:\n%s", out)
	}

	// The leader must not have been restarted: it is last in the plan, and the
	// rollout stopped before reaching it.
	after, err := c.Report()
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.RQLiteLeader != leader {
		t.Errorf("leadership moved to %s during a rollout that should have stopped at %s",
			after.Summary.RQLiteLeader, victim)
	}
	if err := after.LeaderAgreement(); err != nil {
		t.Errorf("the cluster lost agreement during a halted rollout: %v", err)
	}
}

// A successful rolling upgrade must upgrade the leader last.
//
// docs/DEV_DEPLOY.md claimed this for a long time while nothing implemented it.
func TestRollingUpgrade_upgradesTheLeaderLast(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	leader := c.Leader()

	// The plan is printed without --yes and nothing is restarted, so this
	// assertion costs nothing and cannot itself disturb the cluster.
	plan, err := c.CLI("node", "upgrade", "--env", c.Env)
	if err == nil {
		t.Fatal("the upgrade ran without --yes; the plan must require confirmation")
	}
	if !strings.Contains(plan, leader) {
		t.Fatalf("the plan does not mention the leader %s:\n%s", leader, plan)
	}

	lines := planLines(plan)
	if len(lines) == 0 {
		t.Fatalf("no plan steps found in:\n%s", plan)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, leader) {
		t.Errorf("the leader %s is not the last step; plan was:\n%s", leader, plan)
	}

	c.MustCLI("node", "upgrade", "--env", c.Env, "--yes")
	c.WaitConverged(lifecycle.ConvergeBudget, "the cluster reconverged after the rollout",
		func(r *lifecycle.Report) error {
			if err := r.Converged(len(c.Nodes)); err != nil {
				return err
			}
			return r.LeaderAgreement()
		})
}

// A fourth node joins.
//
// Covers the ghost-row findings: an install aborted part-way must leave nothing
// behind, and a completed one must be a full member of every membership store,
// not just raft.
func TestJoin_fourthNode_becomesAFullMember(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	before := len(c.Nodes)
	host := lifecycle.ProvisionNode(t, c)

	c.WaitConverged(lifecycle.ConvergeBudget, "the new node is a full member",
		func(r *lifecycle.Report) error {
			if err := r.Converged(before + 1); err != nil {
				return err
			}
			if err := r.LeaderAgreement(); err != nil {
				return err
			}
			for _, n := range r.Nodes {
				if n.Host == host {
					return nil
				}
			}
			return errNotInReport(host)
		})
}

// Decommissioning a node must remove it from every membership store, the same
// way an abrupt death does — the difference should be that it is clean, not
// that it is more thorough.
func TestDecommission_removesTheNodeEverywhere(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	victim := c.AnyFollower()
	victimWG := lifecycle.WireGuardIPOf(t, c, victim)

	c.MustCLI("node", "decommission", "--env", c.Env, "--node", victim, "--yes")

	c.WaitConverged(lifecycle.ConvergeBudget, "the decommissioned node is gone from every view",
		func(r *lifecycle.Report) error {
			if err := r.Converged(len(c.Nodes) - 1); err != nil {
				return err
			}
			return r.Forgotten(victimWG)
		})
}

// The index rqlite goes down entirely; DNS must keep answering from the stale
// cache.
//
// Before the serve-stale work every backend error became SERVFAIL for the whole
// zone, so an index rqlite with no leader took every name in the fleet offline —
// including the names an operator needs to reach the machines and fix it.
func TestIndexRQLiteDown_dnsServesStale(t *testing.T) {
	c := lifecycle.New(t)
	c.RequireHealthy()

	name := lifecycle.SomeResolvableName(t, c)
	if err := lifecycle.Resolves(name); err != nil {
		t.Fatalf("precondition: %s does not resolve: %v", name, err)
	}

	restore := lifecycle.StopIndexRQLiteEverywhere(t, c)
	defer restore()

	// Immediately, not after a wait: a stale answer that takes a minute to
	// arrive is a resolver timeout.
	if err := lifecycle.Resolves(name); err != nil {
		t.Fatalf("DNS stopped answering with the index rqlite down: %v", err)
	}
}

// planLines extracts the numbered steps from a printed rollout plan.
func planLines(plan string) []string {
	var steps []string
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
			steps = append(steps, trimmed)
		}
	}
	return steps
}

func errNotInReport(host string) error {
	return &notInReportError{host: host}
}

type notInReportError struct{ host string }

func (e *notInReportError) Error() string {
	return e.host + " is not in the monitor report yet"
}

var _ = time.Second
