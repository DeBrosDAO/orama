//go:build e2e_fleet

package releaseinstall

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// releaseVersion is the version the test publishes the run's build as.
	releaseVersion = "9.0.1"
	// nodeRoot is the adopted release root (core/pkg/releaseverify.RootPath).
	nodeRoot = "/etc/orama/release-root.json"
	// otherRootPath is where the test uploads a second root.
	otherRootPath = "/root/e2e-other-root.json"
	// acceptedSnapshot and olderSnapshot are the snapshot versions of the
	// release the server joins from and of a replay of an older repository.
	acceptedSnapshot = 6
	olderSnapshot    = 3
	uploadBudget     = 2 * time.Minute
)

// allowLocalRepo is releaseverify.AllowLocalEnv: a release repository on a
// loopback or private address is refused unless the process asks for it.
const allowLocalRepo = "ORAMA_ALLOW_LOCAL_RELEASE_REPO=1"

// repository serves a release repository from this machine's loopback, where
// `orama node setup` runs.
type repository struct {
	dir string
	url string
}

func serve(t *testing.T) *repository {
	t.Helper()
	dir := t.TempDir()
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	return &repository{dir: dir, url: srv.URL}
}

func (r *repository) publish(t *testing.T, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		path := filepath.Join(r.dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// setupRelease is `orama node setup` joining extra from the release, with the
// run's other flags (SetupArgs) and the three release flags in place of
// --archive.
func setupRelease(t *testing.T, extra harness.Extra, repo *repository, rootFile string) []string {
	t.Helper()
	f := harness.Fleet(t)
	args := infra.SetupArgs(t, extra, infra.RunningArchive(t, f))
	for i, a := range args {
		if a == "--archive" {
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	return append(args, "--release", releaseVersion, "--release-repo", repo.url, "--release-root", rootFile)
}

func writeRoot(t *testing.T, root []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "root.json")
	if err := os.WriteFile(path, root, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func untouched(t *testing.T, f *fleet.Fleet, extra harness.Extra) {
	t.Helper()
	if f.Exec(t, extra.Node, "test -e /opt/orama").Exit == 0 {
		t.Fatal("a refused release put /opt/orama on the server")
	}
}

// TestSetupRelease_joinsFromAVerifiedReleaseAndRefusesTheRest: the CLI is the
// release client. Each refusal happens on this machine, before an archive is
// uploaded; the good release joins the server as a full member, and the node
// adopts the root the operator named.
func TestSetupRelease_joinsFromAVerifiedReleaseAndRefusesTheRest(t *testing.T) {
	f := harness.Fleet(t)
	infra.RequireHealthy(t)
	cli := harness.CLI(t)
	// The repository is served from this machine's loopback, which the CLI
	// refuses as a release repository unless a test asks.
	cli.Env = append(cli.Env, allowLocalRepo)
	extra := infra.NewExtra(t, "extra-release")
	t.Cleanup(func() { infra.RemoveMembers(t, f, cli, extra.PublicIP) })

	run, err := os.ReadFile(infra.RunningArchive(t, f))
	if err != nil {
		t.Fatal(err)
	}
	arch := tuf.ManifestArch(t, run)
	release := tuf.ReleaseArchive(t, run, releaseVersion)
	channel := tuf.NewChannelRepo(t)
	rootFile := writeRoot(t, channel.Root())
	repo := serve(t)
	archives := map[string][]byte{releaseVersion: release}
	targetFile := "targets/stable/orama-" + releaseVersion + "-linux-" + arch + ".tar.gz"

	t.Run("tamperedArchiveRefused", func(t *testing.T) {
		files := channel.Files(t, acceptedSnapshot, time.Time{}, arch, archives)
		files[targetFile] = tuf.Flipped(release)
		repo.publish(t, files)
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, rootFile)...)
		infra.ExpectRefused(t, res, tuf.ErrTargetHash)
		untouched(t, f, extra)
	})
	t.Run("frozenTimestampRefused", func(t *testing.T) {
		repo.publish(t, channel.Files(t, acceptedSnapshot, time.Now().Add(-time.Hour), arch, archives))
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, rootFile)...)
		infra.ExpectRefused(t, res, tuf.ErrFreeze)
		untouched(t, f, extra)
	})
	t.Run("metadataUnderAnotherRootRefused", func(t *testing.T) {
		repo.publish(t, channel.Files(t, acceptedSnapshot, time.Time{}, arch, archives))
		other := writeRoot(t, tuf.NewChannelRepo(t).Root())
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, other)...)
		infra.ExpectRefused(t, res, tuf.ErrThreshold)
		untouched(t, f, extra)
	})
	t.Run("aVersionTheChannelDoesNotListRefused", func(t *testing.T) {
		repo.publish(t, channel.Files(t, acceptedSnapshot, time.Time{}, arch, map[string][]byte{"9.0.2": release}))
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, rootFile)...)
		infra.ExpectRefused(t, res, "does not name")
		untouched(t, f, extra)
	})
	t.Run("goodReleaseJoins", func(t *testing.T) {
		repo.publish(t, channel.Files(t, acceptedSnapshot, time.Time{}, arch, archives))
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, rootFile)...)
		infra.ExpectExit(t, res, infra.ExitOK, "setup complete")
		r := infra.WaitConverged(t, len(f.State.Nodes)+1, infra.ConvergeBudget, extra.Name+" to join as a full member")
		if _, err := infra.ReportFor(r, extra.Node); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("nodeAdoptedTheRoot", func(t *testing.T) {
		if got := f.ReadFile(t, extra.Node, nodeRoot); !bytes.Equal(got, channel.Root()) {
			t.Fatalf("the node's release root is not the one the operator named (sha256 %s)", channel.Digest())
		}
		manifest := f.ReadFile(t, extra.Node, infra.StagedManifest)
		if !strings.Contains(string(manifest), `"release_root"`) {
			t.Error("the installed manifest does not carry the release root")
		}
		if sig := f.ReadFile(t, extra.Node, infra.StagedSignature); len(sig) == 0 {
			t.Error("the installed release is not signed by the operator's wallet")
		}
	})
	t.Run("olderSnapshotRefused", func(t *testing.T) {
		repo.publish(t, channel.Files(t, olderSnapshot, time.Time{}, arch, archives))
		res := infra.RunFor(t, cli, infra.InstallBudget, setupRelease(t, extra, repo, rootFile)...)
		infra.ExpectRefused(t, res, tuf.ErrRollback)
	})
	t.Run("addRootNeedsReplaceAndAVerifiedRoot", func(t *testing.T) {
		other := tuf.NewChannelRepo(t)
		put(t, f, extra, otherRootPath, other.Root())
		put(t, f, extra, otherRootPath+".bad", []byte(`{"signed":{}}`))

		out := infra.OnNode(t, f, extra.Node, "node", "trust", "add-root", otherRootPath)
		infra.ExpectNodeExit(t, "add-root without --replace", out, infra.ExitUsage, "--replace", channel.Digest())
		if got := f.ReadFile(t, extra.Node, nodeRoot); !bytes.Equal(got, channel.Root()) {
			t.Fatal("a different root replaced the adopted one without --replace")
		}
		out = infra.OnNode(t, f, extra.Node, "node", "trust", "add-root", otherRootPath+".bad", "--replace")
		infra.ExpectNodeExit(t, "add-root of a root that does not verify", out, infra.ExitUsage)
		if got := f.ReadFile(t, extra.Node, nodeRoot); !bytes.Equal(got, channel.Root()) {
			t.Fatal("a root that does not verify replaced the adopted one")
		}
		out = infra.OnNode(t, f, extra.Node, "node", "trust", "add-root", otherRootPath, "--replace")
		infra.ExpectNodeExit(t, "add-root --replace", out, infra.ExitOK, other.Digest())
		if got := f.ReadFile(t, extra.Node, nodeRoot); !bytes.Equal(got, other.Root()) {
			t.Fatal("--replace did not adopt the new root")
		}
	})
}

func put(t *testing.T, f *fleet.Fleet, extra harness.Extra, path string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), uploadBudget)
	defer cancel()
	if err := f.SSHFor(t, extra.Node).Put(ctx, path, data, 0o600); err != nil {
		t.Fatalf("upload %s: %v", path, err)
	}
}
