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

// wgRow is a row for a node that already came up: created long ago and
// confirmed. That is what every row looks like after migration 038, and the
// unconfirmed cases are spelled out explicitly in the tests that need them.
func wgRow(ip string) WireGuardRow {
	return WireGuardRow{
		NodeID:      "node-" + ip,
		WGIP:        ip,
		PublicKey:   "key-" + ip,
		CreatedAt:   ago(72 * time.Hour),
		ConfirmedAt: ago(72 * time.Hour),
	}
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

func TestBuildPlan_confirms_a_row_once_its_node_appears(t *testing.T) {
	plan := BuildPlan(Evidence{
		Nodes: []Node{{PeerID: "peerA", InternalIP: "10.0.0.5", Status: "active", LastSeen: now}},
		WireGuardRows: []WireGuardRow{
			{NodeID: "node-10.0.0.5", WGIP: "10.0.0.5", CreatedAt: now.Add(-2 * time.Minute)},
		},
		Now: now,
	})

	if want := []string{"node-10.0.0.5"}; !reflect.DeepEqual(plan.ConfirmWireGuardPeers, want) {
		t.Fatalf("ConfirmWireGuardPeers = %v, want %v", plan.ConfirmWireGuardPeers, want)
	}
	if len(plan.DropUnconfirmedWireGuardPeers) != 0 {
		t.Fatalf("a row whose node appeared must not be dropped: %v", plan.DropUnconfirmedWireGuardPeers)
	}
}

func TestBuildPlan_does_not_reconfirm_an_already_confirmed_row(t *testing.T) {
	plan := BuildPlan(Evidence{
		Nodes: []Node{{PeerID: "peerA", InternalIP: "10.0.0.5", Status: "active", LastSeen: now}},
		WireGuardRows: []WireGuardRow{
			{NodeID: "peerA", WGIP: "10.0.0.5", CreatedAt: now.Add(-time.Hour), ConfirmedAt: now.Add(-time.Hour)},
		},
		Now: now,
	})

	if len(plan.ConfirmWireGuardPeers) != 0 {
		t.Fatalf("expected no confirmations, got %v", plan.ConfirmWireGuardPeers)
	}
	if !plan.Empty() {
		t.Fatalf("expected an empty plan, got %+v", plan)
	}
}

func TestBuildPlan_drops_the_residue_of_a_join_that_never_finished(t *testing.T) {
	plan := BuildPlan(Evidence{
		// No dns_nodes row ever appeared at this address.
		WireGuardRows: []WireGuardRow{
			{NodeID: "node-10.0.0.9", WGIP: "10.0.0.9", CreatedAt: now.Add(-JoinGrace - time.Minute)},
		},
		Now: now,
	})

	if want := []string{"node-10.0.0.9"}; !reflect.DeepEqual(plan.DropUnconfirmedWireGuardPeers, want) {
		t.Fatalf("DropUnconfirmedWireGuardPeers = %v, want %v", plan.DropUnconfirmedWireGuardPeers, want)
	}
	if len(plan.OrphanWireGuardPeers) != 0 {
		t.Fatalf("a failed join is dropped, not reported: %v", plan.OrphanWireGuardPeers)
	}
}

func TestBuildPlan_leaves_a_join_still_in_flight_alone(t *testing.T) {
	plan := BuildPlan(Evidence{
		WireGuardRows: []WireGuardRow{
			{NodeID: "node-10.0.0.9", WGIP: "10.0.0.9", CreatedAt: now.Add(-time.Minute)},
		},
		Now: now,
	})

	if !plan.Empty() {
		t.Fatalf("a node mid-join gets its WireGuard row before its dns_nodes row; plan was %+v", plan)
	}
}

func TestBuildPlan_never_drops_a_confirmed_row_for_being_unmatched(t *testing.T) {
	plan := BuildPlan(Evidence{
		// dns_nodes lost the row, but this node demonstrably came up once.
		WireGuardRows: []WireGuardRow{
			{NodeID: "peerA", WGIP: "10.0.0.9",
				CreatedAt: now.Add(-72 * time.Hour), ConfirmedAt: now.Add(-71 * time.Hour)},
		},
		Now: now,
	})

	if len(plan.DropUnconfirmedWireGuardPeers) != 0 {
		t.Fatalf("a confirmed row must never be dropped as a failed join: %v", plan.DropUnconfirmedWireGuardPeers)
	}
	if want := []string{"peerA"}; !reflect.DeepEqual(plan.OrphanWireGuardPeers, want) {
		t.Fatalf("OrphanWireGuardPeers = %v, want %v", plan.OrphanWireGuardPeers, want)
	}
}

func TestBuildPlan_unparseable_created_at_protects_the_row(t *testing.T) {
	// A zero CreatedAt is what parseTimestamp yields for a malformed value. The
	// safe direction is to keep the row, not to age it out immediately.
	plan := BuildPlan(Evidence{
		WireGuardRows: []WireGuardRow{{NodeID: "node-10.0.0.9", WGIP: "10.0.0.9"}},
		Now:           now,
	})

	if len(plan.DropUnconfirmedWireGuardPeers) != 0 {
		t.Fatalf("a row with no usable creation time must not be dropped: %v",
			plan.DropUnconfirmedWireGuardPeers)
	}
	if want := []string{"node-10.0.0.9"}; !reflect.DeepEqual(plan.OrphanWireGuardPeers, want) {
		t.Fatalf("OrphanWireGuardPeers = %v, want %v — keeping it is right, keeping it silently is not",
			plan.OrphanWireGuardPeers, want)
	}
}
