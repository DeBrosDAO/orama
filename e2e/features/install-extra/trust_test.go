//go:build e2e_fleet

package installextra

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

// refusedNothingChanged is what a node's stage-archive says when it refuses
// (core/cmd/orama/internal/production/push/stage.go stageArchive).
const refusedNothingChanged = "nothing under /opt/orama was changed"

// target digests the node the refusals are pushed to, the joined extra (a
// refusal that regresses into an install lands on a node the test removes
// and wipes anyway, never on a core node), with a cleanup that puts the build
// it ran back if a refusal ever turns out not to be one.
func target(t testing.TB, n fleet.Node) infra.StagedState {
	t.Helper()
	f := harness.Fleet(t)
	before := infra.ReadStaged(t, f, n)
	original := string(f.ReadFile(t, n, infra.StagedManifest))
	t.Cleanup(func() { restoreBuild(t, f, n, before, original) })
	return before
}

// restoreBuild re-pushes whichever of the run's archives the node ran if the
// staged build changed: the documented way to put a build on a node.
func restoreBuild(t testing.TB, f *fleet.Fleet, n fleet.Node, before infra.StagedState, original string) {
	ctx, cancel := context.WithTimeout(context.Background(), fleet.CleanupBudget)
	defer cancel()
	out, err := f.SSH(ctx, n).Run(ctx, "sha256sum "+infra.StagedManifest)
	if err == nil && strings.TrimSpace(out.Stdout) == before.Manifest {
		return
	}
	t.Errorf("cleanup: %s's staged build changed during a refusal test; re-pushing the run's build", n.Name)
	for _, archive := range []string{f.State.ArchivePath, f.State.PreviousArchivePath} {
		if archive == "" || string(infra.ReadArchiveFile(t, archive, infra.ManifestName)) != original {
			continue
		}
		res, err := harness.CLI(t).Run(ctx, "push", "--env", f.State.Env, "--node", n.PublicIP, "--archive", archive)
		if err != nil || res.Exit != 0 {
			t.Errorf("cleanup: re-push to %s failed (exit %d): %v %s", n.Name, res.Exit, err, res.Stderr)
		}
		return
	}
	t.Errorf("cleanup: neither of the run's archives is the build %s ran", n.Name)
}

// pushRefused pushes archive to n and requires a refusal naming why, with
// the staged build, its signature and the trust anchor untouched.
func pushRefused(t testing.TB, n fleet.Node, before infra.StagedState, archive string, extra []string, why ...string) {
	t.Helper()
	f := harness.Fleet(t)
	args := append([]string{"node", "push", "--env", f.State.Env, "--node", n.PublicIP, "--archive", archive}, extra...)
	res := infra.RunFor(t, harness.CLI(t), infra.InstallBudget, args...)
	infra.ExpectRefused(t, res, why...)
	if after := infra.ReadStaged(t, f, n); after != before {
		t.Fatalf("%s: a refused push changed /opt/orama or the anchor", n.Name)
	}
	leftover := f.MustExec(t, n, "find /opt/orama -maxdepth 1 \\( -name '.archive-staging-*' -o -name '.archive-cli-*' \\) -print")
	if s := strings.TrimSpace(leftover.Stdout); s != "" {
		t.Errorf("%s: a staging leftover after a refused push:\n%s", n.Name, s)
	}
}

// unsignedArchiveRefused: an archive without manifest.sig is
// refused on the node, whatever else it holds (docs/SECURITY.md
// "Verification": there is no unsigned escape hatch).
func unsignedArchiveRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := target(t, n)
	a := infra.RewriteArchive(t, f.State.ArchivePath, t.TempDir(), "unsigned.tar.gz",
		infra.Edit{Drop: map[string]bool{infra.SignatureName: true}})
	pushRefused(t, n, before, a, nil, "unsigned", refusedNothingChanged)
}

// untrustedSignerRefused: a valid signature by a wallet the
// node does not trust is refused, naming both the signer and who is trusted.
func untrustedSignerRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := target(t, n)
	stranger, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	manifest := infra.ReadArchiveFile(t, f.State.ArchivePath, infra.ManifestName)
	sig, err := infra.SignManifest(manifest, stranger)
	if err != nil {
		t.Fatal(err)
	}
	a := infra.RewriteArchive(t, f.State.ArchivePath, t.TempDir(), "stranger.tar.gz", infra.Edit{
		Replace: map[string]func([]byte) ([]byte, error){infra.SignatureName: func([]byte) ([]byte, error) { return sig, nil }},
	})
	pushRefused(t, n, before, a, nil, "does not trust", strings.ToLower(stranger.Address()), refusedNothingChanged)
}

// tamperedFileRefused: one byte added to a signed file is
// refused: every file must match the signed manifest.
func tamperedFileRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := target(t, n)
	tmpl := infra.FirstTemplate(t, infra.ArchiveManifest(t, f.State.ArchivePath))
	a := infra.RewriteArchive(t, f.State.ArchivePath, t.TempDir(), "tampered.tar.gz", infra.Edit{
		Replace: map[string]func([]byte) ([]byte, error){tmpl: func(b []byte) ([]byte, error) {
			return append(b, []byte("\nExecStartPre=/bin/true\n")...), nil
		}},
	})
	pushRefused(t, n, before, a, nil, "does not match the signed manifest", refusedNothingChanged)
}

// extraFileRefused: a file the manifest does not list is
// refused, even a harmless one.
func extraFileRefused(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := target(t, n)
	a := infra.RewriteArchive(t, f.State.ArchivePath, t.TempDir(), "extra.tar.gz", infra.Edit{
		Add: map[string][]byte{"systemd/e2e-extra@.service": []byte("[Unit]\nDescription=not signed\n")},
	})
	pushRefused(t, n, before, a, nil, "not in its signed manifest", refusedNothingChanged)
}

// trustSignersNeverChangesAnchor: --trust-signers only
// creates a missing anchor; on a node that has one, a different list is
// refused and the anchor is unchanged (docs/CLI_REFERENCE.md "orama node push").
func trustSignersNeverChangesAnchor(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	n := extra.Node
	before := target(t, n)
	other, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	list := strings.ToLower(f.State.OperatorAddress) + "," + strings.ToLower(other.Address())
	pushRefused(t, n, before, f.State.ArchivePath, []string{"--trust-signers", list}, "only creates a missing anchor")
}
