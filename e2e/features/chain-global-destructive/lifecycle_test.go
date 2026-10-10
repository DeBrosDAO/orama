//go:build e2e_fleet

package chainglobaldestructive

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// mainPID is the chain unit's main process id.
func mainPID(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	return sh(t, c, n, "systemctl show -p MainPID --value "+chain.Unit)
}

// TestGlobalLifecycle_stopStartRestartTheChain: `orama global stop chain`
// stops the chain unit (and every installed service that needs it) and
// `status` reports it inactive; `start` starts it again after the sign-floor
// check and waits for its RPC, and the validator catches up; `start` on a
// running chain changes nothing; `restart` with no service named restarts
// every installed service, chain first, under a new process. With three equal
// bootstrap seats the chain halts while the validator is down and moves
// again on every validator afterwards, invariants intact.
func TestGlobalLifecycle_stopStartRestartTheChain(t *testing.T) {
	c := chain.New(t)
	n := victim(t, c)
	c.AdvanceAtCleanup(t)
	restoreAtCleanup(t, c, n, "")
	infra.ExpectNodeExit(t, "stop chain", orama(t, c, n, "global", "stop", "chain"), infra.ExitOK, "stop: "+chain.Unit)
	if got := unitState(t, c, n); got == stateActive {
		t.Fatalf("%s: the chain unit is still %s after global stop", n.Name, got)
	}
	if got := unitState(t, c, n); got != infra.UnitInactive {
		t.Errorf("%s: the stopped chain unit is %s, want %s", n.Name, got, infra.UnitInactive)
	}
	infra.ExpectNodeExit(t, "a second stop", orama(t, c, n, "global", "stop", "chain"), infra.ExitOK)
	infra.ExpectNodeExit(t, "start chain", orama(t, c, n, "global", "start", "chain"), infra.ExitOK, "waiting for the chain RPC")
	requireChainBack(t, c, n, "global start")
	pid := mainPID(t, c, n)
	infra.ExpectNodeExit(t, "start of a running chain", orama(t, c, n, "global", "start"), infra.ExitOK)
	if got := mainPID(t, c, n); got != pid {
		t.Errorf("global start on a running chain changed its process %s -> %s", pid, got)
	}
	infra.ExpectNodeExit(t, "restart", orama(t, c, n, "global", "restart"), infra.ExitOK, "stop: "+chain.Unit, "start: "+chain.Unit)
	requireChainBack(t, c, n, "global restart")
	if got := mainPID(t, c, n); got == pid {
		t.Errorf("global restart left the chain in process %s", pid)
	}
	if got := unitState(t, c, n); got != stateActive {
		t.Errorf("%s: the restarted chain unit is %s, want %s", n.Name, got, stateActive)
	}
}
