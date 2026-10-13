package namespace

import (
	"context"
	"errors"
	"testing"
)

func clusterRows(t *testing.T, r *registryRig) int {
	t.Helper()
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM namespace_clusters`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func pendingRows(t *testing.T, r *registryRig) int {
	t.Helper()
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE namespace = 'acme'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A node that does not confirm the teardown must be reported as exactly that, so
// the delete can finish: the cluster row is already gone and the teardown is
// recorded for replay.
func TestDeprovisionCluster_unconfirmedTeardownIsReportedAsIncomplete(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.sendErr = errors.New("node unreachable")

	err := r.cm.DeprovisionCluster(context.Background(), nsID)
	if !errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want it to wrap ErrTeardownIncomplete", err)
	}
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d, want 0: the registry side is complete", n)
	}
	if n := pendingRows(t, r); n != 1 {
		t.Errorf("pending cleanups = %d, want the unconfirmed teardown recorded", n)
	}
}

// The retry of a delete whose teardown was unconfirmed finds no cluster and
// succeeds, and must not drop what is still owed.
func TestDeprovisionCluster_retryAfterAnIncompleteTeardownSucceedsAndKeepsThePendingCleanup(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.sendErr = errors.New("node unreachable")
	_ = r.cm.DeprovisionCluster(context.Background(), nsID)

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if n := pendingRows(t, r); n != 1 {
		t.Errorf("pending cleanups = %d, want the owed teardown kept", n)
	}
}

// Other failures are not "incomplete": only the unconfirmed node teardown is.
func TestDeprovisionCluster_aConfirmedTeardownIsNotIncomplete(t *testing.T) {
	r, nsID := deprovisionRig(t)
	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := pendingRows(t, r); n != 0 {
		t.Errorf("pending cleanups = %d, want none", n)
	}
}

// A cluster row that cannot be deleted must fail the deprovision with the row
// in place, so the retry finds it. It used to be ignored, and the delete went
// on to remove the namespace under a cluster that still existed.
func TestDeprovisionCluster_failedClusterRowDeleteFailsWithTheRowInPlace(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.exec(`CREATE TRIGGER refuse_cluster_delete BEFORE DELETE ON namespace_clusters BEGIN SELECT RAISE(ABORT, 'refused'); END`)

	err := r.cm.DeprovisionCluster(context.Background(), nsID)
	if err == nil || errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
	if n := clusterRows(t, r); n != 1 {
		t.Errorf("cluster rows = %d, want the row kept for the retry", n)
	}

	r.exec(`DROP TRIGGER refuse_cluster_delete`)
	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if n := clusterRows(t, r); n != 0 {
		t.Errorf("cluster rows = %d after the retry, want 0", n)
	}
}
