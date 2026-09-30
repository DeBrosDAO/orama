package node

import (
	"context"
	"testing"
)

func TestActiveOverlayPeers_usesTheRegisteredNodeId(t *testing.T) {
	db := setupDNSTestDB(t)
	if _, err := db.Exec(`ALTER TABLE dns_nodes ADD COLUMN internal_ip TEXT`); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]string{
		{"12D3KooWLive", "10.0.0.2", "active"},
		{"12D3KooWReplaced", "10.0.0.2", "inactive"}, // a node that was replaced at the same overlay IP
		{"12D3KooWNoOverlay", "", "active"},
	} {
		if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES (?, '203.0.113.1', ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := activeOverlayPeers(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "12D3KooWLive" || got[0].IP != "10.0.0.2" {
		t.Fatalf("got %+v, want only the active registered node", got)
	}
}

func TestActiveOverlayPeers_emptyRegistry(t *testing.T) {
	db := setupDNSTestDB(t)
	if _, err := db.Exec(`ALTER TABLE dns_nodes ADD COLUMN internal_ip TEXT`); err != nil {
		t.Fatal(err)
	}
	got, _, err := activeOverlayPeers(context.Background(), db)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestActiveOverlayPeers_registryError(t *testing.T) {
	db := setupDNSTestDB(t) // no internal_ip column
	if _, _, err := activeOverlayPeers(context.Background(), db); err == nil {
		t.Fatal("a failed registry read was swallowed")
	}
}

// A registered address outside the WireGuard overlay is not a target: requests
// signed for a node go only over the mesh. It is counted, not silently lost.
func TestActiveOverlayPeers_anAddressOffTheOverlayIsSkippedAndCounted(t *testing.T) {
	db := setupDNSTestDB(t)
	if _, err := db.Exec(`ALTER TABLE dns_nodes ADD COLUMN internal_ip TEXT`); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]string{
		{"12D3KooWOverlay", "10.0.0.3"},
		{"12D3KooWPublic", "203.0.113.9"},
		{"12D3KooWGarbage", "not-an-ip"},
	} {
		if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES (?, '203.0.113.1', ?, 'active')`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, skipped, err := activeOverlayPeers(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "12D3KooWOverlay" || skipped != 2 {
		t.Fatalf("got %+v, skipped %d; want only the overlay node and two skipped", got, skipped)
	}
}
