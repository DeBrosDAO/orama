//go:build e2e_fleet

package chaincoredestructive

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// loadPerValidator: three validators x 5 transactions of 7M gas wanted is
// 105M, more than one 100M block holds; each consumes at least ~6.5M
// (chain.LoadGasConsumed), so the 15 consume ~98M, far over the 50M target.
const loadPerValidator = 5

// TestBaseFee_risesOnFullBlocksAndFloorsAtMin: blocks more than half full
// raise the base fee (by at least one norama at the floor: security review
// B4, x/fees/types/basefee.go NextBaseFee), emptier blocks lower it again,
// and it never goes below min_base_fee (docs/whitepaper/technical-reference/vol2/40-economics.md "The base fee").
func TestBaseFee_risesOnFullBlocksAndFloorsAtMin(t *testing.T) {
	c := chain.New(t)
	n := c.Node(t, 0)
	floor := c.BaseFee(t, n)
	if floor.Cmp(chain.NewInt(1)) != 0 {
		t.Fatalf("base fee %s before the load, want the floor 1 (an idle chain)", floor.String())
	}
	dirs := map[chain.Key]string{}
	for i := range c.Nodes() {
		k := c.FundedValidator(t, i, chain.Orama(1))
		dirs[k] = c.PrepareLoad(t, k, loadPerValidator)
	}
	start := c.Height(t)
	codes := c.FireLoad(t, dirs)
	accepted := 0
	for node, cs := range codes {
		for _, code := range cs {
			if code == "0" {
				accepted++
			} else {
				t.Logf("%s: a load transaction was refused by CheckTx with %s", node, code)
			}
		}
	}
	if accepted*chain.LoadGasConsumed <= blockMaxGas/2 {
		t.Fatalf("only %d load transactions entered the mempool: at least ~%d gas is not over half a block", accepted, accepted*chain.LoadGasConsumed)
	}
	peak := waitBaseFeeAbove(t, c, floor, start)
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "the base fee to fall back to the floor", func() (bool, error) {
		bf := c.BaseFee(t, n)
		if bf.Cmp(floor) != 0 {
			return false, fmt.Errorf("base fee %s", bf.String())
		}
		return true, nil
	})
	end := c.Height(t) - 1 // one below the head: its state is readable
	for h := start; h <= end; h++ {
		if bf := c.BaseFeeAt(t, n, h); bf.Cmp(floor) < 0 {
			t.Errorf("height %d: base fee %s under the floor %s", h, bf.String(), floor.String())
		}
	}
	t.Logf("base fee peaked at %s after height %d", peak.String(), start)
	c.RequireInvariants(t, "a full-block load")
}

// blockMaxGas is the run chain's consensus max_gas (chain-deploy.sh).
const blockMaxGas = 100_000_000

// waitBaseFeeAbove scans heights from start until one reports a base fee
// above floor, and returns it.
func waitBaseFeeAbove(t *testing.T, c *chain.Chain, floor chain.Int, start int64) chain.Int {
	t.Helper()
	n := c.Node(t, 0)
	next := start
	var peak chain.Int
	eventually.Require(t, chain.PollEvery, chain.EpochBudget, "a block to raise the base fee", func() (bool, error) {
		head := c.Height(t) - 1 // scan only heights below the head
		for ; next <= head; next++ {
			if bf := c.BaseFeeAt(t, n, next); bf.Cmp(floor) > 0 {
				peak = bf
				return true, nil
			}
		}
		return false, fmt.Errorf("base fee still %s at height %d", floor.String(), head)
	})
	return peak
}
