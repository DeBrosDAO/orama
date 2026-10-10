//go:build e2e_fleet

package clinodeopsreadonly

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestMonitor_badArgumentsAreUsage: flag mistakes are usage errors (exit 2)
// found before any gateway is asked (cmd/monitorcmd, monitor/source.go
// NewSource and ResolveInterval).
func TestMonitor_badArgumentsAreUsage(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	env := f.State.Env
	cases := []struct {
		label string
		args  []string
		want  string
	}{
		{"unknown env", []string{"status", "alerts", "--env", "e2e-cli-absent"}, "cannot find the gateway"},
		{"config without ssh", []string{"status", "node", "--env", env, "--config", "/dev/null"}, "only applies with --ssh"},
		{"interval too short", []string{"status", "--env", env, "--interval", "1s"}, "--interval must be between"},
		{"interval too long", []string{"status", "--env", env, "--interval", "2m"}, "--interval must be between"},
		{"ssh interval too short", []string{"status", "--env", env, "--ssh", "--interval", "5s"}, "at least"},
		{"interval not a duration", []string{"status", "--env", env, "--interval", "soon"}, "interval"},
	}
	cli := harness.CLI(t)
	// With no --env, status reads the active environment; a CLI with none is refused.
	if res := run(t, cli.Isolated(t), "status", "alerts"); res.Exit != exitUsage || !strings.Contains(output(res), "active environment") {
		t.Errorf("no env and no active environment: exit %d\n%s", res.Exit, output(res))
	}
	for _, c := range cases {
		res := run(t, cli, c.args...)
		if res.Exit != exitUsage || !strings.Contains(output(res), c.want) {
			t.Errorf("%s (orama %v): exit %d, want %d and %q\n%s", c.label, c.args, res.Exit, exitUsage, c.want, output(res))
		}
	}
}

// TestMonitor_noCredentialIsAuthError: telemetry needs the operator's
// session; a HOME without one is told to sign in (monitor/token.go tokenError).
func TestMonitor_noCredentialIsAuthError(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t).Isolated(t)
	for _, v := range oneShotViews {
		res := run(t, cli, "status", v, "--env", f.State.Env)
		if res.Exit != exitAuth || !strings.Contains(output(res), "orama auth login") {
			t.Errorf("monitor %s with no credential: exit %d, want %d\n%s", v, res.Exit, exitAuth, output(res))
		}
	}
}

// TestMonitor_sshBreakGlassReadsEveryNode: --ssh reads `orama node report`
// on every node instead of the gateway API, and is never chosen by itself
// (docs/CLI_REFERENCE.md#orama-monitor).
func TestMonitor_sshBreakGlassReadsEveryNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	var nodes []nodeEntry
	decode(t, run(t, harness.CLI(t), "status", "node", "--env", f.State.Env, "--ssh", "--json"), &nodes)
	if len(nodes) != len(f.State.Nodes) {
		t.Fatalf("monitor node --ssh reports %d nodes, want %d", len(nodes), len(f.State.Nodes))
	}
	for _, n := range nodes {
		if _, ok := f.Lookup(n.Host); !ok || n.Report == nil || n.Error != "" {
			t.Errorf("--ssh node %s: report present %v, error %q", n.Host, n.Report != nil, n.Error)
		}
	}
}

// unknownViewBudget bounds `orama monitor <typo>`: the bug it guards against
// is the live view opening instead of a refusal, and the live view never ends.
const unknownViewBudget = time.Minute

// TestMonitor_unknownViewIsUsage: a mistyped view is an unknown subcommand,
// a usage error naming it (core/cmd/orama/root.go classifyUsageErrors), not
// the live view with the typo ignored.
func TestMonitor_unknownViewIsUsage(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	res := infra.RunFor(t, harness.CLI(t), unknownViewBudget, "status", "e2e-no-such-view", "--env", f.State.Env)
	if res.Exit != exitUsage || !strings.Contains(output(res), "e2e-no-such-view") {
		t.Errorf("monitor e2e-no-such-view: exit %d, want %d naming it\n%s", res.Exit, exitUsage, output(res))
	}
}
