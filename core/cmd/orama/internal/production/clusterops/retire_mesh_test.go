package clusterops

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/overlay"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// An OramaOS node's mesh row carries the placeholder id its enrolment gave it,
// not its peer id. Retiring it must still take the machine off the mesh: a row
// left behind kept it there, and would admit a fresh identity registering from
// its address.
func TestRetirementPlan_releasesAnOramaOSNodesPlaceholderMeshRow(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	const peerID, wgIP = "12D3KooWorama", "10.0.0.6"
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, last_seen) VALUES (?, '203.0.113.6', ?, 'active', '2026-10-04 00:00:00')`, peerID, wgIP); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO wireguard_peers (node_id, wg_ip, public_key, public_ip) VALUES (?, ?, 'pk', '198.51.100.6')`, overlay.PlaceholderNodeID(wgIP), wgIP); err != nil {
		t.Fatal(err)
	}

	for _, step := range RetirementPlan(NodeRecord{PeerID: peerID, InternalIP: wgIP}) {
		if _, err := db.Exec(step.SQL); err != nil {
			t.Fatalf("%s: %v", step.What, err)
		}
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wireguard_peers WHERE wg_ip = ?`, wgIP).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d mesh row(s) left at the retired node's address %s", left, wgIP)
	}
}

// Removing a node a second time — the plan is idempotent — must not reach a
// newer node that has since been given the retired node's address.
func TestRetirementPlan_aSecondRemovalLeavesTheAddressesNewHolder(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	const peerID, wgIP = "12D3KooWold", "10.0.0.6"
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, last_seen) VALUES (?, '203.0.113.6', ?, 'active', '2026-10-04 00:00:00')`, peerID, wgIP); err != nil {
		t.Fatal(err)
	}
	plan := RetirementPlan(NodeRecord{PeerID: peerID, InternalIP: wgIP})
	for _, step := range plan {
		if _, err := db.Exec(step.SQL); err != nil {
			t.Fatalf("%s: %v", step.What, err)
		}
	}
	// The address is given to a new OramaOS node, then the old one is removed again.
	if _, err := db.Exec(`INSERT INTO wireguard_peers (node_id, wg_ip, public_key, public_ip) VALUES (?, ?, 'pk-new', '198.51.100.9')`, overlay.PlaceholderNodeID(wgIP), wgIP); err != nil {
		t.Fatal(err)
	}
	for _, step := range plan {
		if _, err := db.Exec(step.SQL); err != nil {
			t.Fatalf("%s: %v", step.What, err)
		}
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM wireguard_peers WHERE wg_ip = ?`, wgIP).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("the new holder of %s lost its mesh row to a second removal of the old node", wgIP)
	}
}
