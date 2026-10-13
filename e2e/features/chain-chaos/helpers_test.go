//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Budgets shared by the faults of this package. The run chain keeps the
// config oramad init writes (timeout_commit 5s), so blocks come about every
// 5 s.
const (
	// observeFor is how long the nodes that still hold the chain are watched
	// for a new block: a few block intervals.
	observeFor = 45 * time.Second
	// movedBlocks is how many blocks past its starting height a chain must
	// commit to count as moving.
	movedBlocks = 1
	// restartBudget bounds systemd restarting a killed chain unit by itself
	// (RestartSec 5s) and its RPC answering again.
	restartBudget = 3 * time.Minute
	// healBudget bounds a healed or restored validator rejoining the head:
	// persistent peers redial with a growing backoff after a long cut.
	healBudget = 10 * time.Minute
	// stateActive is systemd's active state.
	stateActive = "active"
)

// roles splits the validators into the one every fault here disturbs (the
// last, as the other chain packages do) and the rest.
func roles(t testing.TB, c *chain.Chain) (victim fleet.Node, survivors []fleet.Node) {
	t.Helper()
	nodes := c.Nodes()
	if len(nodes) < 3 {
		t.Fatalf("the run has %d validators: a fault on one needs at least 3", len(nodes))
	}
	return c.Node(t, len(nodes)-1), nodes[:len(nodes)-1]
}

// movedWithout reports whether observer commits a block past from within
// observeFor.
func movedWithout(t *testing.T, c *chain.Chain, observer fleet.Node, from int64, what string) bool {
	t.Helper()
	return eventually.Poll(t.Context(), chain.PollEvery, observeFor, what, func() (bool, error) {
		h := c.MustStatus(t, observer).Height
		if h > from+movedBlocks {
			return true, nil
		}
		return false, fmt.Errorf("height %d (was %d)", h, from)
	}) == nil
}

// requireQuorumRule checks CometBFT's rule for a chain that lost a validator:
// the others commit blocks if and only if they hold more than 2/3 of the
// voting power (a halt without it is correct, a fork is not).
func requireQuorumRule(t *testing.T, c *chain.Chain, observer fleet.Node, quorum bool, from int64, what string) {
	t.Helper()
	if moved := movedWithout(t, c, observer, from, "the chain to commit "+what); moved != quorum {
		t.Errorf("%s: committed=%v, but the others hold quorum=%v of the voting power (CometBFT needs more than 2/3)", what, moved, quorum)
	}
}

// systemdValue is `systemctl show -p <prop> --value` of the chain unit on n.
func systemdValue(t testing.TB, c *chain.Chain, n fleet.Node, prop string) string {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, "systemctl show -p "+prop+" --value "+chain.Unit)
	return strings.TrimSpace(out.Stdout)
}

// mainPID is the chain unit's main process on n, 0 when it is not running.
func mainPID(t testing.TB, c *chain.Chain, n fleet.Node) int {
	t.Helper()
	pid, err := strconv.Atoi(systemdValue(t, c, n, "MainPID"))
	if err != nil {
		t.Fatalf("%s: MainPID of %s is not a number: %v", n.Name, chain.Unit, err)
	}
	return pid
}

// requirePowerKept fails when n's voting power is not what it was: a
// validator that signed twice or missed too many blocks would be jailed or
// slashed out of the set.
func requirePowerKept(t testing.TB, c *chain.Chain, n fleet.Node, want int64, after string) {
	t.Helper()
	if got := c.MustStatus(t, n).VotingPow; got != want {
		t.Errorf("%s has voting power %d after %s, was %d: it was jailed or slashed", n.Name, got, after, want)
	}
}
