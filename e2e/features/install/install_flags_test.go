//go:build e2e_fleet

package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestInstallFlags_localInstallNeedsRoot: without root and without --remote
// the install is refused as a usage error that points at --remote, instead of
// being reinterpreted as a remote install (production/install/command.go).
func TestInstallFlags_localInstallNeedsRoot(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[1]
	out := f.Exec(t, n, "runuser -u orama -- "+infra.OramaCommand("maint", "node", "install", "--vps-ip", n.PublicIP,
		"--base-domain", f.State.BaseDomain, "--operator-wallet", f.State.OperatorAddress))
	if out.Exit != infra.ExitUsage || !strings.Contains(out.Stdout+out.Stderr, "--remote") {
		t.Fatalf("a non-root local install: exit %d, want %d pointing at --remote:\n%s%s", out.Exit, infra.ExitUsage, out.Stdout, out.Stderr)
	}
}
