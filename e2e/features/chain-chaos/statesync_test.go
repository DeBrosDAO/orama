//go:build e2e_fleet

package chainchaos

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// stateSyncBudget bounds the wiped validator discovering a snapshot,
	// restoring it (the application state, the nullifier database) and
	// reaching the head.
	stateSyncBudget = 15 * time.Minute
	// minSnapshotProviders is how many other nodes must cut snapshots: a
	// joiner cross-checks its trusted header between two servers
	// (core/pkg/install stateSyncMinServers).
	minSnapshotProviders = 2
	// stateSyncWorst is the most the state sync test takes with its recovery.
	stateSyncWorst = stateSyncBudget + healBudget
	// snapshotDir is where a node keeps the snapshots it cut: <height>/<format>/
	// holds the chunks, beside the metadata database the running node has open.
	snapshotDir = chain.Home + "/data/snapshots"
)

// tomlValue prints the value of key in [section] of file on the node: the
// installer's settings (core/pkg/install/chainconfig.go) read back. A key the
// file does not have fails the test: every node of the chain has both.
func tomlValue(t testing.TB, c *chain.Chain, n fleet.Node, file, section, key string) string {
	t.Helper()
	script := fmt.Sprintf(`awk '/^\[%s\]/{s=1;next} /^\[/{s=0} s && $1=="%s" {print $3}' %s/config/%s`, section, key, chain.Home, file)
	out := c.MustRun(t, n, chain.QueryBudget, script)
	v := strings.Trim(strings.TrimSpace(out.Stdout), `"`)
	if v == "" {
		t.Fatalf("%s: %s has no [%s] %s", n.Name, file, section, key)
	}
	return v
}

// snapshotInterval is app.toml's state-sync snapshot-interval on n (0: none).
func snapshotInterval(t testing.TB, c *chain.Chain, n fleet.Node) int {
	t.Helper()
	raw := tomlValue(t, c, n, "app.toml", "state-sync", "snapshot-interval")
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		t.Fatalf("%s: snapshot-interval %q is not a count: %v", n.Name, raw, err)
	}
	return v
}

// hasSnapshot reports whether n holds a snapshot on disk: a directory
// <height>/<format> under snapshotDir. (`oramad snapshots list` opens the
// metadata database the running node holds locked.) Exit 1 is "none"; any
// other failure ends the test.
func hasSnapshot(t testing.TB, c *chain.Chain, n fleet.Node) bool {
	t.Helper()
	out := c.Run(t, n, chain.QueryBudget, fmt.Sprintf("test -n \"$(find %s -mindepth 2 -maxdepth 2 -type d 2>/dev/null | head -n 1)\"", snapshotDir))
	if out.Exit > 1 {
		t.Fatalf("%s: looking for snapshots in %s exited %d: %s", n.Name, snapshotDir, out.Exit, out.Stderr)
	}
	return out.Exit == 0
}

// stateSyncPlan finds a validator the installer configured to restore a
// snapshot ([statesync] enable = true, written only for a joiner) and the
// other nodes that cut snapshots. why is not empty when the run has no such
// pair: the test never configures state sync itself.
func stateSyncPlan(t testing.TB, c *chain.Chain) (victim fleet.Node, providers []fleet.Node, why string) {
	t.Helper()
	var enabled []fleet.Node
	for _, n := range c.Nodes() {
		if tomlValue(t, c, n, "config.toml", "statesync", "enable") == "true" {
			enabled = append(enabled, n)
		}
		if snapshotInterval(t, c, n) > 0 && hasSnapshot(t, c, n) {
			providers = append(providers, n)
		}
	}
	if len(enabled) == 0 {
		return victim, nil, "no validator has [statesync] enable = true: the installer writes it only for a joiner (`orama global install --statesync-rpc`, core/pkg/install/chainconfig.go) and chain-deploy.sh writes neither it nor a snapshot interval, so a wiped validator of this chain has no way to restore a snapshot and the test will not configure one"
	}
	victim, providers = enabled[0], without(providers, enabled[0])
	if len(providers) < minSnapshotProviders {
		return victim, nil, fmt.Sprintf("%s is set to state sync but %d other nodes cut snapshots: a joiner needs %d providers", victim.Name, len(providers), minSnapshotProviders)
	}
	return victim, providers, ""
}

func without(nodes []fleet.Node, n fleet.Node) []fleet.Node {
	var out []fleet.Node
	for _, m := range nodes {
		if m.Name != n.Name {
			out = append(out, m)
		}
	}
	return out
}

// TestChainStateSync_wipedValidatorRestoresASnapshotAndAgrees: a validator the
// installer set up to restore a snapshot has its chain data wiped (only its
// sign state kept, so it can not sign a height twice) and is started again. It
// restores a snapshot from the others, reaches the head, and holds the same
// blocks and app hash as every node, nullifier database included
// (docs/whitepaper/technical-reference/vol2/39-chain-architecture.md
// "Lifecycle"). It is not applicable on a chain without state sync configured
// by the installer: a run chain from chain-deploy.sh has none.
func TestChainStateSync_wipedValidatorRestoresASnapshotAndAgrees(t *testing.T) {
	c := chain.New(t)
	victim, providers, why := stateSyncPlan(t, c)
	if why != "" {
		harness.SkipNotApplicable(t, why)
	}
	realistic.RequireFaultBudget(t, "the state sync", stateSyncWorst)
	t.Logf("%s restores a snapshot from %d other nodes", victim.Name, len(providers))
	power := c.MustStatus(t, victim).VotingPow
	backup := fmt.Sprintf("%s/data.e2e-%s", chain.Home, c.F.State.RunID)
	c.AdvanceAtCleanupWithin(t, stateSyncBudget)
	c.F.StopService(t, victim, chain.Unit)
	t.Cleanup(func() { c.CleanupExec(t, victim, chain.RestoreDataScript(backup)) })

	c.MustRun(t, victim, chain.QueryBudget, chain.WipeDataScript(backup))
	c.MustRun(t, victim, chain.QueryBudget, "systemctl start "+chain.Unit)
	c.RequireSameChain(t, c.Nodes(), stateSyncBudget, "after the validator restored a snapshot")
	c.MustRun(t, victim, chain.QueryBudget, chain.DropBackupScript(backup))
	requirePowerKept(t, c, victim, power, "a state sync")
}
