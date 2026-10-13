//go:build e2e_fleet

package releaseupgrade

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Output of `orama upgrade` (core/cmd/orama/internal/production/relupgrade/plan.go,
// core/cmd/orama/internal/production/upgrade/remote.go).
const (
	noRegistryText = "not on a network of the registry"
	rollOwnBuild   = "orama maint rollout --env "
	nothingToDo    = "nothing to do"
	completeText   = "Rolling upgrade complete"
)

var (
	releaseLine   = regexp.MustCompile(`newest release (\S+)`)
	upgradingLine = regexp.MustCompile(`Upgrading (\S+) \(`)
)

// registryNetwork is the registry network the run's environment is on ("" when
// the environment is a bare gateway, as a provisioned fleet's is).
func registryNetwork(t testing.TB) string {
	t.Helper()
	res := harness.CLI(t).MustOK(t, "network", "current", "--json")
	var env struct {
		Network string `json:"network"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("`orama network current --json` is not JSON: %v\n%s", err, res.Stdout)
	}
	return env.Network
}

// requireChannel skips unless the environment is on a registry network, whose
// release channel `orama upgrade` reads.
func requireChannel(t testing.TB) {
	t.Helper()
	if registryNetwork(t) == "" {
		harness.SkipNotApplicable(t, "the run's environment is a bare gateway, on no registry network and so with no release channel; run against the stagenet target, or add the network from its manifest")
	}
}

// starts records every core node's orama-node start time: a node that was not
// restarted keeps it.
func starts(t testing.TB, f *fleet.Fleet) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range f.State.Nodes {
		out[n.Name] = strings.TrimSpace(f.MustExec(t, n, "systemctl show -p ActiveEnterTimestampMonotonic --value "+infra.NodeUnit).Stdout)
	}
	return out
}

func sameStarts(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestUpgrade_anEnvironmentOnNoRegistryNetworkIsRefused: a bare gateway has no
// channel to fetch from; the refusal is a usage error that names how to roll a
// build of your own, and nothing is restarted.
func TestUpgrade_anEnvironmentOnNoRegistryNetworkIsRefused(t *testing.T) {
	f := harness.Fleet(t)
	if registryNetwork(t) != "" {
		harness.SkipNotApplicable(t, "the run's environment is on a registry network; the refusal is for a bare gateway, as a provisioned fleet's is")
	}
	before := starts(t, f)

	res := infra.Run(t, harness.CLI(t), "upgrade", "--env", f.State.Env, "--dry-run")

	infra.ExpectExit(t, res, infra.ExitUsage, noRegistryText, rollOwnBuild+f.State.Env)
	if after := starts(t, f); !sameStarts(before, after) {
		t.Errorf("a refused upgrade restarted a node: %v -> %v", before, after)
	}
}

// TestUpgrade_dryRunPrintsThePlanAndChangesNothing: --dry-run fetches and
// verifies the release, lists every node with what it runs, the release and its
// action, and stages and restarts nothing.
func TestUpgrade_dryRunPrintsThePlanAndChangesNothing(t *testing.T) {
	f := harness.Fleet(t)
	requireChannel(t)
	infra.RequireHealthy(t)
	before := starts(t, f)

	res := infra.Run(t, harness.CLI(t), "upgrade", "--env", f.State.Env, "--dry-run")

	infra.ExpectExit(t, res, infra.ExitOK, "newest release", "ACTION")
	for _, n := range f.State.Nodes {
		if !strings.Contains(res.Stdout, n.PublicIP) {
			t.Errorf("the plan does not list %s:\n%s", n.PublicIP, res.Stdout)
		}
	}
	if after := starts(t, f); !sameStarts(before, after) {
		t.Errorf("a dry run restarted a node: %v -> %v", before, after)
	}
}

// TestUpgrade_aNodeOutsideTheNetworkIsRefused: --node names a node of the
// environment, and a typo stops the command before any release is staged.
func TestUpgrade_aNodeOutsideTheNetworkIsRefused(t *testing.T) {
	f := harness.Fleet(t)
	requireChannel(t)

	res := infra.Run(t, harness.CLI(t), "upgrade", "--env", f.State.Env, "--node", "192.0.2.1", "--dry-run")

	infra.ExpectRefused(t, res, "192.0.2.1", "not found")
}

// TestUpgrade_rollingToTheSameReleaseKeepsTheClusterConverged: --reinstall --yes
// puts the channel's release on every node, one at a time with the raft leader
// last, the cluster ends converged with every node on that release, and the next
// plain run finds every node current and restarts nothing.
func TestUpgrade_rollingToTheSameReleaseKeepsTheClusterConverged(t *testing.T) {
	f := harness.Fleet(t)
	requireChannel(t)
	r := infra.RequireHealthy(t)
	leader := infra.Leader(t, r)

	res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "upgrade", "--env", f.State.Env, "--reinstall", "--yes")

	infra.ExpectExit(t, res, infra.ExitOK, completeText)
	order := upgradingLine.FindAllStringSubmatch(res.Stdout, -1)
	if len(order) != len(f.State.Nodes) {
		t.Fatalf("%d nodes were upgraded, want %d:\n%s", len(order), len(f.State.Nodes), res.Stdout)
	}
	if last := order[len(order)-1][1]; last != leader.PublicIP {
		t.Errorf("the raft leader %s was not upgraded last (last: %s):\n%s", leader.PublicIP, last, res.Stdout)
	}
	infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster after the rolling upgrade")
	m := releaseLine.FindStringSubmatch(res.Stdout)
	if m == nil {
		t.Fatalf("the plan does not name the release:\n%s", res.Stdout)
	}
	for _, n := range f.State.Nodes {
		if out := infra.OnNode(t, f, n, "version"); !strings.Contains(out.Stdout, m[1]) {
			t.Errorf("%s runs %q, want release %s", n.Name, strings.TrimSpace(out.Stdout), m[1])
		}
	}

	before := starts(t, f)
	again := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "upgrade", "--env", f.State.Env, "--yes")
	infra.ExpectExit(t, again, infra.ExitOK, nothingToDo)
	if after := starts(t, f); !sameStarts(before, after) {
		t.Errorf("an upgrade with nothing to do restarted a node: %v -> %v", before, after)
	}
}
