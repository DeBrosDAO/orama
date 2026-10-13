//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	// partitionTxs is the public transfers signed ahead for the partition,
	// partitionChunk how many go into the survivor's mempool per poll while
	// the cut takes hold.
	partitionTxs   = 40
	partitionChunk = 5
	// cutBudget bounds the isolated validator noticing it lost every peer:
	// CometBFT pings every 60 s and drops a peer 45 s after an unanswered ping.
	cutBudget = 4 * time.Minute
	// partitionWorst is the most the partition test takes with its recovery.
	partitionWorst = cutBudget + observeFor + healBudget + chain.EpochBudget + chain.CatchUpBudget
)

// TestChainPartition_isolatedValidatorCatchesUpAfterHeal: one validator's p2p
// is dropped in both directions while public transfers keep arriving at a
// survivor. The others commit blocks if and only if they hold more than 2/3
// of the voting power (a chain of three equal seats halts, which is correct;
// five commit); the isolated validator never gets ahead of them; after the
// heal it catches up, every node holds the same blocks and app hash, every
// transfer is delivered exactly once, and the validator is neither jailed nor
// slashed (website/src/docs/blockchain/run-a-global-node.mdx,
// docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).
func TestChainPartition_isolatedValidatorCatchesUpAfterHeal(t *testing.T) {
	c := chain.New(t)
	realistic.RequireFaultBudget(t, "the chain partition", partitionWorst)
	victim, survivors := roles(t, c)
	ref := survivors[0]
	quorum := c.SurvivorsHaveQuorum(t, ref, victim)
	load := c.NewTransferLoad(t, ref, "partition", partitionTxs)
	power := c.MustStatus(t, victim).VotingPow
	c.AdvanceAtCleanupWithin(t, healBudget)

	heal := c.Partition(t, victim)
	sent := 0
	eventually.Require(t, chain.PollEvery, cutBudget, victim.Name+" to lose every peer while transfers keep coming", func() (bool, error) {
		next := min(sent+partitionChunk, load.Total())
		chain.RequireAccepted(t, "during the partition", load.Broadcast(t, sent, next))
		sent = next
		peers, err := c.PeerCount(t, victim)
		if err != nil {
			return false, err
		}
		if peers != 0 {
			return false, fmt.Errorf("%s still has %d peers", victim.Name, peers)
		}
		return true, nil
	})
	from := c.MustStatus(t, ref).Height
	requireQuorumRule(t, c, ref, quorum, from, "while "+victim.Name+" is cut off")
	if quorum {
		load.WaitDelivered(t, ref, sent)
	}
	if v, r := c.MustStatus(t, victim).Height, c.MustStatus(t, ref).Height; v > r {
		t.Errorf("the isolated %s is at height %d, ahead of the others at %d", victim.Name, v, r)
	}

	heal()
	chain.RequireAccepted(t, "after the heal", load.Broadcast(t, sent, load.Total()))
	c.RequireSameChain(t, c.Nodes(), healBudget, "after the partition healed")
	load.WaitDelivered(t, ref, load.Total())
	for _, n := range c.Nodes() {
		load.WaitDelivered(t, n, load.Total())
		load.RequireBalance(t, n, load.Total())
	}
	requirePowerKept(t, c, victim, power, "the partition")
}
