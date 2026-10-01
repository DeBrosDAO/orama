//go:build e2e_fleet

package install

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// The pushes that reach a node's stage-archive are in install-extra, against
// the joined extra: a refusal that regressed would install on the node.

// TestArchiveTrust_pushUsageRefusals: the refusals the runner makes before
// it touches a node: no --archive, an archive that does not exist, a
// --trust-signers value that is no address, neither --env nor --host.
func TestArchiveTrust_pushUsageRefusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	harness.RequireArchive(t, f.State.ArchivePath)
	cli := harness.CLI(t)
	infra.ExpectRefused(t, infra.Run(t, cli, "push", "--env", f.State.Env), "--archive is required")
	infra.ExpectRefused(t, infra.Run(t, cli, "push", "--env", f.State.Env, "--archive", "/nonexistent/orama.tar.gz"),
		"/nonexistent/orama.tar.gz")
	infra.ExpectRefused(t, infra.Run(t, cli, "push", "--env", f.State.Env, "--archive", f.State.ArchivePath,
		"--trust-signers", "not-an-address"), "not-an-address")
	infra.ExpectRefused(t, infra.Run(t, cli, "push", "--env", f.State.Env, "--node", "192.0.2.1",
		"--archive", f.State.ArchivePath), "192.0.2.1")
}
