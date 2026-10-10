//go:build e2e_fleet

package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// releaseRootPath is the adopted TUF release root
// (core/pkg/releaseverify/file.go RootPath).
const releaseRootPath = "/etc/orama/release-root.json"

// TestStageArchive_usageRefusals: the node-side step refuses a command line
// that cannot work before it verifies anything: no --archive, and half of
// the release-root flags, which must never fall back to the wallet path
// (docs/CLI_REFERENCE.md "orama maint node stage-archive").
func TestStageArchive_usageRefusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	res := infra.OnNode(t, f, n, "maint", "node", "stage-archive")
	if res.Exit != infra.ExitUsage {
		t.Errorf("stage-archive without --archive: exit %d, want %d: %s", res.Exit, infra.ExitUsage, res.Stderr)
	}
	dummy := "/tmp/e2e-stage-" + f.State.RunID + ".tar.gz"
	f.WriteFile(t, n, dummy, []byte("not an archive"), 0o600)
	for _, half := range [][]string{{"--release-target", "orama.tar.gz"}, {"--release-metadata", "/tmp"}} {
		args := append([]string{"maint", "node", "stage-archive", "--archive", dummy}, half...)
		res := infra.OnNode(t, f, n, args...)
		if res.Exit != infra.ExitUsage || !strings.Contains(res.Stdout+res.Stderr, "go together") {
			t.Errorf("stage-archive %v: exit %d, want %d naming both flags: %s%s", half, res.Exit, infra.ExitUsage, res.Stdout, res.Stderr)
		}
	}
}

// TestStageArchive_releaseRootRequiredWhenAsked: asking for the TUF release
// check on a node that adopted no release root refuses the archive before
// extracting it, and is never retried on the wallet path (docs/SECURITY.md
// "Verification").
func TestStageArchive_releaseRootRequiredWhenAsked(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	if f.Exec(t, n, "test -e "+releaseRootPath).Exit == 0 {
		harness.SkipNotApplicable(t, n.Name+" has adopted a release root; this case needs a node without one")
	}
	dummy := "/tmp/e2e-tuf-" + f.State.RunID + ".tar.gz"
	f.WriteFile(t, n, dummy, []byte("not an archive"), 0o600)
	before := infra.ReadStaged(t, f, n)
	res := infra.OnNode(t, f, n, "maint", "node", "stage-archive", "--archive", dummy,
		"--release-metadata", "/tmp", "--release-target", "orama.tar.gz")
	if res.Exit == infra.ExitOK || !strings.Contains(res.Stdout+res.Stderr, releaseRootPath) {
		t.Fatalf("stage-archive with no adopted root: exit %d, want a refusal naming %s:\n%s%s",
			res.Exit, releaseRootPath, res.Stdout, res.Stderr)
	}
	if after := infra.ReadStaged(t, f, n); after != before {
		t.Fatal("a refused stage changed /opt/orama")
	}
}

// TestStageArchive_notRootRefused: staging needs root; the orama user, which
// runs every daemon, cannot put a build in place.
func TestStageArchive_notRootRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	res := f.Exec(t, n, "runuser -u orama -- "+infra.OramaCommand("maint", "node", "stage-archive", "--archive", "/tmp/x.tar.gz"))
	if res.Exit == infra.ExitOK {
		t.Fatalf("the orama user staged an archive:\n%s", res.Stdout)
	}
}
