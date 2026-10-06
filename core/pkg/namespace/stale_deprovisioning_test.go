package namespace

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

func (r *registryRig) setDeprovisioning(stamp string) {
	r.t.Helper()
	if stamp == "" {
		r.exec(`UPDATE namespace_clusters SET status = 'deprovisioning', deprovisioning_at = NULL WHERE id = 'c1'`)
		return
	}
	r.exec(`UPDATE namespace_clusters SET status = 'deprovisioning', deprovisioning_at = datetime('now', ?) WHERE id = 'c1'`, stamp)
}

func (r *registryRig) teardownRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, req := range r.requests {
		if req["action"] == teardownAction {
			n++
		}
	}
	return n
}

// A cluster found in 'deprovisioning' with no teardown running for it (the node
// that took the delete stopped, or the request was cut off) is finished by the
// sweep: nothing else looked at such a cluster, and it stayed there for ever
// with its node rows 'running'.
func TestResumeStaleDeprovisioning_finishesAnAbandonedTeardown(t *testing.T) {
	r, _ := deprovisionRig(t)
	r.setDeprovisioning("-1 hour")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d, want the cluster removed", n)
	}
	if n := r.teardownRequests(); n != 1 {
		t.Errorf("teardown requests = %d, want the node asked once", n)
	}
}

// A cluster marked by a node on the previous release has no stamp, and its delete
// may still be running there: it is stamped, and resumed only after a whole
// window of silence.
func TestResumeStaleDeprovisioning_aClusterWithNoStampGetsAWholeWindow(t *testing.T) {
	r, _ := deprovisionRig(t)
	r.setDeprovisioning("")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 1 || r.teardownRequests() != 0 {
		t.Fatalf("an unstamped cluster was resumed at once: rows %d, requests %d", n, r.teardownRequests())
	}
	var stamped int
	if err := r.db.QueryRow(`SELECT deprovisioning_at IS NOT NULL FROM namespace_clusters WHERE id = 'c1'`).Scan(&stamped); err != nil || stamped != 1 {
		t.Fatalf("the cluster was not stamped (%v)", err)
	}

	r.exec(`UPDATE namespace_clusters SET deprovisioning_at = datetime('now', '-1 hour') WHERE id = 'c1'`)
	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d, want it resumed after the window", n)
	}
}

// A teardown that is still running keeps its lease: a sweep on another node
// must not run a second one under it.
func TestResumeStaleDeprovisioning_leavesALiveTeardownAlone(t *testing.T) {
	r, _ := deprovisionRig(t)
	r.setDeprovisioning("-1 minutes")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 1 {
		t.Errorf("cluster rows = %d, want the cluster left to its teardown", n)
	}
	if n := r.teardownRequests(); n != 0 {
		t.Errorf("teardown requests = %d, want none", n)
	}
}

// Clusters in any other state are not this sweep's.
func TestResumeStaleDeprovisioning_ignoresOtherStatuses(t *testing.T) {
	r, _ := deprovisionRig(t)

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 1 || r.teardownRequests() != 0 {
		t.Errorf("a ready cluster was touched: rows %d, requests %d", n, r.teardownRequests())
	}
}

// Every node sweeps; the guarded claim lets exactly one resume. The first
// resume fails to delete the row, so the cluster is still there, with a fresh
// lease, for the second sweep to find and leave alone.
func TestResumeStaleDeprovisioning_onlyOneNodeTakesAClusterOver(t *testing.T) {
	r, _ := deprovisionRig(t)
	r.exec(`CREATE TRIGGER refuse_cluster_delete BEFORE DELETE ON namespace_clusters BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	r.setDeprovisioning("-1 hour")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	first := r.teardownRequests()
	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatalf("a second sweep inside the lease: %v", err)
	}
	r.cm.resumeWG.Wait()
	if got := r.teardownRequests(); got != first || first != 1 {
		t.Errorf("teardown requests = %d after the second sweep, want the one from the first", got)
	}
}

// An unconfirmed node is the delete's usual outcome and is owed, not a failure
// of the resume.
func TestResumeStaleDeprovisioning_anUnconfirmedNodeIsNotAFailure(t *testing.T) {
	r, _ := deprovisionRig(t)
	r.sendErr = errors.New("node unreachable")
	r.setDeprovisioning("-1 hour")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatalf("err = %v", err)
	}
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d, want 0", n)
	}
	if n := pendingRows(t, r); n != 1 {
		t.Errorf("pending cleanups = %d, want the node's teardown owed", n)
	}
}

// A resume of a cluster whose nodes do not answer takes minutes, and the sweep
// also restores services and replays cleanups: it only claims, and returns while
// the resume runs.
func TestResumeStaleDeprovisioning_theSweepDoesNotWaitForTheResume(t *testing.T) {
	r, _ := deprovisionRig(t)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	r.cm.spawnRequestFn = func(context.Context, string, map[string]interface{}) (*spawnResponse, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return &spawnResponse{Success: true}, nil
	}
	r.setDeprovisioning("-1 hour")

	done := make(chan error, 1)
	go func() { done <- r.cm.resumeStaleDeprovisioning(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the sweep waited for the resumed teardown")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the teardown was never resumed")
	}
	close(release)
	r.cm.resumeWG.Wait()
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d, want the resume to finish", n)
	}
}

// One resume per cluster per process, and at most maxConcurrentResumes at once.
func TestReserveResume_boundsAndDeduplicates(t *testing.T) {
	cm := &ClusterManager{}
	if !cm.reserveResume("a") || cm.reserveResume("a") {
		t.Fatal("a cluster can be resumed twice at once")
	}
	for i := 1; i < maxConcurrentResumes; i++ {
		if !cm.reserveResume(fmt.Sprint("b", i)) {
			t.Fatalf("slot %d refused below the cap", i)
		}
	}
	if cm.reserveResume("overflow") {
		t.Fatal("the cap was exceeded")
	}
	cm.releaseResume("a")
	if !cm.reserveResume("overflow") {
		t.Fatal("a released slot was not reusable")
	}
}

// A cluster is claimed only when a slot is free, so one with no room stays
// stale for the next sweep rather than claimed and abandoned.
func TestResumeStaleDeprovisioning_noFreeSlotLeavesTheClusterUnclaimed(t *testing.T) {
	r, _ := deprovisionRig(t)
	for i := 0; i < maxConcurrentResumes; i++ {
		r.cm.reserveResume(fmt.Sprint("busy", i))
	}
	r.setDeprovisioning("-1 hour")

	if err := r.cm.resumeStaleDeprovisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.cm.resumeWG.Wait()
	var stale int
	if err := r.db.QueryRow(`SELECT datetime(deprovisioning_at) < datetime('now', '-30 minutes') FROM namespace_clusters WHERE id = 'c1'`).Scan(&stale); err != nil || stale != 1 {
		t.Fatalf("the cluster was claimed with no slot to run it (%v)", err)
	}
}

func TestBeginDeprovision(t *testing.T) {
	ctx := context.Background()
	t.Run("a cluster nobody tears down is claimed", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
		var status string
		var stamped int
		if err := r.db.QueryRow(`SELECT status, deprovisioning_at IS NOT NULL FROM namespace_clusters WHERE id = 'c1'`).Scan(&status, &stamped); err != nil {
			t.Fatal(err)
		}
		if status != "deprovisioning" || stamped != 1 {
			t.Errorf("status %q stamped %d", status, stamped)
		}
	})
	t.Run("a second delete inside the window is refused", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
		if err := BeginDeprovision(ctx, r.client, nsID); !errors.Is(err, ErrDeprovisionInProgress) {
			t.Fatalf("err = %v, want ErrDeprovisionInProgress", err)
		}
	})
	t.Run("an abandoned one is taken over", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		r.setDeprovisioning("-1 hour")
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an unstamped one (previous release) is taken over by a client retry", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		r.setDeprovisioning("")
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a namespace with no cluster has nothing to claim", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		r.exec(`DELETE FROM namespace_clusters`)
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a released claim does not refuse the retry", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
		if err := ReleaseDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatalf("the retry after a release: %v", err)
		}
	})
	t.Run("a live delete is not taken over by the reconciler", func(t *testing.T) {
		r, nsID := deprovisionRig(t)
		if err := BeginDeprovision(ctx, r.client, nsID); err != nil {
			t.Fatal(err)
		}
		if err := r.cm.resumeStaleDeprovisioning(ctx); err != nil {
			t.Fatal(err)
		}
		r.cm.resumeWG.Wait()
		if n := r.teardownRequests(); n != 0 || clusterRows(t, r) != 1 {
			t.Fatalf("a resume ran under a live delete: requests %d", n)
		}
	})
}

// The window restarts between the steps of a teardown: a slow one that is alive
// is not taken over. The first step ages the stamp, as a long fan-out would.
func TestDeprovisionCluster_refreshesItsClaimBetweenSteps(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.exec(`CREATE TRIGGER refuse_cluster_delete BEFORE DELETE ON namespace_clusters BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	r.cm.spawnRequestFn = func(context.Context, string, map[string]interface{}) (*spawnResponse, error) {
		r.exec(`UPDATE namespace_clusters SET deprovisioning_at = datetime('now', '-1 hour') WHERE id = 'c1'`)
		return &spawnResponse{Success: true}, nil
	}

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err == nil {
		t.Fatal("the refused delete was not reported")
	}
	var fresh int
	if err := r.db.QueryRow(`SELECT datetime(deprovisioning_at) >= datetime('now', '-1 minutes') FROM namespace_clusters WHERE id = 'c1'`).Scan(&fresh); err != nil || fresh != 1 {
		t.Fatalf("the claim was not refreshed after the node steps (%v)", err)
	}
}

// A teardown whose caller has given up by the time the namespace's lock is free
// is not begun on this node.
func TestTeardownNamespaceOfCluster_honoursACancelledContext(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())
	ran := false
	s.teardownUnitsFn = func(string) error { ran = true; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.TeardownNamespaceOfCluster(ctx, "acme", "", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if ran {
		t.Fatal("the units were torn down for a caller that had given up")
	}
}

// The delete stamps the cluster, so that a live teardown can be told from an
// abandoned one.
func TestDeprovisionCluster_stampsTheClusterWhileItRuns(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.exec(`CREATE TRIGGER refuse_cluster_delete BEFORE DELETE ON namespace_clusters BEGIN SELECT RAISE(ABORT, 'refused'); END`)

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err == nil {
		t.Fatal("the refused delete was not reported")
	}
	var status string
	var stamped int
	if err := r.db.QueryRow(`SELECT status, deprovisioning_at IS NOT NULL FROM namespace_clusters WHERE id = 'c1'`).Scan(&status, &stamped); err != nil {
		t.Fatal(err)
	}
	if status != string(ClusterStatusDeprovisioning) || stamped != 1 {
		t.Errorf("status %q stamped %d, want deprovisioning and a stamp", status, stamped)
	}
}

// A node that has been told to tear a namespace down must not start its SFU
// again. The teardown holds the namespace's lock for as long as the SFU takes to
// drain; the sweep that was waiting on it then read the registry, which keeps the
// WebRTC config and the SFU allocation until every node has confirmed, and
// started the SFU into a namespace whose data had just been deleted (devnet
// 2026-10-01, e2e-3b990879-sd5jth on poseidon).
func TestSpawnSFUIfDown_doesNotStartForAClusterBeingTornDown(t *testing.T) {
	for _, status := range []string{"deprovisioning", "failed", "provisioning"} {
		t.Run(status, func(t *testing.T) {
			r, asked := sfuSpawnRig(t, status)
			r.cm.spawnSFUIfDown(context.Background(), &ClusterLocalState{NamespaceName: "acme", ClusterID: "c-acme"}, "turn.example.test")
			if *asked {
				t.Fatalf("the SFU of a %s cluster was considered for a start", status)
			}
		})
	}
	t.Run("cluster gone", func(t *testing.T) {
		r, asked := sfuSpawnRig(t, "ready")
		r.exec(`DELETE FROM namespace_clusters WHERE id = 'c-acme'`)
		r.cm.spawnSFUIfDown(context.Background(), &ClusterLocalState{NamespaceName: "acme", ClusterID: "c-acme"}, "turn.example.test")
		if *asked {
			t.Fatal("the SFU of a namespace the registry no longer holds was considered for a start")
		}
	})
}

func TestSpawnSFUIfDown_stillStartsForAServingCluster(t *testing.T) {
	for _, status := range []string{"ready", "degraded"} {
		r, asked := sfuSpawnRig(t, status)
		r.cm.spawnSFUIfDown(context.Background(), &ClusterLocalState{NamespaceName: "acme", ClusterID: "c-acme"}, "turn.example.test")
		if !*asked {
			t.Fatalf("a %s cluster's SFU is no longer checked for a start", status)
		}
	}
}

// sfuSpawnRig is a cluster with WebRTC enabled and this node's SFU allocated,
// whose unit state is read through a recorded query that reports it running (so
// nothing is spawned). asked reports whether the start was reached.
func sfuSpawnRig(t *testing.T, status string) (*sfuRig, *bool) {
	t.Helper()
	r := newSFURig(t)
	r.cluster("c-acme", "acme")
	r.exec(`UPDATE namespace_clusters SET status = ? WHERE id = 'c-acme'`, status)
	r.membership("c-acme", "coordinator")
	r.webrtcConfig("c-acme", "acme", true)
	if _, err := r.cm.webrtcPortAllocator.AllocateSFUPorts(context.Background(), "coordinator", "c-acme"); err != nil {
		t.Fatal(err)
	}
	r.cm.systemdSpawner = NewSystemdSpawner(r.nsBase, "", zap.NewNop())
	r.cm.systemdSpawner.systemdMgr = systemd.NewManager(r.nsBase, zap.NewNop())

	asked := false
	prev := serviceActiveState
	serviceActiveState = func(*systemd.Manager, string, systemd.ServiceType) (systemd.ActiveState, error) {
		asked = true
		return systemd.ActiveStateActive, nil
	}
	t.Cleanup(func() { serviceActiveState = prev })
	return r, &asked
}
