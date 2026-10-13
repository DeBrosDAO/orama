//go:build e2e_fleet

package chainchaos

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// clockSkew is how far one validator's clock is moved: minutes, far past any
	// drift NTP leaves, the seconds a block takes and monitoring's 60 s critical
	// threshold, and short enough for the WireGuard handshakes of a node whose
	// clock went back (a peer drops an initiation older than the last it saw).
	clockSkew = 2 * time.Minute
	// skewBlocks is how many blocks are examined with the clock moved.
	skewBlocks = 6
	// clockWorst is the most one skew direction takes with its recovery.
	clockWorst = chain.EpochBudget + chain.CatchUpBudget + infra.ConvergeBudget
)

// TestChainClock_skewedValidatorDoesNotMoveBlockTime: one validator's clock is
// moved two minutes ahead, then two minutes behind (NTP off, restored at
// cleanup). The chain runs CometBFT with BFT time, not proposer-based
// timestamps (cometbft v0.39 has none): a block's time is the voting-power
// weighted median of the previous commit's precommit times, and a validator
// stamps its precommit with its own clock but never earlier than the block it
// signs (consensus voteTime). So consensus continues on every node, the
// skewed validator included; block times stay at real time because one
// outlier of three is not the median; a clock ahead shows only in the
// skewed validator's own precommit times, and a clock behind is held at the
// block's time. Every node keeps the same blocks and app hash, and the
// validator keeps its voting power
// (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).
func TestChainClock_skewedValidatorDoesNotMoveBlockTime(t *testing.T) {
	c := chain.New(t)
	realistic.RequireFaultBudget(t, "the clock skews", 2*clockWorst)
	victim, survivors := roles(t, c)
	for _, tc := range []struct {
		name   string
		offset time.Duration
	}{{"ahead", clockSkew}, {"behind", -clockSkew}} {
		t.Run(tc.name, func(t *testing.T) {
			status := c.MustStatus(t, victim)
			addr, power := status.ValidatorAddr, status.VotingPow
			infra.ConvergeInCleanup(t, len(c.Nodes()), infra.ConvergeBudget, "the cluster after the clock of "+victim.Name+" was moved")
			c.AdvanceAtCleanupWithin(t, healBudget)
			c.F.ClockSkew(t, victim, tc.offset)
			from, start := c.MustStatus(t, survivors[0]).Height, time.Now()
			c.WaitHeight(t, from+skewBlocks+1)
			seen := 0
			for h := from + 1; h <= from+skewBlocks; h++ {
				if requireVoteRules(t, c, survivors[0], addr, h, tc.offset, start) {
					seen++
				}
			}
			if seen == 0 {
				t.Errorf("%s: no precommit of %s in blocks %d..%d: its votes were not counted", tc.name, victim.Name, from+1, from+skewBlocks)
			}
			c.RequireSameChain(t, c.Nodes(), chain.CatchUpBudget, "with the clock of "+victim.Name+" "+tc.name)
			requirePowerKept(t, c, victim, power, "a clock "+tc.name)
		})
	}
}

// requireVoteRules checks block h against the BFT time rules for a validator
// (consensus address addr) whose clock was moved by offset at start, and
// reports whether that validator's precommit is in the commit.
func requireVoteRules(t *testing.T, c *chain.Chain, observer fleet.Node, addr string, h int64, offset time.Duration, start time.Time) bool {
	t.Helper()
	blockTime, votes, err := c.CommitVotes(t, observer, h)
	if err != nil {
		t.Errorf("height %d: %v", h, err)
		return false
	}
	if lo, hi := start.Add(-clockSkew/2), time.Now().Add(clockSkew/2); blockTime.Before(lo) || blockTime.After(hi) {
		t.Errorf("height %d: the block's time %s is outside real time [%s, %s]: the skewed clock moved the median", h, blockTime, lo, hi)
	}
	vote, ok := votes[addr]
	if !ok {
		return false
	}
	gap := vote.Sub(blockTime)
	switch {
	case offset > 0 && gap < offset/2:
		t.Errorf("height %d: the validator %s ahead stamped its precommit %s after the block, want about its own clock", h, offset, gap)
	case offset < 0 && (gap < 0 || gap > clockSkew/2):
		t.Errorf("height %d: the validator %s behind stamped its precommit %s after the block, want it held at the block's time", h, -offset, gap)
	}
	return true
}
