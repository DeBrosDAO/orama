package namespace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

func (r *registryRig) rows() []pendingCleanupRow {
	r.t.Helper()
	var rows []pendingCleanupRow
	if err := r.client.Query(client.WithInternalAuth(context.Background()), &rows,
		`SELECT id, namespace, node_id, node_ip, action, cluster_id, purge_data, attempts FROM namespace_pending_cleanup`); err != nil {
		r.t.Fatal(err)
	}
	return rows
}

func (r *registryRig) count(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(query, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// Every node's gateway replays the same rows. Two of them holding the same row
// sent the same teardown twice, and the second could land after the name was
// created again on the node.
func TestReplayRow_twoReplayersOfOneRowSendOnce(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", true)
	row := r.rows()[0]

	entered := make(chan struct{})
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	r.cm.spawnRequestFn = func(context.Context, string, map[string]interface{}) (*spawnResponse, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(entered)
		<-release
		return &spawnResponse{Success: true}, nil
	}

	done := make(chan error, 1)
	go func() { done <- r.cm.replayRow(context.Background(), row) }()
	<-entered
	if err := r.cm.replayRow(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("the teardown was sent %d times, want once", calls)
	}
	if r.pendingCount() != 0 {
		t.Fatal("the replayed row was kept")
	}
}

// A gateway that read the row before another replayed and failed it holds a
// stale attempt count: it must not replay the row again, or the attempts grow
// once per gateway.
func TestReplayRow_aReplayerWithAStaleReadLeavesTheRowToTheNextSweep(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", false)
	stale := r.rows()[0]
	r.sendErr = errors.New("node unreachable")

	if err := r.cm.replayRow(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if err := r.cm.replayRow(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if got := len(r.sent()); got != 1 {
		t.Fatalf("sent %d times, want once", got)
	}
	if attempts, _, _ := r.pendingFor("node1"); attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (the record, and one replay)", attempts)
	}
}

// A lease that was never given back (its gateway died) lapses: the row is
// replayed again once it has.
func TestReplayPendingCleanups_aLapsedClaimIsReplayed(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", false)
	r.exec(`UPDATE namespace_pending_cleanup SET claimed_until = datetime('now', '+5 minutes')`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.sent()) != 0 {
		t.Fatalf("a row claimed by another gateway was replayed: %v", r.sent())
	}
	r.exec(`UPDATE namespace_pending_cleanup SET claimed_until = datetime('now', '-1 minutes')`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.sent()) != 1 {
		t.Fatalf("sent %v, want the replay once the claim lapsed", r.sent())
	}
}

func TestReplayPendingCleanups_aTeardownCarriesItsClusterID(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", true)
	r.pending("acme", "node1", teardownSFUAction, "c-old", false)
	r.pending("acme", "node1", "stop-gateway", "", false)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 3 {
		t.Fatalf("requests = %v", r.requests)
	}
	for _, req := range r.requests {
		id, has := req["cluster_id"]
		switch req["action"] {
		case teardownAction, teardownSFUAction:
			if id != "c-old" {
				t.Errorf("%v: cluster_id = %v, want c-old", req["action"], id)
			}
		default:
			if has {
				t.Errorf("%v carries a cluster_id: %v", req["action"], id)
			}
		}
	}
}

// The row went before the ports were freed, and a failure to free them was only
// logged: nothing owed them any more, and they leaked for good.
func TestReplayRow_aFailedReleaseKeepsTheRowAndTheBlock(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.pending("acme", "node1", teardownAction, "c1", false)
	r.exec(`CREATE TRIGGER no_release BEFORE DELETE ON namespace_port_allocations
		BEGIN SELECT RAISE(ABORT, 'registry unavailable'); END`)

	if err := r.replay(); err == nil {
		t.Fatal("a failed release was not reported")
	}
	if r.pendingCount() != 1 {
		t.Fatal("the row was cleared before the block was freed: nothing owes it any more")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 1 {
		t.Fatal("the block was freed")
	}

	r.exec(`DROP TRIGGER no_release`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if r.pendingCount() != 0 || r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 0 {
		t.Fatal("the retry did not free the block and clear the row")
	}
}

// The superseded drop frees the old incarnation's blocks, and only those.
func TestReplayRow_aSupersededDropFreesTheOldClustersBlocksNotTheNewOnes(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-old", "acme")
	r.cluster("c-new", "acme")
	r.allocation("c-old", "node1")
	r.webrtc("c-old", "node1", "sfu")
	r.allocation("c-new", "node1")
	r.webrtc("c-new", "node1", "sfu")
	r.pending("acme", "node1", teardownAction, "c-old", true)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.sent()) != 0 || r.pendingCount() != 0 {
		t.Fatalf("sent %v, pending %d: the superseded teardown must be dropped unsent", r.sent(), r.pendingCount())
	}
	if n := r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = 'c-old'`) +
		r.count(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE namespace_cluster_id = 'c-old'`); n != 0 {
		t.Fatalf("%d blocks of the old cluster leaked", n)
	}
	if n := r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = 'c-new'`) +
		r.count(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE namespace_cluster_id = 'c-new'`); n != 2 {
		t.Fatalf("the new cluster holds %d blocks, want its 2", n)
	}
}

// Giving the namespace the node again withdraws the teardown owed for the
// earlier incarnation; its blocks go with it, the new cluster's stay.
func TestWithdrawPendingTeardowns_freesTheOldClustersBlocksNotTheNewOnes(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-old", "acme")
	r.cluster("c-new", "acme")
	r.allocation("c-old", "node1")
	r.webrtc("c-old", "node1", "sfu")
	r.webrtc("c-old", "node1", "turn")
	r.allocation("c-new", "node1")
	r.webrtc("c-new", "node1", "sfu")
	r.pending("acme", "node1", teardownAction, "c-old", false)
	r.pending("acme", "node1", teardownSFUAction, "c-old", false)

	if err := withdrawPendingTeardowns(context.Background(), r.client, "c-new", "node1", teardownAction, teardownSFUAction); err != nil {
		t.Fatal(err)
	}
	if r.pendingCount() != 0 {
		t.Fatal("the withdrawn teardowns are still owed")
	}
	if n := r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = 'c-old'`) +
		r.count(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE namespace_cluster_id = 'c-old'`); n != 0 {
		t.Fatalf("%d blocks of the old cluster leaked", n)
	}
	if n := r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = 'c-new'`) +
		r.count(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE namespace_cluster_id = 'c-new'`); n != 2 {
		t.Fatalf("the new cluster holds %d blocks, want its 2", n)
	}
}

func TestWithdrawPendingTeardowns_withNothingOwedChangesNothing(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.allocation("c-new", "node1")
	if err := withdrawPendingTeardowns(context.Background(), r.client, "c-new", "node1", teardownAction); err != nil {
		t.Fatal(err)
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 1 {
		t.Fatal("a block was freed with no teardown owed")
	}
}

// The failed-cluster path freed every block of the cluster, including that of a
// node whose teardown is still owed and whose units may still hold the ports.
func TestCheckNamespaceCluster_aFailedClusterKeepsAnUnconfirmedNodesBlock(t *testing.T) {
	r := newRegistryRig(t)
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, r.cm.logger)
	r.cm.dnsManager = NewDNSRecordManager(r.client, "example.test", r.cm.logger)
	r.cluster("c1", "acme")
	r.exec(`UPDATE namespace_clusters SET status = 'failed'`)
	r.allocation("c1", "node1")
	r.allocation("c1", "node2")
	r.pending("acme", "node1", teardownAction, "c1", false)

	_, _, needs, err := r.cm.CheckNamespaceCluster(context.Background(), "acme")
	if err != nil || !needs {
		t.Fatalf("needs provisioning = %v, err = %v", needs, err)
	}
	var kept []string
	rows, err := r.db.Query(`SELECT node_id FROM namespace_port_allocations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, n)
	}
	if len(kept) != 1 || kept[0] != "node1" {
		t.Fatalf("blocks kept = %v, want only the unconfirmed node1's", kept)
	}
}

// A node deleted from dns_nodes was removed from the cluster: its units went
// with it, so the cleanup is dropped and its blocks freed. A node that is only
// offline still has its row, and the cleanup stays owed.
func TestReplayPendingCleanups_aRemovedNodesCleanupIsDroppedAndAnOfflineNodesIsKept(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node8")
	r.allocation("c1", "node9")
	if err := r.cm.recordPendingCleanup(context.Background(), "acme", "node8", "10.0.0.8", teardownAction,
		cleanupScope{ClusterID: "c1"}, errors.New("node unreachable")); err != nil {
		t.Fatal(err) // node8 has no dns_nodes row
	}
	r.pending("acme", "node9", teardownAction, "c1", false)
	r.exec(`UPDATE dns_nodes SET status = 'offline' WHERE id = 'node9'`)
	r.sendErr = errors.New("node unreachable")

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 || !strings.HasSuffix(got[0], "@node9") {
		t.Fatalf("sent %v, want only the offline node9 tried", got)
	}
	if _, _, ok := r.pendingFor("node8"); ok {
		t.Error("the removed node's cleanup is retried for ever")
	}
	if _, _, ok := r.pendingFor("node9"); !ok {
		t.Error("an offline node's cleanup was dropped")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE node_id = 'node8'`) != 0 {
		t.Error("the removed node's block leaked")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations WHERE node_id = 'node9'`) != 1 {
		t.Error("the offline node's block was freed while its units may hold it")
	}
}

func TestSendSpawnRequest_refusesATargetOutsideTheOverlay(t *testing.T) {
	for _, ip := range []string{"203.0.113.9", "127.0.0.1", "", "10.0.0.5:6001", "10.0.1.5", "not-an-ip"} {
		if err := requireOverlayTarget(ip); err == nil {
			t.Errorf("%q was accepted as a spawn target", ip)
		}
	}
	if err := requireOverlayTarget("10.0.0.5"); err != nil {
		t.Errorf("an overlay address was refused: %v", err)
	}

	r := newRegistryRig(t)
	r.cm.spawnRequestFn = nil // the real sender
	err := r.cm.sendStopRequest(context.Background(), "203.0.113.9", teardownAction, "acme", "node1", cleanupScope{ClusterID: "c1"})
	if err == nil || !strings.Contains(err.Error(), "overlay") {
		t.Fatalf("err = %v, want the refusal of a non-overlay target", err)
	}
	if r.pendingCount() != 1 {
		t.Fatal("the refused teardown was not kept owed")
	}
}

// The sweep's first read of the allocation is made before the lock; the decision
// to stop is made again under it.
func TestStopServiceIfStillUnallocated_decidesUnderTheNamespaceLock(t *testing.T) {
	s := &SystemdSpawner{}
	cm := &ClusterManager{systemdSpawner: s}
	unlock := s.LockNamespace("acme")

	var heldDuringRead bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.stopServiceIfStillUnallocated("acme", "sfu", func() bool {
			m, _ := s.namespaceLocks.Load("acme")
			mu := m.(*sync.Mutex)
			if mu.TryLock() {
				mu.Unlock()
			} else {
				heldDuringRead = true
			}
			return false
		})
	}()
	select {
	case <-done:
		t.Fatal("the stop decision was made while another holder had the namespace's lock")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	<-done
	if !heldDuringRead {
		t.Fatal("the allocation was re-read without the namespace's lock")
	}
}
