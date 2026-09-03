package membership

import (
	"reflect"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

func node(peerID, ip string, lastSeen time.Time) Node {
	return Node{PeerID: peerID, InternalIP: ip, Status: "active", LastSeen: lastSeen}
}

func wgRow(ip string) WireGuardRow {
	return WireGuardRow{NodeID: "node-" + ip, WGIP: ip, PublicKey: "key-" + ip}
}

func TestBuildPlan_leavesALiveFleetAlone(t *testing.T) {
	e := Evidence{
		Nodes:         []Node{node("peerA", "10.0.0.1", ago(time.Minute)), node("peerB", "10.0.0.2", ago(time.Minute))},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.1"), wgRow("10.0.0.2")},
		Discovered:    map[string]struct{}{"peerA": {}, "peerB": {}},
		Now:           now,
	}

	if plan := BuildPlan(e); !plan.Empty() {
		t.Fatalf("a healthy fleet produced changes: %+v", plan)
	}
}

// Deleting a WireGuard row for a live node severs it from the mesh, and raft
// runs over the mesh. Every signal that the node is alive must veto removal.
func TestBuildPlan_neverDropsALiveNode(t *testing.T) {
	tests := []struct {
		name     string
		lastSeen time.Time
		seen     bool
	}{
		{"discovery can see it", ago(48 * time.Hour), true},
		{"heartbeat is recent", ago(time.Minute), false},
		{"heartbeat is inside the grace window", ago(LivenessGrace - time.Minute), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			discovered := map[string]struct{}{}
			if tc.seen {
				discovered["peerA"] = struct{}{}
			}
			e := Evidence{
				Nodes:         []Node{node("peerA", "10.0.0.1", tc.lastSeen)},
				WireGuardRows: []WireGuardRow{wgRow("10.0.0.1")},
				// Even an explicit tombstone must not override liveness.
				Tombstoned: map[string]time.Time{"peerA": ago(24 * time.Hour)},
				Discovered: discovered,
				Now:        now,
			}

			plan := BuildPlan(e)
			if len(plan.DropWireGuardPeers) != 0 || len(plan.DropDNSNodes) != 0 {
				t.Fatalf("a node that is alive was scheduled for removal: %+v", plan)
			}
		})
	}
}

func TestBuildPlan_dropsWireGuardRowForATombstonedNode(t *testing.T) {
	e := Evidence{
		Nodes: []Node{
			node("peerA", "10.0.0.1", ago(time.Minute)),
			node("peerGone", "10.0.0.9", ago(3*time.Hour)),
		},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.1"), wgRow("10.0.0.9")},
		Tombstoned:    map[string]time.Time{"peerGone": ago(time.Hour)},
		Discovered:    map[string]struct{}{"peerA": {}},
		Now:           now,
	}

	plan := BuildPlan(e)
	if want := []string{"node-10.0.0.9"}; !reflect.DeepEqual(plan.DropWireGuardPeers, want) {
		t.Fatalf("DropWireGuardPeers = %v, want %v", plan.DropWireGuardPeers, want)
	}
	// Still inside TombstoneGrace, so the dns_nodes row stays for now.
	if len(plan.DropDNSNodes) != 0 {
		t.Fatalf("dropped a dns_nodes row before the grace period: %v", plan.DropDNSNodes)
	}
}

func TestBuildPlan_dropsDNSNodeOnceTheTombstoneGraceHasPassed(t *testing.T) {
	e := Evidence{
		Nodes:      []Node{node("peerGone", "10.0.0.9", ago(24*time.Hour))},
		Tombstoned: map[string]time.Time{"peerGone": ago(TombstoneGrace + time.Hour)},
		Discovered: map[string]struct{}{},
		Now:        now,
	}

	plan := BuildPlan(e)
	if want := []string{"peerGone"}; !reflect.DeepEqual(plan.DropDNSNodes, want) {
		t.Fatalf("DropDNSNodes = %v, want %v", plan.DropDNSNodes, want)
	}
}

// A node that simply stopped answering is missing, not gone. Something
// deliberate — the raft eviction, an operator decommission — has to establish
// departure, and that writes a tombstone.
func TestBuildPlan_missingWithoutATombstoneIsNotDeparture(t *testing.T) {
	e := Evidence{
		Nodes:         []Node{node("peerQuiet", "10.0.0.7", ago(72*time.Hour))},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.7")},
		Tombstoned:    map[string]time.Time{},
		Discovered:    map[string]struct{}{},
		Now:           now,
	}

	if plan := BuildPlan(e); !plan.Empty() {
		t.Fatalf("an unexplained disappearance was treated as departure: %+v", plan)
	}
}

// The obvious rule — delete WireGuard rows whose node_id matches no dns_nodes
// id — would today delete EVERY row, because the join handler writes a
// synthetic "node-<wgip>". Matching is on the overlay address, and a row that
// still finds no match is only reported.
func TestBuildPlan_orphanRowsAreReportedNotDropped(t *testing.T) {
	e := Evidence{
		Nodes:         []Node{node("peerA", "10.0.0.1", ago(time.Minute))},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.1"), wgRow("10.0.0.42")},
		Discovered:    map[string]struct{}{"peerA": {}},
		Now:           now,
	}

	plan := BuildPlan(e)
	if want := []string{"node-10.0.0.42"}; !reflect.DeepEqual(plan.OrphanWireGuardPeers, want) {
		t.Fatalf("OrphanWireGuardPeers = %v, want %v", plan.OrphanWireGuardPeers, want)
	}
	if len(plan.DropWireGuardPeers) != 0 {
		t.Fatalf("an orphan row was dropped; a node mid-join has a WireGuard row before a dns_nodes row: %v",
			plan.DropWireGuardPeers)
	}
	if !plan.Empty() {
		t.Fatal("a plan that only reports orphans must count as empty")
	}
}

// The synthetic node id is exactly why matching cannot be done on it.
func TestBuildPlan_matchesOnOverlayAddressNotNodeID(t *testing.T) {
	e := Evidence{
		Nodes: []Node{{PeerID: "12D3KooWReal", InternalIP: "10.0.0.4", Status: "active", LastSeen: ago(time.Minute)}},
		// node_id bears no relation to the peer id, as the join handler writes it.
		WireGuardRows: []WireGuardRow{{NodeID: "node-10.0.0.4", WGIP: "10.0.0.4", PublicKey: "k"}},
		Discovered:    map[string]struct{}{"12D3KooWReal": {}},
		Now:           now,
	}

	plan := BuildPlan(e)
	if len(plan.OrphanWireGuardPeers) != 0 {
		t.Fatalf("matching on node_id would orphan every row; got %v", plan.OrphanWireGuardPeers)
	}
}

func TestBuildPlan_isDeterministic(t *testing.T) {
	e := Evidence{
		Nodes: []Node{
			node("peerC", "10.0.0.3", ago(9*time.Hour)),
			node("peerA", "10.0.0.1", ago(9*time.Hour)),
			node("peerB", "10.0.0.2", ago(9*time.Hour)),
		},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.3"), wgRow("10.0.0.1"), wgRow("10.0.0.2")},
		Tombstoned: map[string]time.Time{
			"peerA": ago(TombstoneGrace + time.Hour),
			"peerB": ago(TombstoneGrace + time.Hour),
			"peerC": ago(TombstoneGrace + time.Hour),
		},
		Discovered: map[string]struct{}{},
		Now:        now,
	}

	first := BuildPlan(e)
	for i := 0; i < 5; i++ {
		if got := BuildPlan(e); !reflect.DeepEqual(got, first) {
			t.Fatalf("plan %d differs: %+v vs %+v", i, got, first)
		}
	}
	if want := []string{"peerA", "peerB", "peerC"}; !reflect.DeepEqual(first.DropDNSNodes, want) {
		t.Fatalf("DropDNSNodes = %v, want %v sorted", first.DropDNSNodes, want)
	}
}

func TestBuildPlan_emptyEvidence(t *testing.T) {
	if plan := BuildPlan(Evidence{Now: now}); !plan.Empty() {
		t.Fatalf("empty evidence produced changes: %+v", plan)
	}
}

// A node with no internal_ip cannot be matched to a WireGuard row; it must not
// silently make every row look orphaned.
func TestBuildPlan_nodeWithoutAnOverlayAddress(t *testing.T) {
	e := Evidence{
		Nodes:         []Node{{PeerID: "peerA", InternalIP: "", Status: "active", LastSeen: ago(time.Minute)}},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.1")},
		Discovered:    map[string]struct{}{"peerA": {}},
		Now:           now,
	}

	plan := BuildPlan(e)
	if len(plan.DropWireGuardPeers) != 0 {
		t.Fatalf("dropped a row for a node with no overlay address: %v", plan.DropWireGuardPeers)
	}
	if want := []string{"node-10.0.0.1"}; !reflect.DeepEqual(plan.OrphanWireGuardPeers, want) {
		t.Fatalf("OrphanWireGuardPeers = %v, want %v", plan.OrphanWireGuardPeers, want)
	}
}

// A node with no heartbeat at all (zero LastSeen) must not be protected by the
// liveness grace — otherwise a row written and never updated is immortal.
func TestBuildPlan_zeroLastSeenIsNotLiveness(t *testing.T) {
	e := Evidence{
		Nodes:         []Node{{PeerID: "peerGone", InternalIP: "10.0.0.9", Status: "offline"}},
		WireGuardRows: []WireGuardRow{wgRow("10.0.0.9")},
		Tombstoned:    map[string]time.Time{"peerGone": ago(time.Hour)},
		Discovered:    map[string]struct{}{},
		Now:           now,
	}

	plan := BuildPlan(e)
	if want := []string{"node-10.0.0.9"}; !reflect.DeepEqual(plan.DropWireGuardPeers, want) {
		t.Fatalf("DropWireGuardPeers = %v, want %v", plan.DropWireGuardPeers, want)
	}
}
