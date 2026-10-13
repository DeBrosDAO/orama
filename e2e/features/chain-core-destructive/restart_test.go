//go:build e2e_fleet

package chaincoredestructive

import (
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// Budgets of the restart case.
const (
	// observeBlocks is how many block intervals the survivors are watched
	// while one validator is down.
	observeBlocks = 10
	// blockInterval paces the poll for the survivors' next block. The run chain
	// keeps the config oramad init writes (chain-deploy.sh edits no consensus
	// timeouts): timeout_commit 5s, so blocks come about every 5 s.
	blockInterval = 3 * time.Second
	// catchUpBudget bounds the restarted validator's return to the head.
	catchUpBudget = 5 * time.Minute
)

// TestChainRestart_stoppedValidatorCatchesUp: a validator whose unit is
// stopped and started again catches up to the same chain: the same block and
// app hash at every height as the others, not catching up, and blocks keep
// coming. While it is down the survivors commit blocks only if they hold more
// than 2/3 of the voting power (CometBFT): with the run's three equal
// bootstrap seats (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md "x/power": B_i = 1/3 each, and C_i falls
// back to equal shares because 5% x 3 < 1) they hold exactly 2/3, so the
// chain must halt, not fork.
func TestChainRestart_stoppedValidatorCatchesUp(t *testing.T) {
	c := chain.New(t)
	victim := c.Node(t, len(c.Nodes())-1)
	survivors := c.Nodes()[:len(c.Nodes())-1]
	victimPower := c.MustStatus(t, victim).VotingPow
	quorumWithout := c.SurvivorsHaveQuorum(t, survivors[0], victim)
	c.AdvanceAtCleanup(t)
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
		t.Errorf("survivors committed=%v, but they hold quorum=%v without a validator of %d voting power (CometBFT needs more than 2/3)", moved, quorumWithout, victimPower)
	}
	c.F.MustExec(t, victim, "systemctl start "+chain.Unit)
	c.RequireSameChain(t, c.Nodes(), catchUpBudget, "after a validator restart")
}
