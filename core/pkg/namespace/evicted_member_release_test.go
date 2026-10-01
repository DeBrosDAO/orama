package namespace

import (
	"context"
	"errors"
	"testing"
)

// A cluster that lives on and stops using a node must free that node's port
// reservations only once the node's units are confirmed stopped; freeing them
// under units that still run hands the ports to the next namespace, whose
// services then crash-loop on "address already in use" (bugboard #275).

// newEvictRig is cluster c1 of "acme" on node1 (remote, with a core block and an
// SFU allocation) and node2 (a core block), both active.
func newEvictRig(t *testing.T) *registryRig {
	t.Helper()
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	for _, n := range []string{"node1", "node2"} {
		r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES (?, '192.0.2.1', ?, 'active')`, n, "10.0.0."+n[len(n)-1:])
		r.membership("c1", n)
		r.blockAt("c1", n, 10000)
	}
	r.webrtc("c1", "node1", "sfu")
	return r
}

func TestEvictMemberAllocations_anUnconfirmedTeardownKeepsTheBlockAndIsOwed(t *testing.T) {
	r := newEvictRig(t)
	r.sendErr = errors.New("node unreachable")
	r.exec(`DELETE FROM namespace_cluster_nodes WHERE node_id = 'node1'`) // evicted members leave the membership first

	err := r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1")
	if err == nil {
		t.Fatal("an unconfirmed teardown was reported as done")
	}
	if !r.portBlocks()["node1"] || !r.hasWebRTCRow("node1") {
		t.Error("the unconfirmed node's reservations were freed under units that may still run")
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE node_id = 'node1' AND cluster_id = 'c1' AND action = ?`, teardownAction); got != 1 {
		t.Errorf("owed teardowns of node1 = %d, want 1 recorded with the cluster id", got)
	}

	r.sendErr = nil
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if r.portBlocks()["node1"] || r.hasWebRTCRow("node1") || r.pendingCount() != 0 {
		t.Error("the replayed teardown did not free the node's reservations and clear the record")
	}
	if !r.portBlocks()["node2"] {
		t.Error("the replay freed a node that was not evicted")
	}
}

func TestEvictMemberAllocations_aConfirmedTeardownFreesTheBlock(t *testing.T) {
	r := newEvictRig(t)

	if err := r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1"); err != nil {
		t.Fatal(err)
	}
	if r.portBlocks()["node1"] || r.hasWebRTCRow("node1") {
		t.Error("a confirmed node kept its reservations")
	}
	if !r.portBlocks()["node2"] || r.pendingCount() != 0 {
		t.Errorf("blocks = %v, pending = %d, want node2's block kept and nothing owed", r.portBlocks(), r.pendingCount())
	}
	if got := r.sent(); len(got) != 1 || got[0] != "teardown-namespace:acme@node1" {
		t.Errorf("requests = %v, want one teardown sent to node1", got)
	}
}

func TestEvictMemberAllocations_aNodeGoneFromTheRegistryFreesTheBlock(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`DELETE FROM dns_nodes WHERE id = 'node1'`)

	if err := r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1"); err != nil {
		t.Fatal(err)
	}
	if r.portBlocks()["node1"] || len(r.sent()) != 0 || r.pendingCount() != 0 {
		t.Errorf("blocks = %v, requests = %v, pending = %d, want the block freed with nothing sent or owed", r.portBlocks(), r.sent(), r.pendingCount())
	}
}

func TestEvictMemberAllocations_aNodeWithoutAnOverlayAddressIsOwed(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET internal_ip = '', ip_address = '' WHERE id = 'node1'`)

	if err := r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1"); err == nil {
		t.Fatal("a node that could not be asked was reported as confirmed")
	}
	if !r.portBlocks()["node1"] || r.pendingCount() != 1 {
		t.Errorf("blocks = %v, pending = %d, want the block kept and the teardown owed", r.portBlocks(), r.pendingCount())
	}
}

// Bug: pruning dropped the block of a member silent for 15 minutes without
// asking it to stop. It is unconfirmed, so the block stays and the member is
// out of every read of the cluster's members.
func TestPruneStaleClusterNodes_aMemberThatDoesNotConfirmKeepsItsBlock(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET status = 'inactive', last_seen = datetime('now', '-1 hour') WHERE id = 'node1'`)
	r.sendErr = errors.New("node unreachable")

	removed, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "node1" {
		t.Fatalf("removed = %v, want node1", removed)
	}
	if !r.portBlocks()["node1"] || r.pendingCount() != 1 {
		t.Errorf("blocks = %v, pending = %d, want node1's block kept and its teardown owed", r.portBlocks(), r.pendingCount())
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_cluster_nodes WHERE node_id = 'node1'`); got != 0 {
		t.Error("the evicted member is still a member")
	}

	_, blocks, err := r.cm.clusterStateInputs(context.Background(), &NamespaceCluster{ID: "c1", NamespaceName: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.NodeID == "node1" {
			t.Error("the evicted member reappeared in the cluster state through its kept block")
		}
	}
}

// An inactive member cannot answer: it is not asked (a request to it blocks for
// the whole spawn timeout), its teardown is owed and its block stays.
func TestPruneStaleClusterNodes_anInactiveMemberIsNotAskedAndItsTeardownIsOwed(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET status = 'inactive', last_seen = datetime('now', '-1 hour') WHERE id = 'node1'`)

	if _, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 0 {
		t.Errorf("requests = %v, want none to the inactive node", got)
	}
	if !r.portBlocks()["node1"] || r.pendingCount() != 1 {
		t.Errorf("blocks = %v, pending = %d, want the block kept and the teardown owed", r.portBlocks(), r.pendingCount())
	}
}

// The cluster is given the evicted node again before the teardown is replayed:
// the replay must not tear down the member it now is.
func TestAllocatePortBlock_givenToTheClusterAgainWithdrawsItsOwnOwedTeardown(t *testing.T) {
	r := newEvictRig(t)
	r.sendErr = errors.New("node unreachable")
	_ = r.cm.evictMemberAllocations(context.Background(), "c1", "acme", "node1")
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, r.cm.logger)

	if _, owed, err := r.cm.portAllocator.AllocatePortBlock(context.Background(), "node1", "c1", BlueprintTenant()); err != nil {
		t.Fatal(err)
	} else if !owed {
		t.Error("the block given back was not reported as owed")
	}
	if r.pendingCount() != 0 {
		t.Error("the teardown owed for this very cluster survived the node being given the cluster again")
	}
}

// Bug: a stale-provisioning cluster freed the blocks of inactive nodes, which
// were never asked to stop and may still run their units.
func TestFailStaleCluster_anInactiveNodeKeepsItsBlockAndIsOwed(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE namespace_clusters SET status = 'provisioning', provisioned_at = datetime('now', '-1 day') WHERE id = 'c1'`)
	r.exec(`UPDATE dns_nodes SET status = 'inactive' WHERE id = 'node1'`)
	r.cm.dnsManager = NewDNSRecordManager(r.client, "example.test", r.cm.logger)

	if err := r.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_clusters WHERE id = 'c1' AND status = 'failed'`); got != 1 {
		t.Fatal("the stale cluster was not failed")
	}
	if !r.portBlocks()["node1"] || !r.hasWebRTCRow("node1") {
		t.Error("the inactive node's reservations were freed without a confirmed stop")
	}
	if r.portBlocks()["node2"] {
		t.Error("the confirmed node's block was kept")
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE node_id = 'node1' AND cluster_id = 'c1'`); got != 1 {
		t.Errorf("owed teardowns of the inactive node = %d, want 1 with the cluster id", got)
	}

	r.exec(`UPDATE dns_nodes SET status = 'active' WHERE id = 'node1'`)
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if r.portBlocks()["node1"] || r.hasWebRTCRow("node1") || r.pendingCount() != 0 {
		t.Error("the replay did not free the node's reservations")
	}
}

func TestFailStaleCluster_confirmedNodesLoseTheirBlocks(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE namespace_clusters SET status = 'provisioning', provisioned_at = datetime('now', '-1 day') WHERE id = 'c1'`)
	r.cm.dnsManager = NewDNSRecordManager(r.client, "example.test", r.cm.logger)

	if err := r.cm.failStaleProvisioning(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.portBlocks()) != 0 || r.hasWebRTCRow("node1") || r.pendingCount() != 0 {
		t.Errorf("blocks = %v, pending = %d, want everything freed after a confirmed teardown", r.portBlocks(), r.pendingCount())
	}
}
