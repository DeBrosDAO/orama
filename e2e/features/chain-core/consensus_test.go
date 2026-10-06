//go:build e2e_fleet

package chaincore

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// sampledBlocks is how many consecutive heights every validator is compared on.
const sampledBlocks = 5

// TestConsensus_validatorsAgreeOnEveryBlock: every validator stores the same
// block hash and app hash at the same heights, for the run's chain id, and
// none is catching up (docs/CHAIN.md: one CometBFT network; a diverging
// app hash is a consensus failure).
func TestConsensus_validatorsAgreeOnEveryBlock(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	top := c.Height(t)
	c.WaitHeight(t, top+1)
	for h := top - sampledBlocks + 1; h <= top; h++ {
		var first chain.Header
		for i, n := range c.Nodes() {
			hd, err := c.BlockHeader(t, n, h)
			if err != nil {
				t.Fatal(err)
			}
			if hd.ChainID != c.ID || hd.Height != h {
				t.Fatalf("%s: block %d names chain %q height %d, want %q", n.Name, h, hd.ChainID, hd.Height, c.ID)
			}
			if i == 0 {
				first = hd
				continue
			}
			if hd.BlockHash != first.BlockHash || hd.AppHash != first.AppHash {
				t.Errorf("height %d: %s has block %s app %s, %s has block %s app %s",
					h, c.Nodes()[0].Name, first.BlockHash, first.AppHash, n.Name, hd.BlockHash, hd.AppHash)
			}
		}
	}
	for _, n := range c.Nodes() {
		s := c.MustStatus(t, n)
		if s.Network != c.ID || s.CatchingUp || s.VotingPow <= 0 {
			t.Errorf("%s: network=%q catching_up=%v voting_power=%d, want %q, false, >0", n.Name, s.Network, s.CatchingUp, s.VotingPow, c.ID)
		}
	}
}

// TestConsensus_chainLinksAndAdvances: each block names the previous one's
// hash (a validator serving a forked history would break the link), and the
// latest height grows on every node while we watch.
func TestConsensus_chainLinksAndAdvances(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	top := c.Height(t)
	prev, err := c.BlockHeader(t, n, top-1)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := c.BlockHeader(t, n, top)
	if err != nil {
		t.Fatal(err)
	}
	if cur.LastBlockHash != prev.BlockHash {
		t.Errorf("block %d links to %s, block %d is %s", top, cur.LastBlockHash, top-1, prev.BlockHash)
	}
	start := map[string]int64{}
	for _, v := range c.Nodes() {
		start[v.Name] = c.MustStatus(t, v).Height
	}
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "every validator to commit new blocks", func() (bool, error) {
		for _, v := range c.Nodes() {
			s, err := c.NodeStatus(t, v)
			if err != nil {
				return false, err
			}
			if s.Height <= start[v.Name] {
				return false, fmt.Errorf("%s still at %d", v.Name, s.Height)
			}
		}
		return true, nil
	})
}

// TestConsensus_futureBlockIsRefused: asking a node for a height it has not
// committed is an error, not an empty or invented block (edge: boundary
// height).
func TestConsensus_futureBlockIsRefused(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 0)
	far := c.Height(t) + 1_000_000
	if _, err := c.BlockHeader(t, n, far); err == nil {
		t.Fatalf("%s served block %d, which is far beyond its head", n.Name, far)
	}
	if _, err := c.BlockHeader(t, n, 0); err == nil {
		t.Fatalf("%s served block 0, which does not exist", n.Name)
	}
}
