package rqlite

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/discovery"
)

// A cluster part-way through `orama node migrate-raft-id` holds members under
// two kinds of id at once: migrated nodes under their libp2p peer id, the rest
// under their raft address. Every path that reads membership has to cope, and
// the ones that did not were the ones that added a duplicate voter for each
// migrated node every five minutes.

const (
	migratedID   = "12D3KooWMtjibhBBfgbhN9Vio4jTFDeD1NLZCSxG98B6nTsoscRy"
	migratedAddr = "10.0.0.2:10101"
	legacyAddr   = "10.0.0.3:10101"
)

func mixedMembers() RQLiteNodes {
	return RQLiteNodes{
		{ID: migratedID, Addr: migratedAddr, Voter: true, Reachable: true},
		{ID: legacyAddr, Addr: legacyAddr, Voter: true, Reachable: true},
	}
}

func TestRaftNodeSet_matchesAMigratedNodeUnderEitherName(t *testing.T) {
	// Orphan recovery decides a peer is missing by failing to find it. On a
	// mixed cluster a peer may announce either name, so both must match — a
	// miss means the node is re-added a SECOND time under its other name.
	set := make(map[string]bool)
	for _, n := range mixedMembers() {
		set[n.ID] = true
		if n.Addr != "" {
			set[n.Addr] = true
		}
	}

	for _, name := range []string{migratedID, migratedAddr, legacyAddr} {
		if !set[name] {
			t.Fatalf("%q was not recognised as already in the cluster", name)
		}
	}
}

func TestRQLiteNode_decodesTheAddressFieldRQLiteActuallySends(t *testing.T) {
	// rqlite's /nodes calls this field "addr". It was decoded as "address", so
	// it was silently always empty and every consumer that wanted a member's
	// address reached for its id instead.
	nodes, err := decodeNodes([]byte(`{"nodes":[{"id":"12D3KooWAlpha","addr":"10.0.0.2:10101","voter":true,"reachable":true}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes", len(nodes))
	}
	if nodes[0].Addr != "10.0.0.2:10101" {
		t.Fatalf("Addr = %q, want the address rqlite sent", nodes[0].Addr)
	}
}

func TestSafeToRemoveMember_allowsRemovingAReachableVoter(t *testing.T) {
	// A decommission and an identity migration both remove a member that is up
	// on purpose. The eviction rule refuses exactly that, and using it meant
	// the migration could not execute a single step on a healthy cluster.
	members := []RaftMember{
		{ID: "a", Addr: "10.0.0.1:10101", Voter: true, Reachable: true},
		{ID: "b", Addr: "10.0.0.2:10101", Voter: true, Reachable: true},
		{ID: "c", Addr: "10.0.0.3:10101", Voter: true, Reachable: true},
	}

	if refusal := SafeToRemoveMember(members, "a"); refusal != "" {
		t.Fatalf("removing a reachable voter from a healthy 3-node cluster was refused: %s", refusal)
	}
	if refusal := SafeToRemoveVoter(members, "a"); refusal == "" {
		t.Fatal("the EVICTION rule must still refuse a reachable member")
	}
}

func TestSafeToRemoveMember_stillProtectsQuorum(t *testing.T) {
	// The arithmetic has to survive the split. Two of three voters already
	// unreachable means removing the last good one leaves nothing.
	members := []RaftMember{
		{ID: "a", Voter: true, Reachable: true},
		{ID: "b", Voter: true, Reachable: false},
		{ID: "c", Voter: true, Reachable: false},
	}

	if refusal := SafeToRemoveMember(members, "a"); refusal == "" {
		t.Fatal("removing the only reachable voter of three was allowed")
	}
}

func TestSafeToRemoveMember_edgeCases(t *testing.T) {
	tests := []struct {
		name    string
		members []RaftMember
		target  string
		refuse  bool
	}{
		{"not in the configuration", []RaftMember{{ID: "a", Voter: true, Reachable: true}}, "zz", true},
		{"non-voter gains nothing", []RaftMember{
			{ID: "a", Voter: true, Reachable: true},
			{ID: "b", Voter: false, Reachable: true},
		}, "b", true},
		{"last voter", []RaftMember{{ID: "a", Voter: true, Reachable: true}}, "a", true},
		{"empty configuration", nil, "a", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refusal := SafeToRemoveMember(tc.members, tc.target)
			if (refusal != "") != tc.refuse {
				t.Fatalf("refusal = %q, want refuse=%v", refusal, tc.refuse)
			}
		})
	}
}

func TestIsSelfPeer_matchesOnAddressNotID(t *testing.T) {
	// Self-detection compared the announced node id against this node's raft
	// address. That works only while an id IS an address: the moment a node
	// announces a stable peer id it stops recognising itself and starts
	// counting itself as a peer.
	self := &discovery.RQLiteNodeMetadata{NodeID: migratedID, RaftAddress: migratedAddr}
	other := &discovery.RQLiteNodeMetadata{NodeID: legacyAddr, RaftAddress: legacyAddr}

	if !isSelfPeer(self, migratedAddr) {
		t.Fatal("a migrated node did not recognise its own announcement")
	}
	if isSelfPeer(other, migratedAddr) {
		t.Fatal("another node was mistaken for self")
	}
	if isSelfPeer(nil, migratedAddr) || isSelfPeer(self, "") {
		t.Fatal("nil metadata or an empty self address must not match")
	}
}

func TestRaftIDOf_prefersTheAnnouncedID(t *testing.T) {
	// peers.json resets the raft configuration to what it contains, so writing
	// addresses as ids would revert every migrated node in one step.
	migrated := &discovery.RQLiteNodeMetadata{NodeID: migratedID, RaftAddress: migratedAddr}
	if got := raftIDOf(migrated); got != migratedID {
		t.Fatalf("got %q, want the announced peer id", got)
	}

	legacy := &discovery.RQLiteNodeMetadata{RaftAddress: legacyAddr}
	if got := raftIDOf(legacy); got != legacyAddr {
		t.Fatalf("got %q, want the raft address as the fallback", got)
	}
	if got := raftIDOf(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestRewriteAdvertisedAddresses_leavesAStableIDAlone(t *testing.T) {
	// This is what made the whole change a no-op. The rewrite's condition
	// included `meta.NodeID == originalNodeID`, which is vacuously true —
	// originalNodeID is captured from meta.NodeID a few lines above — so the id
	// was stamped back to the raft address on every single announcement,
	// including one that had already been set to a stable peer id.
	meta := &discovery.RQLiteNodeMetadata{
		NodeID:      migratedID,
		RaftAddress: "127.0.0.1:10101",
		HTTPAddress: "127.0.0.1:10100",
	}

	rewriteAdvertisedAddresses(meta, "10.0.0.2", true)

	if meta.NodeID != migratedID {
		t.Fatalf("NodeID = %q, want the peer id to survive the address rewrite", meta.NodeID)
	}
	if meta.RaftAddress != "10.0.0.2:10101" {
		t.Fatalf("RaftAddress = %q, want the host replaced", meta.RaftAddress)
	}
}

func TestRewriteAdvertisedAddresses_stillFixesAnAddressDerivedID(t *testing.T) {
	// A node that has not migrated announces its address as its id, and both
	// must follow the overlay rewrite together — otherwise it advertises an id
	// the cluster does not have it under.
	meta := &discovery.RQLiteNodeMetadata{
		NodeID:      "127.0.0.1:10101",
		RaftAddress: "127.0.0.1:10101",
		HTTPAddress: "127.0.0.1:10100",
	}

	rewriteAdvertisedAddresses(meta, "10.0.0.3", true)

	if meta.NodeID != "10.0.0.3:10101" {
		t.Fatalf("NodeID = %q, want it rewritten alongside the address", meta.NodeID)
	}
}

func TestRewriteAdvertisedAddresses_emptyIDTakesTheAddress(t *testing.T) {
	meta := &discovery.RQLiteNodeMetadata{RaftAddress: "127.0.0.1:10101"}
	rewriteAdvertisedAddresses(meta, "10.0.0.4", true)
	if meta.NodeID != "10.0.0.4:10101" {
		t.Fatalf("NodeID = %q, want the rewritten address", meta.NodeID)
	}
}
