//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

const (
	// killTxs is the public transfers signed ahead for the kill.
	killTxs = 30
	// killWorst is the most the kill test takes with its recovery.
	killWorst = restartBudget + chain.CatchUpBudget + chain.EpochBudget
)

// TestChainKill_oramadKilledUnderLoadRestartsAndAgrees: kill -9 of the chain
// process of one validator while public transfers are submitted. systemd
// restarts the unit by itself (Restart=always), the validator replays to the
// head, every node holds the same blocks and app hash, every transfer is
// delivered once, and the restarted validator keeps its voting power: a crash
// between signing and persisting its sign state must not double-sign
// (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md).
func TestChainKill_oramadKilledUnderLoadRestartsAndAgrees(t *testing.T) {
	c := chain.New(t)
	realistic.RequireFaultBudget(t, "the kill -9 of oramad", killWorst)
	victim, survivors := roles(t, c)
	ref := survivors[0]
	load := c.NewTransferLoad(t, ref, "kill", killTxs)
	power := c.MustStatus(t, victim).VotingPow
	before := mainPID(t, c, victim)
	c.AdvanceAtCleanup(t)

	c.F.Kill(t, victim, chain.Unit)
	chain.RequireAccepted(t, "while "+victim.Name+" restarts", load.Broadcast(t, 0, load.Total()))
	eventually.Require(t, chain.PollEvery, restartBudget, victim.Name+" to be restarted by systemd with a new process", func() (bool, error) {
		state, pid := c.F.Unit(t, victim, chain.Unit), mainPID(t, c, victim)
		if state != stateActive || pid == 0 || pid == before {
			return false, fmt.Errorf("unit %s, main pid %d (was %d)", state, pid, before)
		}
		return true, nil
	})

	c.RequireSameChain(t, c.Nodes(), chain.CatchUpBudget, "after oramad was killed")
	load.WaitDelivered(t, ref, load.Total())
	for _, n := range c.Nodes() {
		load.WaitDelivered(t, n, load.Total())
		load.RequireBalance(t, n, load.Total())
	}
	requirePowerKept(t, c, victim, power, "kill -9")
}
