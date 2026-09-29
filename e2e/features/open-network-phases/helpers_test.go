//go:build e2e_fleet

package opennetworkphases

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	planIndex = "plans/open-network/INDEX.md"
	pollEvery = 5 * time.Second
	exitOK    = infra.ExitOK
	exitUsage = infra.ExitUsage
)

// phase makes a test apply only when the checkout's doc documents what the
// phase delivered: the plan's status lines were never updated, and the docs
// describe the code that ships (CLAUDE.md "Code is the source of truth").
// A phase the docs do not document is not applicable, naming its plan
// section; a documented one is asserted, and fails when it is absent.
func phase(t *testing.T, id, doc, fragment, planSection string) {
	t.Helper()
	if !strings.Contains(cliconf.ReadRepoFile(t, doc), fragment) {
		harness.SkipNotApplicable(t, "phase "+id+" is not delivered in this checkout: "+doc+" does not document "+
			fragment+" (plan: "+planSection+", "+planIndex+")")
	}
	t.Logf("phase %s: delivered per %s (%s)", id, doc, fragment)
}

// freshCLI is the CLI on a machine that has never been configured: a HOME
// of its own with no environment list at all (not even the run's).
func freshCLI(t *testing.T) *oramacli.Runner {
	t.Helper()
	c := *harness.CLI(t)
	c.Home = t.TempDir()
	return &c
}

// run is the CLI under test, failing only when it could not run.
func run(t *testing.T, cli *oramacli.Runner, args ...string) oramacli.Result {
	t.Helper()
	return infra.Run(t, cli, args...)
}

// out is a result's combined output.
func out(res oramacli.Result) string { return res.Stdout + res.Stderr }

// onNode runs `orama <args>` as root on n.
func onNode(t *testing.T, f *fleet.Fleet, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	return infra.OnNode(t, f, n, args...)
}

// readLocal reads a file on the runner.
func readLocal(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return raw
}

// cleanupPath removes a path the test created on n, from a cleanup.
func cleanupPath(t *testing.T, f *fleet.Fleet, n fleet.Node, path string) {
	edge.RunInCleanup(t, f, n, "rm -rf "+fleet.ShellQuote(path)+" && ! test -e "+fleet.ShellQuote(path))
}
