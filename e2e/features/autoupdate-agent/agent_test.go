//go:build e2e_fleet

package autoupdateagent

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tuf"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// repoDir is the repository on the node and repoPort where a static server
	// on the node's loopback serves it: plain HTTP is allowed to a loopback
	// address (core/pkg/releaseverify/repository.go).
	repoDir  = "/root/e2e-release-repo"
	repoPort = 18081
	rootFile = "/root/e2e-release-root.json"
	pidFile  = "/root/e2e-release-repo.pid"
	// noticePath, nodeManifest and previousDir are the node's own paths
	// (core/pkg/updatenotice, core/pkg/install).
	noticePath   = "/etc/orama/update-notice.json"
	adoptedRoot  = "/etc/orama/release-root.json"
	nodeManifest = "/opt/orama/manifest.json"
	// snapshot versions: the first the node accepts, and an older replay.
	firstSnapshot = 7
	olderSnapshot = 2
	uploadBudget  = 5 * time.Minute
	serveBudget   = 2 * time.Minute
	pollEvery     = 2 * time.Second
)

type agent struct {
	f      *fleet.Fleet
	node   fleet.Node
	repo   *tuf.ChannelRepo
	arch   string
	oldVer string
	newVer string
	run    []byte
}

func (a *agent) put(t *testing.T, p string, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), uploadBudget)
	defer cancel()
	a.f.MustExec(t, a.node, "install -d -m 0700 "+path.Dir(p))
	if err := a.f.SSHFor(t, a.node).Put(ctx, p, data, 0o600); err != nil {
		t.Fatalf("upload %s: %v", p, err)
	}
}

// publish replaces the repository on the node with files.
func (a *agent) publish(t *testing.T, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		a.put(t, path.Join(repoDir, name), data)
	}
}

// allowLocalRepo lets the agent fetch from the repository server on the node's
// loopback: a release repository is refused on a loopback or private address
// unless the process asks for it, which only a test does.
const allowLocalRepo = "ORAMA_ALLOW_LOCAL_RELEASE_REPO=1 "

func (a *agent) orama(t *testing.T, args ...string) fleet.Output {
	t.Helper()
	return a.f.Exec(t, a.node, allowLocalRepo+infra.OramaCommand(args...))
}

func (a *agent) manifest(t *testing.T) string {
	t.Helper()
	return a.f.MustExec(t, a.node, "sha256sum "+nodeManifest).Stdout
}

func (a *agent) notice(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(a.f.MustExec(t, a.node, "cat "+noticePath+" 2>/dev/null || true").Stdout)
}

// bump is version with its last dotted number raised by one.
func bump(t *testing.T, version string) string {
	t.Helper()
	i := strings.LastIndex(version, ".")
	n, err := strconv.Atoi(version[i+1:])
	if err != nil {
		t.Fatalf("version %q does not end in a number: %v", version, err)
	}
	return fmt.Sprintf("%s.%d", version[:i], n+1)
}

// serveRepository starts a static server on the node's loopback and waits for
// it; the cleanup stops it.
func (a *agent) serveRepository(t *testing.T) string {
	t.Helper()
	a.f.MustExec(t, a.node, "install -d -m 0755 "+repoDir)
	a.f.MustExec(t, a.node, fmt.Sprintf(
		"nohup python3 -m http.server %d --bind 127.0.0.1 --directory %s >/root/e2e-release-repo.log 2>&1 & echo $! > %s",
		repoPort, repoDir, pidFile))
	t.Cleanup(func() { a.f.Exec(t, a.node, "kill $(cat "+pidFile+") 2>/dev/null; true") })
	url := fmt.Sprintf("http://127.0.0.1:%d", repoPort)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), serveBudget)
	defer cancel()
	err := eventually.Poll(ctx, pollEvery, serveBudget, "the repository server on the node to answer", func() (bool, error) {
		return a.f.Exec(t, a.node, "curl -s -o /dev/null http://127.0.0.1:"+strconv.Itoa(repoPort)+"/").Exit == 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// TestAutoupdateRun_followsItsPolicyAndRefusesWhatDoesNotVerify drives one
// eval cluster's only node through the agent's decisions, in order.
func TestAutoupdateRun_followsItsPolicyAndRefusesWhatDoesNotVerify(t *testing.T) {
	f := harness.Fleet(t)
	cl := harness.ExtraCluster(t, "autoupd")
	cli := harness.CLI(t).Isolated(t)
	cli.MustOK(t, "network", "use", cl.Env)

	runArchive, err := os.ReadFile(infra.RunningArchive(t, f))
	if err != nil {
		t.Fatal(err)
	}
	a := &agent{f: f, node: cl.Node, repo: tuf.NewChannelRepo(t), run: runArchive}
	a.arch = tuf.ManifestArch(t, runArchive)
	a.oldVer = tuf.ManifestVersion(t, runArchive)
	a.newVer = bump(t, a.oldVer)
	release := tuf.ReleaseArchive(t, runArchive, a.newVer)
	archives := map[string][]byte{a.newVer: release}
	targetFile := "targets/stable/orama-" + a.newVer + "-linux-" + a.arch + ".tar.gz"
	repoURL := a.serveRepository(t)
	a.publish(t, a.repo.Files(t, firstSnapshot, time.Time{}, a.arch, archives))

	t.Run("doesNothingUntilTheClusterNamesARepository", func(t *testing.T) {
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "no release repository")
		if n := a.notice(t); n != "" {
			t.Fatalf("a notice with nothing to report: %s", n)
		}
	})
	t.Run("doesNothingUntilTheNodeAdoptsARoot", func(t *testing.T) {
		cli.MustOK(t, "maint", "cluster", "settings", "set", "release-repo", repoURL)
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "no release root")
	})
	t.Run("notifyReportsANewerReleaseAndInstallsNothing", func(t *testing.T) {
		a.put(t, rootFile, a.repo.Root())
		out := infra.OnNode(t, a.f, a.node, "node", "trust", "add-root", rootFile)
		infra.ExpectNodeExit(t, "add-root", out, infra.ExitOK, a.repo.Digest())
		before := a.manifest(t)
		out = a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "notify:", a.newVer)
		if n := a.notice(t); !strings.Contains(n, `"state":"available"`) || !strings.Contains(n, a.newVer) {
			t.Fatalf("notice %q", n)
		}
		if after := a.manifest(t); after != before {
			t.Fatal("notify changed the installed release")
		}
	})
	t.Run("aTamperedArchiveIsRefusedAndReported", func(t *testing.T) {
		cli.MustOK(t, "maint", "cluster", "settings", "set", "auto-update", "auto")
		a.put(t, path.Join(repoDir, targetFile), tuf.Flipped(release))
		before := a.manifest(t)
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "refuse:", "did not verify")
		if n := a.notice(t); !strings.Contains(n, `"state":"refused"`) {
			t.Fatalf("notice %q", n)
		}
		if after := a.manifest(t); after != before {
			t.Fatal("a tampered archive changed the installed release")
		}
		a.put(t, path.Join(repoDir, targetFile), release)
	})
	t.Run("aFrozenTimestampIsRefused", func(t *testing.T) {
		a.publish(t, a.repo.Files(t, firstSnapshot+1, time.Now().Add(-time.Hour), a.arch, archives))
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "refuse:", "frozen")
		a.publish(t, a.repo.Files(t, firstSnapshot, time.Time{}, a.arch, archives))
	})
	t.Run("aSoleVoterIsPutBackAndTheReleaseIsNotMarkedBad", func(t *testing.T) {
		before := a.manifest(t)
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitFailure, "back on its previous release")
		if after := a.manifest(t); after != before {
			t.Fatalf("a refused upgrade left a different release in place:\n%s\n%s", before, after)
		}
		if n := a.notice(t); strings.Contains(n, `"state":"failed"`) {
			t.Fatalf("a failure before anything stopped was reported as the release's: %s", n)
		}
		again := a.orama(t, "maint", "node", "autoupdate", "run")
		if strings.Contains(again.Stdout+again.Stderr, "marked bad") {
			t.Fatal("the release was marked bad by a failure that stopped nothing")
		}
		infra.ExpectNodeExit(t, "orama node status", infra.OnNode(t, a.f, a.node, "node", "status"), infra.ExitOK)
	})
	t.Run("anOlderSnapshotIsRefused", func(t *testing.T) {
		a.publish(t, a.repo.Files(t, olderSnapshot, time.Time{}, a.arch, archives))
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "refuse:", "rolled-back")
	})
	t.Run("followsARootRotationTheRepositoryPublished", func(t *testing.T) {
		cli.MustOK(t, "cluster", "settings", "set", "auto-update", "notify")
		name, rotated := a.repo.Rotate(t)
		a.put(t, path.Join(repoDir, name), rotated)
		a.publish(t, a.repo.Files(t, firstSnapshot+3, time.Time{}, a.arch, archives))
		out := a.orama(t, "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "notify:", a.newVer)
		adopted := a.f.MustExec(t, a.node, "sha256sum "+adoptedRoot).Stdout
		if !strings.HasPrefix(adopted, a.repo.Digest()) {
			t.Fatalf("the node did not adopt the rotated root %s: %s", a.repo.Digest(), adopted)
		}
	})
	t.Run("readsTheNightlyChannelByItsPathPrefix", func(t *testing.T) {
		cli.MustOK(t, "cluster", "settings", "set", "update-channel", "nightly")
		a.publish(t, a.repo.FilesOn(t, "nightly", firstSnapshot+4, time.Time{}, a.arch, archives))
		out := a.orama(t, "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "notify:", a.newVer)
		cli.MustOK(t, "cluster", "settings", "set", "update-channel", "main")
		out = a.orama(t, "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK)
		if strings.Contains(out.Stdout, a.newVer) {
			t.Fatalf("the main channel offered a release listed only under nightly/:\n%s", out.Stdout)
		}
	})
	t.Run("offStopsLookingAndClearsTheNotice", func(t *testing.T) {
		cli.MustOK(t, "maint", "cluster", "settings", "set", "auto-update", "off")
		out := a.orama(t, "maint", "node", "autoupdate", "run")
		infra.ExpectNodeExit(t, "autoupdate run", out, infra.ExitOK, "auto-update is off")
		if n := a.notice(t); n != "" {
			t.Fatalf("a notice after updates were turned off: %s", n)
		}
	})
}
