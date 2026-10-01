package namespace

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// owedRig is a deprovision of cluster c1 of "acme" on three nodes: node1 and
// node2 remote, "coordinator" the node the rig's manager runs on. Each has a
// core port block and, on node1, an SFU allocation.
func owedRig(t *testing.T) (*registryRig, int64) {
	t.Helper()
	r, nsID := deprovisionRig(t)
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('node2', '192.0.2.2', '10.0.0.2', 'active')`)
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('coordinator', '192.0.2.3', '10.0.0.3', 'active')`)
	r.membership("c1", "node2")
	r.membership("c1", "coordinator")
	for _, node := range []string{"node1", "node2", "coordinator"} {
		r.blockAt("c1", node, 10000)
	}
	r.cm.teardownLocalFn = func(context.Context, string, bool) error { return nil }
	return r, nsID
}

// blockAt writes a core port block of five ports from start.
func (r *registryRig) blockAt(cluster, node string, start int) {
	r.exec(`INSERT INTO namespace_port_allocations (id, node_id, namespace_cluster_id, port_start, port_end, rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
		VALUES (?, ?, ?, ?, ?, 1, 2, 3, 4, 5)`, "b"+node+cluster, node, cluster, start, start+4)
}

func (r *registryRig) portBlocks() map[string]bool {
	rows, err := r.db.Query(`SELECT node_id FROM namespace_port_allocations`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			r.t.Fatal(err)
		}
		out[n] = true
	}
	return out
}

func (r *registryRig) pendingFor(node string) (attempts int, ip string, found bool) {
	err := r.db.QueryRow(`SELECT attempts, node_ip FROM namespace_pending_cleanup WHERE namespace = 'acme' AND node_id = ?`, node).Scan(&attempts, &ip)
	return attempts, ip, err == nil
}

// Bug: a teardown that failed on the node the delete ran on was recorded
// nowhere, though ErrTeardownIncomplete says every unconfirmed teardown is.
func TestDeprovisionCluster_aFailedLocalTeardownIsRecordedAndReplayedLocally(t *testing.T) {
	r, nsID := owedRig(t)
	localFails := true
	var tornDown int
	r.cm.teardownLocalFn = func(_ context.Context, ns string, purge bool) error {
		if localFails {
			return errors.New("unit would not stop")
		}
		if ns != "acme" || !purge {
			t.Errorf("replayed local teardown of %q purge=%v, want acme with purge", ns, purge)
		}
		tornDown++
		return nil
	}
	r.webrtc("c1", "coordinator", "sfu")

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); !errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want ErrTeardownIncomplete", err)
	}
	if _, ip, ok := r.pendingFor("coordinator"); !ok || ip != "10.0.0.3" {
		t.Fatalf("local teardown recorded = %v with ip %q, want recorded with the node's overlay address", ok, ip)
	}
	if blocks := r.portBlocks(); !blocks["coordinator"] || blocks["node1"] || blocks["node2"] {
		t.Errorf("port blocks left = %v, want only the unconfirmed local node's", blocks)
	}

	localFails = false
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if tornDown != 1 || len(r.sent()) != 0 {
		t.Errorf("local teardowns = %d, spawn requests = %v, want one local teardown and no request", tornDown, r.sent())
	}
	if _, _, ok := r.pendingFor("coordinator"); ok {
		t.Error("the completed local teardown is still owed")
	}
	if blocks := r.portBlocks(); len(blocks) != 0 {
		t.Errorf("port blocks left after the replay = %v, want none", blocks)
	}
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE node_id = 'coordinator'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("WebRTC allocations of the local node = %d (%v), want freed with the teardown", n, err)
	}
}

// Bug: a remote node with no overlay address was reported but recorded nowhere.
// The replay looks the address up again, and sends once the node has one.
func TestDeprovisionCluster_aNodeWithoutAnOverlayAddressIsRecordedAndReplayedOnceItHasOne(t *testing.T) {
	r, nsID := owedRig(t)
	r.exec(`UPDATE dns_nodes SET internal_ip = '', ip_address = '' WHERE id = 'node1'`)

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); !errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want ErrTeardownIncomplete", err)
	}
	if _, _, ok := r.pendingFor("node1"); !ok {
		t.Fatal("the teardown of the node with no address was not recorded")
	}
	if !r.portBlocks()["node1"] || !r.hasWebRTCRow("node1") {
		t.Error("the unreachable node's reservations were freed")
	}

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if attempts, _, ok := r.pendingFor("node1"); !ok || attempts != 2 || len(r.sent()) != 0 {
		t.Errorf("attempts = %d, requests = %v, want a second failed attempt and nothing sent", attempts, r.sent())
	}

	r.exec(`UPDATE dns_nodes SET internal_ip = '10.0.0.1' WHERE id = 'node1'`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 || got[0] != "teardown-namespace:acme@node1" {
		t.Errorf("requests = %v, want the teardown sent to node1", got)
	}
	if _, _, ok := r.pendingFor("node1"); ok {
		t.Error("the completed teardown is still owed")
	}
	if r.portBlocks()["node1"] || r.hasWebRTCRow("node1") {
		t.Error("the node's reservations survived its completed teardown")
	}
}

func (r *registryRig) hasWebRTCRow(node string) bool {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE node_id = ?`, node).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n > 0
}

// Bug: the core port blocks were freed before the unconfirmed check, so the
// next namespace was given the ports a still-running unit holds (#275). Only the
// confirmed nodes' blocks go; the unconfirmed node's block still counts as taken
// with its cluster row gone.
func TestDeprovisionCluster_keepsThePortBlockOfAnUnconfirmedNodeAndTheAllocatorHonoursIt(t *testing.T) {
	r, nsID := owedRig(t)
	r.cm.spawnRequestFn = func(_ context.Context, ip string, _ map[string]interface{}) (*spawnResponse, error) {
		if ip == "10.0.0.1" {
			return nil, errors.New("node unreachable")
		}
		return &spawnResponse{Success: true}, nil
	}
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, zap.NewNop())

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); !errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want ErrTeardownIncomplete", err)
	}
	if blocks := r.portBlocks(); len(blocks) != 1 || !blocks["node1"] {
		t.Fatalf("port blocks left = %v, want only node1's", blocks)
	}
	if clusterRows(t, r) != 0 {
		t.Fatal("the cluster row was kept")
	}

	got, err := r.cm.portAllocator.AllocatePortBlock(context.Background(), "node1", "c-new", BlueprintTenant())
	if err != nil {
		t.Fatal(err)
	}
	if got.PortStart <= 10004 {
		t.Errorf("the new namespace was given ports from %d, inside the block of the namespace still being torn down", got.PortStart)
	}
	if free, err := r.cm.portAllocator.AllocatePortBlock(context.Background(), "node2", "c-new", BlueprintTenant()); err != nil || free.PortStart != 10000 {
		t.Errorf("node2's confirmed block was not freed: %v %v", free, err)
	}

	r.cm.spawnRequestFn = func(context.Context, string, map[string]interface{}) (*spawnResponse, error) {
		return &spawnResponse{Success: true}, nil
	}
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM namespace_port_allocations WHERE namespace_cluster_id = 'c1'`).Scan(&left); err != nil || left != 0 {
		t.Errorf("blocks of the deleted cluster after the replay = %d (%v), want none", left, err)
	}
}

// A teardown owed to a node that cannot be recorded cannot be replayed, so the
// delete must not report it as owed: it fails with the cluster row kept.
func TestDeprovisionCluster_aTeardownThatCannotBeRecordedKeepsTheCluster(t *testing.T) {
	r, nsID := owedRig(t)
	r.sendErr = errors.New("node unreachable")
	r.exec(`CREATE TRIGGER refuse_pending BEFORE INSERT ON namespace_pending_cleanup BEGIN SELECT RAISE(ABORT, 'refused'); END`)

	err := r.cm.DeprovisionCluster(context.Background(), nsID)
	if err == nil || errors.Is(err, ErrTeardownIncomplete) {
		t.Fatalf("err = %v, want a plain failure", err)
	}
	if !errors.Is(err, errCleanupNotRecorded) {
		t.Errorf("err = %v, want it to say the cleanup was not recorded", err)
	}
	if clusterRows(t, r) != 1 {
		t.Error("the cluster row was deleted although nothing owes the nodes their teardown")
	}
	if blocks := r.portBlocks(); len(blocks) != 3 {
		t.Errorf("port blocks = %v, want all kept for the retry", blocks)
	}
}

// An exhausted cleanup is not dropped: it is kept, retried hourly, and logged at
// Error each time it fails.
func TestReplayPendingCleanups_anExhaustedCleanupIsKeptRetriedHourlyAndLoggedAtError(t *testing.T) {
	r := newRegistryRig(t)
	core, logs := observer.New(zap.ErrorLevel)
	r.cm.logger = zap.New(core)
	r.cluster("c-acme", "acme")
	r.pending("acme", "node1", teardownAction, "c-acme", true)
	r.exec(`UPDATE namespace_pending_cleanup SET attempts = ?, last_attempt_at = CURRENT_TIMESTAMP`, pendingCleanupMaxAttempts)
	r.sendErr = errors.New("node unreachable")

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.sent()) != 0 {
		t.Fatalf("an exhausted cleanup was retried at once: %v", r.sent())
	}

	r.exec(`UPDATE namespace_pending_cleanup SET last_attempt_at = datetime('now', '-2 hours')`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if len(r.sent()) != 1 {
		t.Fatalf("requests = %v, want the hourly retry", r.sent())
	}
	if attempts, _, ok := r.pendingFor("node1"); !ok || attempts != pendingCleanupMaxAttempts+1 {
		t.Errorf("attempts = %d, found = %v, want the row kept and counted", attempts, ok)
	}
	if logs.Len() == 0 {
		t.Error("an exhausted cleanup that failed again was not logged at Error")
	}

	r.sendErr = nil
	r.exec(`UPDATE namespace_pending_cleanup SET last_attempt_at = datetime('now', '-2 hours')`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if r.pendingCount() != 0 {
		t.Error("an exhausted cleanup that finally succeeded is still owed")
	}
}
