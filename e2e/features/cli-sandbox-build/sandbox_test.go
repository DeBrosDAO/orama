//go:build e2e_fleet

package clisandboxbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Nothing in this package may create, change or destroy a sandbox: that
// needs Hetzner credentials and money. The CLI's environment is an allowlist
// (no HCLOUD_TOKEN reaches it) and every command that is executed rather than
// asked for --help runs in a HOME with no sandbox configuration at all.
// create, setup, reset, destroy and rollout are only ever asked for help.

// bareHome is a machine with nothing configured: no environment, no
// credential, no sandbox, no wallet. Its HOME is also the working directory,
// outside any checkout.
func bareHome(t testing.TB) *oramacli.Runner {
	t.Helper()
	r := *harness.CLI(t).NoWallet(t)
	r.Home = t.TempDir()
	r.AgentSock = filepath.Join(r.Home, "no-agent.sock")
	return &r
}

// TestConformance_sandboxAndBuildHelp runs the generic checks (help matches
// docs/CLI_REFERENCE.md, --json accepted, unknown flags and subcommands are
// usage errors, `sandbox ssh` takes exactly one node number) on every
// `orama sandbox` command and `orama build`. None of them runs.
func TestConformance_sandboxAndBuildHelp(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, bareHome(t), cliconf.LoadReference(t), "orama sandbox", "orama build")
}

// TestSandbox_unconfiguredMachineRefuses: with no sandbox configured, list
// says there is none and status and ssh point at `orama sandbox setup`
// instead of reaching for a cloud (docs/SANDBOX.md).
func TestSandbox_unconfiguredMachineRefuses(t *testing.T) {
	t.Parallel()
	cli := bareHome(t)
	list := cli.MustOK(t, "sandbox", "list")
	if !strings.Contains(list.Stdout, "No sandboxes found") {
		t.Errorf("sandbox list on a bare machine:\n%s", list.Stdout)
	}
	for _, args := range [][]string{{"sandbox", "status"}, {"sandbox", "ssh", "1"}} {
		infra.ExpectRefused(t, infra.Run(t, cli, args...), "orama sandbox setup")
	}
	entries, err := os.ReadDir(filepath.Join(cli.Home, ".orama", "sandboxes"))
	if err == nil && len(entries) > 0 {
		t.Errorf("read-only sandbox commands wrote %d files under ~/.orama/sandboxes", len(entries))
	}
}
