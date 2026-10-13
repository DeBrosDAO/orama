//go:build e2e_fleet

package chainglobaldestructive

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Paths of a co-hosted validator (core/pkg/constants/chain.go and global.go).
const (
	keyFile      = chain.Home + "/config/priv_validator_key.json"
	migrationKey = "/var/lib/orama-global/migrate-recipient.key"
	sealedMode   = "600"
	// globalBudget bounds one `orama global` command: a start waits up to five
	// minutes for the chain's RPC.
	globalBudget = chain.TxBudget
	stateActive  = infra.UnitActive
)

// victim is the validator every test here disturbs: the last one.
func victim(t *testing.T, c *chain.Chain) fleet.Node { return c.Node(t, len(c.Nodes())-1) }

// orama runs `orama <args>` on n as root.
func orama(t *testing.T, c *chain.Chain, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	return c.Run(t, n, globalBudget, infra.OramaCommand(args...))
}

// sh runs a shell command on n and returns its trimmed stdout.
func sh(t *testing.T, c *chain.Chain, n fleet.Node, cmd string) string {
	t.Helper()
	return strings.TrimSpace(c.Run(t, n, chain.QueryBudget, cmd).Stdout)
}

func exists(t *testing.T, c *chain.Chain, n fleet.Node, path string) bool {
	t.Helper()
	return c.Run(t, n, chain.QueryBudget, "test -e "+fleet.ShellQuote(path)).Exit == 0
}

func unitState(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	return sh(t, c, n, "systemctl is-active "+chain.Unit)
}

// requireChainBack checks the validator is running again and the chain moves
// on every validator with every module's invariants intact.
func requireChainBack(t *testing.T, c *chain.Chain, n fleet.Node, after string) {
	t.Helper()
	if got := unitState(t, c, n); got != stateActive {
		t.Fatalf("%s: the chain unit is %s after %s", n.Name, got, after)
	}
	if err := c.WaitAllAdvance(t); err != nil {
		t.Fatalf("after %s: %v", after, err)
	}
	c.RequireInvariants(t, after)
}

// quarantinedKeys is where `migrate export` keeps the copy of the key it moves
// out of the chain home (core/pkg/globalnode/validator.go keepCopy).
const quarantinedKeys = "/var/lib/orama-global/validator-key-migrated-*.json"

// restoreKey puts the newest quarantined key copy back in the chain home, for
// an export that moved the key and then failed to write its bundle.
var restoreKey = "src=$(ls -t " + quarantinedKeys + " 2>/dev/null | head -1) && [ -n \"$src\" ] && " +
	"install -o " + chain.ServiceUser + " -g " + chain.ServiceUser + " -m 0600 \"$src\" " + keyFile

// restoreAtCleanup registers the cleanup that leaves the validator as it was
// found: the key back in the chain home (imported from bundle when it moved
// away and the bundle was written, copied back from the quarantined copy when
// it was not) and the chain running. Register it after the cleanups it must
// run before (a prepared migration key it imports with is cancelled by a
// cleanup registered earlier) and after c.AdvanceAtCleanup.
func restoreAtCleanup(t *testing.T, c *chain.Chain, n fleet.Node, bundle string) {
	t.Helper()
	t.Cleanup(func() {
		fail := func(what string, out fleet.Output, err error) {
			t.Errorf("cleanup: %s on %s: exit %d: %v %s", what, n.Name, out.Exit, err, c.F.Redact(out.Stdout+out.Stderr))
		}
		try := func(what, cmd string, budget time.Duration) {
			if out, err := c.TryRun(t, n, budget, cmd); err != nil || out.Exit != 0 {
				fail(what, out, err)
			}
		}
		if bundle != "" && keyMissing(t, c, n) {
			try("import the validator key back", infra.OramaCommand("maint", "global", "validator", "migrate", "import", "--from", bundle), globalBudget)
		}
		if keyMissing(t, c, n) {
			try("copy the quarantined validator key back", restoreKey, chain.QueryBudget)
		}
		if st, err := c.TryRun(t, n, chain.QueryBudget, "systemctl is-active "+chain.Unit); err == nil && strings.TrimSpace(st.Stdout) != stateActive {
			try("start the chain", infra.OramaCommand("global", "start"), globalBudget)
		}
	})
}

// keyMissing is true when the chain home has no validator key. It runs from
// cleanups, so an unreachable node counts as not missing: nothing can be
// restored on it anyway.
func keyMissing(t *testing.T, c *chain.Chain, n fleet.Node) bool {
	t.Helper()
	out, err := c.TryRun(t, n, chain.QueryBudget, "test -e "+keyFile)
	return err == nil && out.Exit != 0
}

// prepareMigration runs `migrate prepare` on n, cancels what it prepared at
// cleanup, and returns the migration public key.
func prepareMigration(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	t.Cleanup(func() { c.CleanupExec(t, n, infra.OramaCommand("maint", "global", "validator", "migrate", "cancel")) })
	out := orama(t, c, n, "maint", "global", "validator", "migrate", "prepare")
	infra.ExpectNodeExit(t, "migrate prepare", out, infra.ExitOK)
	return strings.TrimSpace(out.Stdout)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
