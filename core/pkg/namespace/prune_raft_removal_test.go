package namespace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// A namespace node that was replaced stayed in the namespace's raft
// configuration: pruneStaleClusterNodes forgot the member (and freed the port
// allocation its raft address is built from) without removing it from raft, and
// only one of the three callers that prune removed it. The registry then listed
// three members and raft four, so every restart of the namespace rewrote a
// recovery peers.json.

func staleNode1(r *registryRig) {
	r.exec(`UPDATE dns_nodes SET status = 'inactive', last_seen = datetime('now', '-1 hour') WHERE id = 'node1'`)
}

func TestPruneStaleClusterNodes_removesTheDepartedMemberFromRaft(t *testing.T) {
	r := newEvictRig(t)
	staleNode1(r)

	removed, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "node1" {
		t.Fatalf("removed = %v, want node1", removed)
	}
	if want := []string{"10.0.0.1:2"}; !slices.Equal(r.raftRemovals, want) {
		t.Errorf("raft removals = %v, want %v: the departed member stays a configured voter", r.raftRemovals, want)
	}
}

// The replacement of a node can land on the same host under another port block.
// Only the departed member's own address leaves raft; the live member on the
// same host stays.
func TestPruneStaleClusterNodes_sameHostDifferentPortIsTheDepartedMember(t *testing.T) {
	r := newEvictRig(t)
	staleNode1(r)
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('node3', '192.0.2.1', '10.0.0.1', 'active')`)
	r.exec(`INSERT INTO namespace_port_allocations (id, node_id, namespace_cluster_id, port_start, port_end, rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
		VALUES ('p-node3', 'node3', 'c1', 20000, 20004, 6, 7, 8, 9, 10)`)
	r.membership("c1", "node3")

	if _, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"10.0.0.1:2"}; !slices.Equal(r.raftRemovals, want) {
		t.Errorf("raft removals = %v, want only the departed %v", r.raftRemovals, want)
	}
}

// A removal that fails must not make the registry forget the member: nothing
// would name its raft address afterwards. It stays, and the next sweep retries.
func TestPruneStaleClusterNodes_aFailedRaftRemovalKeepsTheMemberForTheNextSweep(t *testing.T) {
	r := newEvictRig(t)
	staleNode1(r)
	r.raftErr = errors.New("no leader")

	removed, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none while raft still holds the member", removed)
	}
	if got := r.count(`SELECT COUNT(*) FROM namespace_cluster_nodes WHERE node_id = 'node1'`); got == 0 {
		t.Error("the member was forgotten although raft still holds it")
	}
	if !r.portBlocks()["node1"] {
		t.Error("the member's allocation was freed although raft still holds it")
	}

	r.raftErr = nil
	if removed, err = r.cm.pruneStaleClusterNodes(context.Background(), "c1"); err != nil || len(removed) != 1 {
		t.Fatalf("retry removed %v, %v; want node1", removed, err)
	}
	if want := []string{"10.0.0.1:2"}; !slices.Equal(r.raftRemovals, want) {
		t.Errorf("raft removals = %v, want %v", r.raftRemovals, want)
	}
}

// pruneAndDeraft, the tenant sweep's leg, goes through the same prune.
func TestPruneAndDeraft_removesTheDepartedMemberFromRaft(t *testing.T) {
	r := newEvictRig(t)
	staleNode1(r)

	if err := r.cm.pruneAndDeraft(context.Background(), tenantAssignment{ClusterID: "c1", NamespaceName: "acme"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"10.0.0.1:2"}; !slices.Equal(r.raftRemovals, want) {
		t.Errorf("raft removals = %v, want %v", r.raftRemovals, want)
	}
}

// A member that is not stale is never removed from raft.
func TestPruneStaleClusterNodes_aLiveMemberStaysInRaft(t *testing.T) {
	r := newEvictRig(t)

	if _, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if len(r.raftRemovals) != 0 {
		t.Errorf("raft removals = %v, want none", r.raftRemovals)
	}
}

func TestRemoveDeadNodeFromRaft_reportsWhenNoSurvivorAccepts(t *testing.T) {
	r := newEvictRig(t)
	r.raftErr = errors.New("no leader")
	survivors := []survivingNodePorts{
		{NodeID: "node2", InternalIP: "10.0.0.2", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
		{NodeID: "node9", InternalIP: "10.0.0.9", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
	}

	if err := r.cm.removeDeadNodeFromRaft(context.Background(), "10.0.0.1:2", survivors); err == nil {
		t.Error("a removal no survivor accepted was reported as done")
	}
	if err := r.cm.removeDeadNodeFromRaft(context.Background(), "10.0.0.1:2", nil); err == nil {
		t.Error("a removal with no survivor to ask was reported as done")
	}
	if err := r.cm.removeDeadNodeFromRaft(context.Background(), "", survivors); err != nil {
		t.Errorf("an empty address is nothing to remove, got %v", err)
	}
}

// Raft ids are addresses: removing the address of a live member removes the
// live member. A dead member whose registry row names the same overlay address
// as a survivor must never be removed.
func TestRemoveDeadNodeFromRaft_refusesTheAddressOfASurvivor(t *testing.T) {
	r := newEvictRig(t)
	survivors := []survivingNodePorts{
		{NodeID: "node2", InternalIP: "10.0.0.2", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
		{NodeID: "node9", InternalIP: "10.0.0.9", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
	}

	err := r.cm.removeDeadNodeFromRaft(context.Background(), "10.0.0.2:2", survivors)
	if err == nil || !strings.Contains(err.Error(), "surviving member node2") {
		t.Fatalf("err = %v, want a refusal naming the surviving member", err)
	}
	if len(r.raftRemovals) != 0 {
		t.Errorf("raft removals = %v, want none", r.raftRemovals)
	}
}

func TestRemoveDeadNodeFromRaft_refusesANonOverlayAddress(t *testing.T) {
	r := newEvictRig(t)
	survivors := []survivingNodePorts{
		{NodeID: "node2", InternalIP: "10.0.0.2", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
		{NodeID: "node9", InternalIP: "10.0.0.9", RQLiteHTTPPort: 1, RQLiteRaftPort: 2},
	}
	for _, addr := range []string{"192.0.2.1:2", "not-an-address", "example.com:2"} {
		if err := r.cm.removeDeadNodeFromRaft(context.Background(), addr, survivors); err == nil {
			t.Errorf("removing %q was allowed", addr)
		}
	}
	if len(r.raftRemovals) != 0 {
		t.Errorf("raft removals = %v, want none", r.raftRemovals)
	}
}

// With two of three voters gone the removal cannot commit: no leader. It is
// refused up front with the recovery procedure, not retried silently.
func TestRemoveDeadNodeFromRaft_refusesWhenQuorumIsLost(t *testing.T) {
	r := newEvictRig(t)
	survivors := []survivingNodePorts{{NodeID: "node2", InternalIP: "10.0.0.2", RQLiteHTTPPort: 1, RQLiteRaftPort: 2}}

	err := r.cm.removeDeadNodeFromRaft(context.Background(), "10.0.0.1:2", survivors)
	if err == nil || !strings.Contains(err.Error(), "lost quorum") || !strings.Contains(err.Error(), "NODE_REPLACEMENT.md") {
		t.Fatalf("err = %v, want a quorum-loss refusal naming the recovery procedure", err)
	}
	if len(r.raftRemovals) != 0 {
		t.Errorf("raft removals = %v, want none", r.raftRemovals)
	}
	// No survivor at all is the same condition, reported the same way.
	err = r.cm.removeDeadNodeFromRaft(context.Background(), "10.0.0.1:2", nil)
	if err == nil || !strings.Contains(err.Error(), "lost quorum") {
		t.Fatalf("no survivors: err = %v, want the quorum-loss refusal", err)
	}
}

// The prune of a member of a cluster that has lost quorum keeps the member
// registered (the removal is retried once the namespace is recovered).
func TestPruneStaleClusterNodes_aQuorumLostClusterKeepsTheMember(t *testing.T) {
	r := newEvictRig(t)
	staleNode1(r)
	r.exec(`UPDATE dns_nodes SET status = 'inactive' WHERE id = 'node9'`)

	removed, err := r.cm.pruneStaleClusterNodes(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 || len(r.raftRemovals) != 0 {
		t.Fatalf("removed = %v, raft removals = %v, want none", removed, r.raftRemovals)
	}
}

func TestMemberRaftAddr_noInternalIPIsAnErrorNotThePublicAddress(t *testing.T) {
	r := newEvictRig(t)
	r.exec(`UPDATE dns_nodes SET internal_ip = NULL WHERE id = 'node1'`)

	addr, err := r.cm.memberRaftAddr(context.Background(), "c1", "node1")
	if err == nil {
		t.Fatalf("addr = %q, want an error: the public ip_address is not where raft listens", addr)
	}
	if addr, err = r.cm.memberRaftAddr(context.Background(), "c1", "node2"); err != nil || addr != "10.0.0.2:2" {
		t.Fatalf("addr, err = %q, %v; want the overlay address", addr, err)
	}
}
