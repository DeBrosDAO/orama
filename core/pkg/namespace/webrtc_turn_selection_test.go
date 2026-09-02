package namespace

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Bugboard #283. TURN binds the fixed ports 3478/5349, which are exclusive per
// HOST — the allocator already encodes that in HostHasTURN. selectTURNNodes merely
// PREFERRED TURN-free nodes and fell back to hosts that already ran TURN for
// another namespace, producing an allocation that could never start.
//
// On devnet anchat-test held TURN on two of three nodes, so enabling WebRTC for
// anchat-v2 picked the one free node plus an occupied one. That second TURN
// crash-looped on "address already in use", a DNS record was published for it
// anyway — sending roughly half of client ICE attempts to a relay that rejects
// their credentials — and webrtc/status still claimed turn_node_count: 2.
//
// Returning fewer nodes is the honest outcome: less redundancy, but every relay
// the namespace advertises actually works.

// turnSelectionCM builds a ClusterManager whose HostHasTURN answers from busyHosts.
func turnSelectionCM(busyHosts map[string]bool) *ClusterManager {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, args ...any) error {
		if !strings.Contains(query, "service_type = 'turn'") {
			return nil
		}
		busy := false
		if len(args) > 0 {
			if id, ok := args[0].(string); ok {
				busy = busyHosts[id]
			}
		}
		count := 0
		if busy {
			count = 1
		}
		appendToSlice(dest, map[string]any{"Count": count})
		return nil
	}
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

// The exact devnet shape: two of three hosts already run TURN for another
// namespace, so only one node is selectable.
func TestSelectTURNNodes_skipsHostsAlreadyRunningTURN(t *testing.T) {
	cm := turnSelectionCM(map[string]bool{"node-a": true, "node-c": true})

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b", "node-c"), 2)

	if len(got) != 1 {
		t.Fatalf("selected %v, want only node-b — allocating onto an occupied host produces a TURN that crash-loops and a DNS record pointing at it", selectedIDs(got))
	}
	if got[0].NodeID != "node-b" {
		t.Errorf("selected %q, want node-b", got[0].NodeID)
	}
}

// When enough free hosts exist, the requested count is honoured.
func TestSelectTURNNodes_selectsRequestedCountWhenHostsAreFree(t *testing.T) {
	cm := turnSelectionCM(nil)

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b", "node-c"), 2)

	if len(got) != 2 {
		t.Fatalf("selected %v, want 2 free hosts", selectedIDs(got))
	}
}

// count >= len(nodes) used to short-circuit and return every node with no check at
// all — the most direct way to allocate onto an occupied host.
func TestSelectTURNNodes_stillChecksWhenCountCoversAllNodes(t *testing.T) {
	cm := turnSelectionCM(map[string]bool{"node-a": true})

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b"), 2)

	for _, n := range got {
		if n.NodeID == "node-a" {
			t.Fatalf("selected %v — an occupied host must be skipped even when the requested count covers every node", selectedIDs(got))
		}
	}
	if len(got) != 1 {
		t.Errorf("selected %v, want just node-b", selectedIDs(got))
	}
}

// Every host busy: select nothing rather than something that cannot start.
func TestSelectTURNNodes_selectsNothingWhenEveryHostIsBusy(t *testing.T) {
	cm := turnSelectionCM(map[string]bool{"node-a": true, "node-b": true})

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b"), 2)

	if len(got) != 0 {
		t.Errorf("selected %v, want none — no host can run another TURN", selectedIDs(got))
	}
}

// A failed occupancy check must be treated as busy: allocating into a possibly
// occupied host is worse than one relay fewer.
func TestSelectTURNNodes_treatsCheckFailureAsBusy(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "service_type = 'turn'") {
			return context.DeadlineExceeded
		}
		return nil
	}
	logger := zap.NewNop()
	cm := &ClusterManager{db: db, logger: logger, webrtcPortAllocator: NewWebRTCPortAllocator(db, logger)}

	got := cm.selectTURNNodes(context.Background(), nodeList("node-a", "node-b"), 2)

	if len(got) != 0 {
		t.Errorf("selected %v despite being unable to verify host occupancy", selectedIDs(got))
	}
}
