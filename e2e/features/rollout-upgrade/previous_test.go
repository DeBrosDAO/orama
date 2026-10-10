//go:build e2e_fleet

package rolloutupgrade

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
)

const (
	// trafficEvery paces the live traffic kept up during the upgrade.
	trafficEvery = time.Second
	// maxFailedShare is the share of live requests an upgrade may fail: one
	// node of three restarts at a time behind a public name that resolves to
	// all three, so a request that lands on the restarting node can fail; a
	// third of all requests failing would mean more than one node was down.
	maxFailedShare = 0.10
	// trafficBudget bounds the live traffic: the mixed-version window and the
	// rolling upgrade, each within an upgrade budget.
	trafficBudget = 2 * infra.UpgradeBudget
)

type testLogger struct{ t testing.TB }

func (l testLogger) Infof(format string, args ...any) { l.t.Logf(format, args...) }

// traffic keeps GETs going to a namespace gateway until stopped, one worker
// per core node on a client pinned to it (a connection of its own each, so
// every node's restart is seen, not only the one a shared keep-alive
// connection happened to land on), counting what failed.
type traffic struct {
	sent, failed atomic.Int64
	// cutShort is set when a worker stopped before end: its wait timed out.
	cutShort atomic.Bool
	stop     context.CancelFunc
	done     sync.WaitGroup
}

func startTraffic(t testing.TB, f *fleet.Fleet, c *gw.Client) *traffic {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &traffic{stop: cancel}
	for _, node := range f.State.Nodes {
		tr.done.Add(1)
		go tr.worker(ctx, c.PinTo(node.PublicIP), node.Name)
	}
	t.Cleanup(tr.end)
	return tr
}

// worker sends a request every trafficEvery until ctx ends. A request cut by
// the end itself is not counted.
func (tr *traffic) worker(ctx context.Context, c *gw.Client, name string) {
	defer tr.done.Done()
	_ = eventually.Poll(ctx, trafficEvery, trafficBudget, "live traffic to "+name, func() (bool, error) {
		r, err := c.Send(ctx, gw.Req{Path: "/v1/health"})
		if ctx.Err() != nil {
			return false, nil
		}
		tr.sent.Add(1)
		if err != nil || r.Status != http.StatusOK {
			tr.failed.Add(1)
		}
		return false, nil
	})
	if ctx.Err() == nil {
		tr.cutShort.Store(true)
	}
}

func (tr *traffic) end() { tr.stop(); tr.done.Wait() }

// TestUpgrade_previousReleaseToHeadUnderTraffic: a fleet installed with the
// previous release is rolled to HEAD the documented way (push, then one node
// at a time, followers first, leader last, each gated on health) while a
// namespace keeps serving. Afterwards every node runs HEAD, every node kept
// its raft id and mesh address, the namespace created before still serves,
// and the previous release's CLI still reads the upgraded cluster
// (website/src/docs/contributor/testing.mdx rolling upgrades; e2e/README.md "the upgrade stage").
func TestUpgrade_previousReleaseToHeadUnderTraffic(t *testing.T) {
	f := harness.Fleet(t)
	if f.State.PreviousArchivePath == "" || f.State.PreviousOramaBin == "" {
		harness.SkipNotApplicable(t, "the fleet was not installed from the previous release: run with E2E_INSTALL_PREVIOUS=1 and E2E_PREVIOUS_ARCHIVE=ref:<previous release>")
	}
	if infra.RunningArchive(t, f) != f.State.PreviousArchivePath {
		harness.SkipNotApplicable(t, "the fleet already runs HEAD: the previous-release install was upgraded before stage 10")
	}
	before := infra.RequireHealthy(t)
	n := ns.New(t, f, ns.Options{})
	tr := startTraffic(t, f, n.Client)
	mixedVersionWindow(t, f, before)
	st := *f.State
	ctx, cancel := context.WithTimeout(t.Context(), infra.UpgradeBudget)
	defer cancel()
	if err := provision.UpgradeToHead(ctx, &st, testLogger{t}); err != nil {
		t.Fatalf("the rolling upgrade to HEAD failed: %v", err)
	}
	tr.end()
	after := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "the cluster on HEAD")
	requireHeadEverywhere(t, f, after)
	requireSameIdentities(t, before, after)
	if sent, failed := tr.sent.Load(), tr.failed.Load(); sent == 0 || float64(failed) > maxFailedShare*float64(sent) {
		t.Errorf("%d of %d requests to the namespace failed during the upgrade", failed, sent)
	}
	if tr.cutShort.Load() {
		t.Errorf("the live traffic stopped after %s, before the upgrade ended", trafficBudget)
	}
	n.Client.MustSend(t, gw.Req{Path: "/v1/health"}).Expect(t, http.StatusOK)
	old := oramacli.ForPreviousRelease(t, f.State, f.Recorder())
	if _, err := monitor.Get(t.Context(), old.For(t), f.State.Env); err != nil {
		t.Errorf("the previous release's CLI cannot read the upgraded cluster: %v", err)
	}
}

func requireHeadEverywhere(t testing.TB, f *fleet.Fleet, r *monitor.Report) {
	t.Helper()
	head := infra.ArchiveManifest(t, f.State.ArchivePath)
	manifest := string(infra.ReadArchiveFile(t, f.State.ArchivePath, infra.ManifestName))
	for _, n := range f.State.Nodes {
		if got := string(f.ReadFile(t, n, infra.StagedManifest)); got != manifest {
			t.Errorf("%s does not have HEAD staged", n.Name)
		}
	}
	for _, e := range r.Nodes {
		if e.Report.Version != head.Version {
			t.Errorf("%s runs %s, HEAD is %s", e.Host, e.Report.Version, head.Version)
		}
	}
}

func requireSameIdentities(t testing.TB, before, after *monitor.Report) {
	t.Helper()
	ids := map[string]string{}
	for _, e := range before.Nodes {
		ids[e.Host] = e.Report.RQLite.NodeID + "@" + e.Report.WGIP
	}
	for _, e := range after.Nodes {
		if got := e.Report.RQLite.NodeID + "@" + e.Report.WGIP; got != ids[e.Host] {
			t.Errorf("%s changed identity in the upgrade: %s -> %s", e.Host, ids[e.Host], got)
		}
	}
	if after.Summary.RQLiteQuorum != monitor.StatusOK {
		t.Errorf("quorum after the upgrade is %q", after.Summary.RQLiteQuorum)
	}
}

// mixedVersionWindow upgrades one follower to HEAD and holds the cluster
// there: a mixed-release cluster converges, and both releases' CLIs read it
// (docs/whitepaper/technical-reference/vol1/31-rolling-upgrades.md "Rollout Strategy": followers first, leader last).
func mixedVersionWindow(t testing.TB, f *fleet.Fleet, before *monitor.Report) {
	t.Helper()
	first := infra.Followers(t, before)[0]
	cli := harness.CLI(t)
	infra.ExpectExit(t, infra.RunFor(t, cli, infra.UpgradeBudget, "node", "push", "--env", f.State.Env, "--node", first.PublicIP,
		"--archive", f.State.ArchivePath), infra.ExitOK)
	infra.ExpectExit(t, infra.RunFor(t, cli, infra.UpgradeBudget, "node", "upgrade", "--env", f.State.Env, "--node", first.PublicIP,
		"--yes"), infra.ExitOK)
	mixed := infra.WaitConverged(t, len(f.State.Nodes), infra.ConvergeBudget, "a mixed-release cluster")
	versions := map[string]bool{}
	for _, e := range mixed.Nodes {
		versions[e.Report.Version] = true
	}
	distinct := infra.ArchiveManifest(t, f.State.ArchivePath).Version != infra.ArchiveManifest(t, f.State.PreviousArchivePath).Version
	if distinct && len(versions) != 2 {
		t.Errorf("the mixed window runs %d releases, want 2: %v", len(versions), versions)
	}
	old := oramacli.ForPreviousRelease(t, f.State, f.Recorder())
	if _, err := monitor.Get(t.Context(), old.For(t), f.State.Env); err != nil {
		t.Errorf("the previous release's CLI cannot read the mixed cluster: %v", err)
	}
}
