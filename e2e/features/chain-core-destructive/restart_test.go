//go:build e2e_fleet

package chaincoredestructive

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Budgets of the restart case.
const (
	// observeBlocks is how many block intervals the survivors are watched
	// while one validator is down.
	observeBlocks = 10
	// blockInterval is CometBFT's default timeout_commit plus slack; the run
	// chain keeps the default config (chain-deploy.sh edits no consensus
	// timeouts).
	blockInterval = 3 * time.Second
	// catchUpBudget bounds the restarted validator's return to the head.
	catchUpBudget = 5 * time.Minute
)

// validatorSet is CometBFT /validators.
type validatorSet struct {
	Validators []struct {
		Address     string    `json:"address"`
		VotingPower chain.Int `json:"voting_power"`
	} `json:"validators"`
}

// TestChainRestart_stoppedValidatorCatchesUp: a validator whose unit is
// stopped and started again catches up to the same chain: the same block and
// app hash at every height as the others, not catching up, and blocks keep
// coming. While it is down the survivors commit blocks only if they hold more
// than 2/3 of the voting power (CometBFT): with the run's three equal
// bootstrap seats (docs/CHAIN.md "x/power": B_i = 1/3 each, and C_i falls
// back to equal shares because 5% x 3 < 1) they hold exactly 2/3, so the
// chain must halt, not fork.
func TestChainRestart_stoppedValidatorCatchesUp(t *testing.T) {
	c := chain.New(t)
	victim := c.Node(t, len(c.Nodes())-1)
	survivors := c.Nodes()[:len(c.Nodes())-1]
	var set validatorSet
	if err := c.Comet(t, survivors[0], "/validators", &set); err != nil {
		t.Fatal(err)
	}
	victimPower := c.MustStatus(t, victim).VotingPow
	total := int64(0)
	for _, v := range set.Validators {
		total += v.VotingPower.Int64()
	}
	quorumWithout := 3*(total-victimPower) > 2*total
	c.F.StopService(t, victim, chain.Unit)
	h0 := c.MustStatus(t, survivors[0]).Height
	// Watching for a fixed number of block intervals is the observation
	// itself (does the chain move without the victim?), polled, not slept.
	moved := eventually.Poll(t.Context(), blockInterval, observeBlocks*blockInterval, "survivors to commit without "+victim.Name, func() (bool, error) {
		h := c.MustStatus(t, survivors[0]).Height
		if h > h0+1 {
			return true, nil
		}
		return false, fmt.Errorf("height %d (was %d)", h, h0)
	}) == nil
	if moved != quorumWithout {
		t.Errorf("survivors committed=%v with %d of %d voting power; CometBFT needs more than 2/3", moved, total-victimPower, total)
	}
	c.F.MustExec(t, victim, "systemctl start "+chain.Unit)
	requireCaughtUp(t, c, victim)
}

// requireCaughtUp waits for the restarted victim to rejoin the head and then
// compares every validator's latest common blocks.
func requireCaughtUp(t *testing.T, c *chain.Chain, victim fleet.Node) {
	t.Helper()
	ref := c.Nodes()[0]
	target := c.MustStatus(t, ref).Height + 2
	err := eventually.Poll(t.Context(), chain.PollEvery, catchUpBudget, victim.Name+" to catch up", func() (bool, error) {
		s, err := c.NodeStatus(t, victim)
		if err != nil {
			return false, err
		}
		if s.CatchingUp || s.Height < target {
			return false, fmt.Errorf("%s at %d catching_up=%v, want >= %d", victim.Name, s.Height, s.CatchingUp, target)
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("after restart: %v", err)
		return
	}
	for h := target - 2; h <= target; h++ {
		want, err := c.BlockHeader(t, ref, h)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		got, err := c.BlockHeader(t, victim, h)
		if err != nil || got.BlockHash != want.BlockHash || got.AppHash != want.AppHash {
			t.Errorf("height %d: %s has %+v (%v), %s has %+v", h, victim.Name, got, err, ref.Name, want)
		}
	}
	c.RequireInvariants(t, "a validator restart")
}
