package namespace

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #283, part 2 — this file previously pinned the OPPOSITE behavior, and
// the reversal is the fix.
//
// TURN binds the fixed ports 3478/5349, which are exclusive per HOST. While each
// namespace ran its own TURN process, a host could serve only one of them, so
// part 1 made selectTURNNodes skip hosts already running TURN for someone else:
// fewer relays, but every relay advertised actually worked.
//
// That capped a namespace at the hosts nobody had claimed. On the 3-node devnet
// fleet anchat-test held TURN on two nodes, so anchat-v2 could only ever get one
// relay and no redundancy — the ceiling the ticket is actually about.
//
// Part 2 removes the ceiling instead of managing it: a single shared TURN server
// per host serves every namespace allocated there, authenticating each against
// its own secret. Host occupancy is no longer a reason to skip a node, so the
// exclusion is gone and these tests now pin its absence. Which namespaces a host
// serves is reconciled by ReconcileHostTURN.

func selectionCM() *ClusterManager {
	db := &recoveryMockDB{}
	logger := zap.NewNop()
	return &ClusterManager{
		db:                  db,
		logger:              logger,
		webrtcPortAllocator: NewWebRTCPortAllocator(db, logger),
	}
}

func nodeList(ids ...string) []clusterNodeInfo {
	out := make([]clusterNodeInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, clusterNodeInfo{NodeID: id})
	}
	return out
}

func selectedIDs(nodes []clusterNodeInfo) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.NodeID)
	}
	return out
}

// The fix: a host already relaying for another namespace is a perfectly good
// choice now, because one shared server there serves both.
func TestSelectTURNNodes_usesHostsThatAlreadyRunTURN(t *testing.T) {
	cm := selectionCM()

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b"), 2)

	if len(got) != 2 {
		t.Fatalf("selected %v, want both nodes — a host running TURN for another namespace can serve this one too; skipping it is the #283 ceiling", selectedIDs(got))
	}
}

// A namespace must be able to reach full redundancy on a fleet where every host
// already relays for someone else. This is the devnet case that motivated the
// ticket: anchat-v2 got one relay because anchat-test held the other two hosts.
func TestSelectTURNNodes_reachesFullRedundancyOnAFullyOccupiedFleet(t *testing.T) {
	cm := selectionCM()

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b", "node-c"), 3)

	if len(got) != 3 {
		t.Errorf("selected %d of 3 nodes (%v) — the second namespace on a fleet still cannot get redundancy", len(got), selectedIDs(got))
	}
}

// Selection is still bounded by the requested count.
func TestSelectTURNNodes_honoursTheRequestedCount(t *testing.T) {
	cm := selectionCM()

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b", "node-c"), 2)

	if len(got) != 2 {
		t.Errorf("selected %v, want exactly 2", selectedIDs(got))
	}
}

// Asking for more nodes than exist yields what exists, not a panic or a
// duplicate — the caller records the real count.
func TestSelectTURNNodes_returnsFewerWhenTheFleetIsSmaller(t *testing.T) {
	cm := selectionCM()

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a"), 3)

	if len(got) != 1 || got[0].NodeID != "node-a" {
		t.Errorf("selected %v, want [node-a]", selectedIDs(got))
	}
}

// Zero candidates must yield zero, not a panic.
func TestSelectTURNNodes_emptyNodeListIsEmpty(t *testing.T) {
	cm := selectionCM()

	if got := cm.selectTURNNodes(context.Background(), nil, 2); len(got) != 0 {
		t.Errorf("selected %v from an empty fleet", selectedIDs(got))
	}
}

// A zero count selects nothing — WebRTC enabled with turn_node_count 0 must not
// quietly allocate a relay.
func TestSelectTURNNodes_zeroCountSelectsNothing(t *testing.T) {
	cm := selectionCM()

	if got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b"), 0); len(got) != 0 {
		t.Errorf("selected %v for a requested count of 0", selectedIDs(got))
	}
}
