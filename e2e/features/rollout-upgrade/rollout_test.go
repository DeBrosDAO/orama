//go:build e2e_fleet

package rolloutupgrade

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
)

// Rollout output (core/cmd/orama/internal/production/upgrade/remote.go,
// core/pkg/rollout/plan.go).
const (
	planHeader   = "Rolling upgrade plan"
	stoppingText = "Stopping rollout"
	completeText = "Rolling upgrade complete"
	needsYes     = "re-run with --yes"
)

// planSteps are the numbered lines of a printed plan ("  1. <host> ...").
func planSteps(out string) []string {
	var steps []string
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if len(l) > 2 && l[0] >= '1' && l[0] <= '9' && l[1] == '.' {
			steps = append(steps, l)
		}
	}
	return steps
}

// starts records every core node's orama-node start time: a node the
// rollout did not touch keeps it.
func starts(t testing.TB, f *fleet.Fleet) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range f.State.Nodes {
		out[n.Name] = strings.TrimSpace(f.MustExec(t, n, "systemctl show -p ActiveEnterTimestampMonotonic --value "+infra.NodeUnit).Stdout)
	}
	return out
}

// TestRollout_planPutsTheLeaderLastAndNeedsYes: without --yes the rolling
// upgrade prints its plan, one numbered step per node with the raft leader
// last, restarts nothing and refuses to go on (docs/CLI_REFERENCE.md "orama
// node upgrade" --yes; core/e2e/lifecycle TestRollingUpgrade_upgradesTheLeaderLast).
func TestRollout_planPutsTheLeaderLastAndNeedsYes(t *testing.T) {
	f := harness.Fleet(t)
	leader := infra.Leader(t, infra.RequireHealthy(t))
	before := starts(t, f)
	res := infra.Run(t, harness.CLI(t), "node", "upgrade", "--env", f.State.Env)
	infra.ExpectRefused(t, res, planHeader, needsYes)
	steps := planSteps(res.Stdout)
	if len(steps) != len(f.State.Nodes) {
		t.Fatalf("the plan has %d steps, want %d:\n%s", len(steps), len(f.State.Nodes), res.Stdout)
	}
	if !strings.Contains(steps[len(steps)-1], leader.PublicIP) {
		t.Errorf("the leader %s is not the last step:\n%s", leader.PublicIP, res.Stdout)
	}
	if after := starts(t, f); !equal(before, after) {
		t.Errorf("printing the plan restarted a node: %v -> %v", before, after)
	}
	infra.ExpectRefused(t, infra.Run(t, harness.CLI(t), "node", "upgrade", "--env", f.State.Env, "--node", "192.0.2.1", "--yes"),
		"192.0.2.1", "not found")
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestRollout_haltsOnABrokenNodeThenResumes: a node whose upgrade fails stops
// the rollout: the output names it and says it stopped, the nodes after it
// are untouched, the leader keeps the leadership and the cluster stays
// converged. Once the node is fixed, the same command completes the rollout
// (core/e2e/lifecycle TestRollingUpgrade_haltsOnAFailingNode; docs/DEV_DEPLOY.md
// rolling upgrades).
func TestRollout_haltsOnABrokenNodeThenResumes(t *testing.T) {
	f := harness.Fleet(t)
	if f.State.IsStagenet() {
		harness.SkipNotApplicable(t, "the stagenet target cannot break a node's staged signature (provision.BreakUpgrade): the existing cluster is only tested, never changed")
	}
	r := infra.RequireHealthy(t)
	leader := infra.Leader(t, r)
	plan := infra.Run(t, harness.CLI(t), "node", "upgrade", "--env", f.State.Env)
	steps := planSteps(plan.Stdout)
	if len(steps) == 0 {
		t.Fatalf("no plan:\n%s", plan.Stdout)
	}
	victim := firstStepNode(t, f, steps[0])
	before := starts(t, f)
	resumed := false
	// Registered before the break, so it runs after the break's own restore.
	t.Cleanup(func() {
		if !resumed {
			finishRollout(t, f)
		}
	})
	breakUpgrade(t, f, victim)
	res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "node", "upgrade", "--env", f.State.Env, "--yes")
	infra.ExpectRefused(t, res, victim.PublicIP, stoppingText)
	after := starts(t, f)
	for _, n := range f.State.Nodes {
		if n.Name != victim.Name && after[n.Name] != before[n.Name] {
			t.Errorf("the halted rollout restarted %s, which comes after the failed node", n.Name)
		}
	}
	rep := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after a halted rollout")
	if infra.Leader(t, rep).Name != leader.Name {
		t.Errorf("leadership moved from %s during a rollout that stopped at %s", leader.Name, victim.Name)
	}
	restoreUpgrade(t, f, victim)
	done := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "node", "upgrade", "--env", f.State.Env, "--yes")
	infra.ExpectExit(t, done, infra.ExitOK, completeText)
	resumed = true
	gatewaysServeNow(t, f)
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after the resumed rollout")
}

// edgeBudget is how long a node's public edge (Caddy, which the supervisor
// starts after the gateway) has, once the rollout reports the node done, to
// answer through the public name. The upgrade's gate waits for the node's
// database and gateway, not the edge; a node whose gateway the upgrade
// restarted after that gate answered nothing for its whole restart.
const (
	edgeBudget = 30 * time.Second
	edgeEvery  = time.Second
)

// gatewaysServeNow requires each node to answer its health check through its
// public edge within edgeBudget of the rollout completing (docs/ARCHITECTURE.md
// "A removed namespace is removed, not stopped").
func gatewaysServeNow(t testing.TB, f *fleet.Fleet) {
	t.Helper()
	for _, n := range f.State.Nodes {
		c := harness.GW(t).PinTo(n.PublicIP)
		eventually.Require(t, edgeEvery, edgeBudget, n.Name+" to serve right after the rollout", func() (bool, error) {
			resp, err := c.Send(t.Context(), gw.Req{Path: "/v1/health"})
			if err != nil {
				return false, err
			}
			if resp.Status != http.StatusOK {
				return false, fmt.Errorf("health answered %d", resp.Status)
			}
			return true, nil
		})
	}
}

func firstStepNode(t testing.TB, f *fleet.Fleet, step string) fleet.Node {
	t.Helper()
	for _, n := range f.State.Nodes {
		if strings.Contains(step, n.PublicIP+" ") {
			return n
		}
	}
	t.Fatalf("plan step %q names no core node", step)
	return fleet.Node{}
}

// breakUpgrade makes the node's next upgrade fail (the staged signature
// cannot verify) and registers the restore, which a resumed rollout needs
// first anyway.
func breakUpgrade(t testing.TB, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
		defer cancel()
		if err := provision.RestoreUpgrade(ctx, f.State, n.PublicIP); err != nil && !strings.Contains(err.Error(), "nothing to restore") {
			t.Errorf("cleanup: restore the upgrade of %s: %v", n.Name, err)
		}
	})
	if err := provision.BreakUpgrade(t.Context(), f.State, n.PublicIP); err != nil {
		t.Fatalf("break the upgrade of %s: %v", n.Name, err)
	}
}

// finishRollout completes a rollout the test left half done (its resume
// failed or never ran), from a cleanup: every node on the same build and the
// cluster converged before the next test.
func finishRollout(t testing.TB, f *fleet.Fleet) {
	ctx, cancel := context.WithTimeout(context.Background(), infra.UpgradeBudget)
	defer cancel()
	res, err := harness.CLI(t).For(t).Run(ctx, "node", "upgrade", "--env", f.State.Env, "--yes")
	if err != nil || res.Exit != infra.ExitOK {
		t.Errorf("cleanup: completing the halted rollout failed (exit %d): %v\n%s", res.Exit, err, f.Redact(res.Stdout+res.Stderr))
	}
	infra.ConvergeInCleanup(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after completing the halted rollout")
}

func restoreUpgrade(t testing.TB, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	if err := provision.RestoreUpgrade(t.Context(), f.State, n.PublicIP); err != nil {
		t.Fatalf("restore the upgrade of %s: %v", n.Name, err)
	}
}
