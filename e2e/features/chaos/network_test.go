//go:build e2e_fleet

package chaos

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	rowsPerPhase = 5
	// Overlay degradation: a bad link, not a dead one.
	degradeDelayMS = 200
	degradeLossPct = 10
)

// TestChaos_symmetricPartitionHeals: a follower cut off from both other
// nodes in both directions. The two others keep quorum, keep serving and
// keep accepting writes; when the partition lifts the node catches up on
// every row written without it and the cluster converges with one leader
// (website/src/docs/contributor/architecture-reference.mdx; rqlite needs a majority of voters).
func TestChaos_symmetricPartitionHeals(t *testing.T) {
	f := harness.Fleet(t)
	realistic.RequireFaultBudget(t, "the symmetric partition", faultWorst)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	survivors := others(f, victim)
	s := newStore(t)
	s.writeN(t, victim, "before", rowsPerPhase)
	t.Run("partitioned", func(t *testing.T) {
		for _, o := range survivors {
			f.IPTablesBlock(t, victim, o)
		}
		requireServing(t, survivors, "with "+victim.Name+" partitioned")
		s.writeN(t, survivors[0], "during", rowsPerPhase)
	})
	healed(t, "the cluster after "+victim.Name+"'s partition lifted")
	s.requireRows(t, victim, "during", rowsPerPhase)
	s.requireRows(t, survivors[0], "before", rowsPerPhase)
}

// TestChaos_asymmetricPartitionHeals: one direction of a link fails. First
// the follower hears nothing from the leader while the leader still hears
// it (the follower times out and campaigns); then the leader goes deaf to the
// follower. In both the cluster keeps serving through the other nodes and
// accepting writes, and when the link is whole again it converges on one
// leader everybody agrees on, with no row lost.
func TestChaos_asymmetricPartitionHeals(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	s := newStore(t)
	// Each case picks its nodes from a fresh report: the first may move the
	// leadership.
	cases := []struct {
		name string
		pick func(leader, follower fleet.Node) (deaf, unheard fleet.Node)
	}{
		{"follower deaf to leader", func(l, fo fleet.Node) (fleet.Node, fleet.Node) { return fo, l }},
		{"leader deaf to follower", func(l, fo fleet.Node) (fleet.Node, fleet.Node) { return l, fo }},
	}
	for _, c := range cases {
		realistic.RequireFaultBudget(t, c.name, faultWorst)
		r := infra.RequireHealthy(t)
		leader, follower := infra.Leader(t, r), infra.Followers(t, r)[0]
		deaf, unheard := c.pick(leader, follower)
		serveVia := others(f, follower)
		t.Run(c.name, func(t *testing.T) {
			realistic.DropFrom(t, f, deaf, unheard)
			requireServing(t, serveVia, "while "+deaf.Name+" cannot hear "+unheard.Name)
			s.writeN(t, serveVia[0], c.name, rowsPerPhase)
		})
		healed(t, "the cluster after "+c.name)
		for _, n := range f.State.Nodes {
			s.requireRows(t, n, c.name, rowsPerPhase)
		}
	}
}

// TestChaos_lossyOverlayKeepsServing: a follower's overlay delays every
// packet and drops a tenth of them. Every node keeps serving and writes made
// through the degraded node still land; afterwards the cluster converges.
func TestChaos_lossyOverlayKeepsServing(t *testing.T) {
	f := harness.Fleet(t)
	realistic.RequireFaultBudget(t, "the lossy overlay", faultWorst)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	s := newStore(t)
	t.Run("degraded", func(t *testing.T) {
		how := realistic.Degrade(t, f, victim, degradeDelayMS, degradeLossPct)
		t.Logf("%s's overlay degraded with %s: %dms, %d%% loss", victim.Name, how, degradeDelayMS, degradeLossPct)
		requireServing(t, f.State.Nodes, "on a lossy overlay")
		s.writeN(t, victim, "lossy", rowsPerPhase)
	})
	healed(t, "the cluster after the lossy overlay")
	for _, n := range f.State.Nodes {
		s.requireRows(t, n, "lossy", rowsPerPhase)
	}
}
