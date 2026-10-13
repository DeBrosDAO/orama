package namespace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// hookClient runs after for every write the registry sees, once the write has
// been applied: a way to make another gateway act at an exact point of a replay.
type hookClient struct {
	rqlite.Client
	after func(query string)
}

func (h *hookClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := h.Client.Exec(ctx, query, args...)
	if err == nil && h.after != nil {
		h.after(query)
	}
	return res, err
}

// reAdd is what the cluster being given node1 again does to the registry.
func reAdd(t *testing.T, r *registryRig) bool {
	t.Helper()
	_, owed, err := NewNamespacePortAllocator(r.client, r.cm.logger).AllocatePortBlock(context.Background(), "node1", "c1", BlueprintTenant())
	if err != nil {
		t.Fatal(err)
	}
	return owed
}

// owedEvictedRig is c1 with node1 evicted while its teardown was unconfirmed:
// the block is kept, the teardown is owed, the membership is gone.
func owedEvictedRig(t *testing.T) *registryRig {
	t.Helper()
	r := newEvictRig(t)
	r.sendErr = errors.New("node unreachable")
	r.exec(`DELETE FROM namespace_cluster_nodes WHERE node_id = 'node1'`)
	_ = r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1")
	r.sendErr = nil
	if r.pendingCount() != 1 || !r.portBlocks()["node1"] {
		t.Fatal("rig: the teardown is not owed")
	}
	return r
}

// Bug: the re-add withdrew the owed row after the replay had claimed and read
// it, and the replay sent the teardown to the live member.
func TestReplayRow_aReAddBetweenTheClaimAndTheSendIsNotTornDown(t *testing.T) {
	r := owedEvictedRig(t)
	hooked := &hookClient{Client: r.client}
	r.cm.db = hooked
	hooked.after = func(q string) {
		if strings.Contains(q, "SET claimed_until") {
			hooked.after = nil
			reAdd(t, r)
		}
	}

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 0 {
		t.Fatalf("sent %v: the replay tore down the re-added member", got)
	}
	if !r.portBlocks()["node1"] {
		t.Error("the re-added member's block was freed")
	}
}

// Bug: the teardown was already on the wire when the node was given the cluster
// again, and the replay then freed the block of the member it had become.
func TestReplayRow_aReAddWhileTheTeardownIsOnTheWireKeepsTheBlock(t *testing.T) {
	r := owedEvictedRig(t)
	send := r.cm.spawnRequestFn
	r.cm.spawnRequestFn = func(ctx context.Context, ip string, req map[string]interface{}) (*spawnResponse, error) {
		reAdd(t, r)
		return send(ctx, ip, req)
	}

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if !r.portBlocks()["node1"] || !r.hasWebRTCRow("node1") {
		t.Error("the replay freed the reservations of the node that was given the cluster again")
	}
}

// A membership row of the very cluster means the node is a member again; the
// cleanup owed for it is dropped unsent and its block, now the member's, kept.
func TestReplayRow_aMemberOfTheSameClusterIsNotTornDown(t *testing.T) {
	r := owedEvictedRig(t)
	r.membership("c1", "node1")

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 0 {
		t.Fatalf("sent %v: the replay tore down a current member", got)
	}
	if r.pendingCount() != 0 {
		t.Error("the cleanup owed for a node that is a member again stayed")
	}
	if !r.portBlocks()["node1"] || !r.hasWebRTCRow("node1") {
		t.Error("a current member's reservations were freed")
	}
}

// A teardown that is still owed (not a member) is replayed as before.
func TestReplayRow_aNodeThatIsNotAMemberIsStillTornDown(t *testing.T) {
	r := owedEvictedRig(t)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 {
		t.Fatalf("sent %v, want the owed teardown", got)
	}
	if r.portBlocks()["node1"] || r.pendingCount() != 0 {
		t.Error("the owed teardown did not free the block")
	}
}

// Bug: a rolled-back add of a node that was owed freed the block its units may
// still hold, with nothing left owing it (#275).
func TestRollbackPortBlock_aBlockThatWasOwedIsOwedAgainNotFreed(t *testing.T) {
	r := owedEvictedRig(t)
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, r.cm.logger)
	if !reAdd(t, r) {
		t.Fatal("rig: the re-add did not report the block as owed")
	}

	r.cm.rollbackPortBlock(context.Background(), &NamespaceCluster{ID: "c1", NamespaceName: "acme"},
		&NodeCapacity{NodeID: "node1", InternalIP: "10.0.0.1"}, true)

	if !r.portBlocks()["node1"] {
		t.Error("the rollback freed a block that was owed its teardown")
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE node_id = 'node1' AND cluster_id = 'c1' AND action = ?`, teardownAction); got != 1 {
		t.Errorf("owed teardowns = %d, want the teardown recorded again", got)
	}
}

func TestRollbackPortBlock_aNewBlockIsFreed(t *testing.T) {
	r := newEvictRig(t)
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, r.cm.logger)

	_, owed, err := r.cm.portAllocator.AllocatePortBlock(context.Background(), "node3", "c1", BlueprintTenant())
	if err != nil {
		t.Fatal(err)
	}
	if owed {
		t.Fatal("a block allocated for a node that owed nothing was reported as owed")
	}
	r.cm.rollbackPortBlock(context.Background(), &NamespaceCluster{ID: "c1", NamespaceName: "acme"},
		&NodeCapacity{NodeID: "node3"}, owed)

	if r.portBlocks()["node3"] || r.pendingCount() != 0 {
		t.Errorf("blocks = %v, pending = %d, want the new block freed and nothing owed", r.portBlocks(), r.pendingCount())
	}
}

// A block that exists for another reason than an owed teardown is not owed.
func TestAllocatePortBlock_anExistingBlockThatWasNotOwedIsNotReportedOwed(t *testing.T) {
	r := newEvictRig(t)
	if reAdd(t, r) {
		t.Error("a live member's existing block was reported as owed")
	}
}

// The eviction must not wait the spawn timeout on a node that cannot answer.
func TestEvictMemberAllocations_anInactiveNodeIsNotAsked(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET status = 'offline' WHERE id = 'node1'`)

	if err := r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1"); err == nil {
		t.Fatal("an unconfirmed teardown was reported as done")
	}
	if got := r.sent(); len(got) != 0 {
		t.Errorf("requests = %v, want none to an offline node", got)
	}
	if !r.portBlocks()["node1"] || r.pendingCount() != 1 {
		t.Errorf("blocks = %v, pending = %d, want the block kept and the teardown owed", r.portBlocks(), r.pendingCount())
	}
}

// The owed row must never coexist with the membership it evicts: a replay in
// between would read the node as a member again and drop the teardown.
func TestPruneStaleClusterNodes_theMembershipGoesBeforeTheTeardownIsOwed(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET status = 'inactive', last_seen = datetime('now', '-1 hour') WHERE id = 'node1'`)
	hooked := &hookClient{Client: r.client}
	r.cm.db = hooked
	members := -1
	hooked.after = func(q string) {
		if strings.Contains(q, "INSERT INTO namespace_pending_cleanup") {
			members = r.count(`SELECT COUNT(*) FROM namespace_cluster_nodes WHERE node_id = 'node1'`)
		}
	}

	if _, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if members != 0 {
		t.Errorf("memberships of node1 when its teardown was recorded = %d, want 0", members)
	}
}

// Readers of a cluster's own assignment do not take an owed block for one.
func TestClusterAssignedQuery_anOwedBlockIsNotAnAssignment(t *testing.T) {
	r := owedEvictedRig(t)

	var n int
	if err := r.db.QueryRow(clusterAssignedQuery, "c1", "node1", "c1", "node1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("assignment count of an evicted node = %d, want 0", n)
	}
	if err := r.db.QueryRow(clusterAssignedQuery, "c1", "node2", "c1", "node2").Scan(&n); err != nil || n == 0 {
		t.Errorf("assignment count of a member = %d (%v), want > 0", n, err)
	}
}

func TestRestoreClusterOnNode_anOwedBlockIsNotThisNodesAllocation(t *testing.T) {
	r := newEvictRig(t)
	r.cm.systemdSpawner = &SystemdSpawner{logger: zap.NewNop()}
	r.blockAt("c1", "coordinator", 20000)
	r.exec(`INSERT INTO namespace_pending_cleanup (namespace, node_id, node_ip, action, cluster_id, attempts) VALUES ('acme', 'coordinator', '', ?, 'c1', 1)`, teardownAction)

	err := r.cm.restoreClusterOnNode(context.Background(), "c1", "acme", "10.0.0.9")
	if err == nil || !strings.Contains(err.Error(), "no port allocation found") {
		t.Fatalf("err = %v, want the owed block not to be restored", err)
	}
}

func TestDesiredLocalConfig_anOwedBlockIsNotThisNodesAllocation(t *testing.T) {
	r := newEvictRig(t)
	r.blockAt("c1", "coordinator", 20000)
	r.exec(`INSERT INTO namespace_pending_cleanup (namespace, node_id, node_ip, action, cluster_id, attempts) VALUES ('acme', 'coordinator', '', ?, 'c1', 1)`, teardownAction)

	cfg, err := r.cm.desiredLocalConfig(context.Background(), "c1")
	if err != nil || cfg != nil {
		t.Fatalf("config = %v, err = %v, want nil for a node whose block is owed its teardown", cfg, err)
	}
}
