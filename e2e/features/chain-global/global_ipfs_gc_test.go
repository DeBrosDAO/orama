//go:build e2e_fleet

package chainglobal

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// globalIPFSGCUnit is the oneshot `orama global install --services ipfs`
	// writes to garbage-collect the public Kubo repo.
	globalIPFSGCUnit = "orama-global-ipfs-gc.service"
	// globalIPFSGCEnv holds the daemon's RPC bearer for the unit (mode 0600).
	globalIPFSGCEnv = constants.GlobalIPFSHome + "/gc.env"
)

// ipfsGCNode is a node that has the public Kubo's GC unit: the one
// `orama global install --services ipfs` ran on. A run chain has none.
func ipfsGCNode(t *testing.T) (*chain.Chain, fleet.Node) {
	t.Helper()
	c := chain.New(t)
	for _, n := range c.F.State.Nodes {
		if c.F.Exec(t, n, "systemctl cat "+globalIPFSGCUnit).Exit == 0 {
			return c, n
		}
	}
	harness.SkipNotApplicable(t, "no node of this target has "+globalIPFSGCUnit+": install the public Kubo with `orama global install --services ipfs` (docs/RUN_A_GLOBAL_NODE.md)")
	return nil, fleet.Node{}
}

// The GC oneshot once ran `ipfs --api-auth=<bearer> repo gc`, so the bearer was
// in the process's argv, readable by every local user for the whole collection.
// It is `orama node ipfs-gc` now, and the bearer reaches it through the
// environment file. Neither the unit nor its ExecStart holds the bearer or an
// --api-auth flag.
func TestGlobalIPFSGC_unitHoldsNoBearerOnItsCommandLine(t *testing.T) {
	c, n := ipfsGCNode(t)

	token := strings.TrimSpace(c.F.MustExec(t, n, "sudo cat "+fleet.ShellQuote(constants.GlobalIPFSHome+"/"+constants.GlobalIPFSAPITokenFile)).Stdout)
	if token == "" {
		t.Fatalf("%s: %s/%s is empty, so the test cannot tell whether the bearer is in the unit", n.Name, constants.GlobalIPFSHome, constants.GlobalIPFSAPITokenFile)
	}
	unit := c.F.MustExec(t, n, "systemctl cat "+globalIPFSGCUnit).Stdout
	execStart := c.F.MustExec(t, n, "systemctl show -p ExecStart --value "+globalIPFSGCUnit).Stdout

	if !strings.Contains(execStart, "node ipfs-gc") {
		t.Errorf("%s: ExecStart of %s is %q, want `orama node ipfs-gc`", n.Name, globalIPFSGCUnit, execStart)
	}
	if !strings.Contains(unit, "EnvironmentFile="+globalIPFSGCEnv) {
		t.Errorf("%s: %s does not read the bearer from %s", n.Name, globalIPFSGCUnit, globalIPFSGCEnv)
	}
	for name, text := range map[string]string{"the unit": unit, "its ExecStart": execStart} {
		if strings.Contains(text, token) {
			t.Errorf("%s: %s holds the RPC bearer", n.Name, name)
		}
		for _, flag := range []string{"--api-auth", "api-auth=", "bearer:"} {
			if strings.Contains(text, flag) {
				t.Errorf("%s: %s has %q, a credential on a command line", n.Name, name, flag)
			}
		}
	}
	if mode := strings.TrimSpace(c.F.MustExec(t, n, "stat -c %a "+globalIPFSGCEnv).Stdout); mode != "600" {
		t.Errorf("%s: %s is mode %s, want 600", n.Name, globalIPFSGCEnv, mode)
	}
}

// Run on a global node, the oneshot collects through the daemon's RPC and ends
// successful. `systemctl start` on a oneshot returns when it has ended, so the
// unit's result is the run's.
func TestGlobalIPFSGC_runsThroughTheDaemonAndEndsSuccessful(t *testing.T) {
	c, n := ipfsGCNode(t)

	c.F.MustExec(t, n, "systemctl start "+globalIPFSGCUnit)
	show := c.F.MustExec(t, n, "systemctl show -p Result -p ExecMainStatus "+globalIPFSGCUnit).Stdout
	for _, want := range []string{"Result=success", "ExecMainStatus=0"} {
		if !strings.Contains(show, want) {
			t.Errorf("%s: %s after a run: %s; want %q", n.Name, globalIPFSGCUnit, strings.TrimSpace(show), want)
		}
	}
	if got := c.F.Unit(t, n, globalIPFSGCUnit); got == "failed" {
		t.Errorf("%s: %s is failed after a run", n.Name, globalIPFSGCUnit)
	}
	if log := c.F.MustExec(t, n, "journalctl -u "+globalIPFSGCUnit+" -n 20 --no-pager -o cat").Stdout; !strings.Contains(log, "removed") {
		t.Errorf("%s: the run's log does not say how many blocks it removed: %s", n.Name, log)
	}
}
