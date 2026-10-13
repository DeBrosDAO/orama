package namespace

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Bugboard #323. DeprovisionCluster fanned six serial stop RPCs per node — each
// with a 60s HTTP timeout — at every member of namespace_cluster_nodes, with no
// liveness filter. Deleting `vrf708`, stranded on three departed nodes, therefore
// blocked ~18 minutes before touching a single row. It reads as a hang, so it gets
// interrupted, which leaves the cluster half-torn-down.
//
// The case is worst exactly when it matters most: a namespace stranded on gone
// hardware is the main reason to delete one.

func newDeprovisionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`CREATE TABLE namespace_cluster_nodes (
		namespace_cluster_id TEXT, node_id TEXT, role TEXT, status TEXT
	)`); err != nil {
		t.Fatalf("create namespace_cluster_nodes: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE dns_nodes (
		id TEXT, ip_address TEXT, internal_ip TEXT, status TEXT
	)`); err != nil {
		t.Fatalf("create dns_nodes: %v", err)
	}
	return db
}

func seedDeprovisionNode(t *testing.T, db *sql.DB, clusterID, nodeID, internalIP, dnsStatus string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status)
		 VALUES (?, ?, 'gateway', 'running')`, clusterID, nodeID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES (?, ?, ?, ?)`,
		nodeID, "203.0.113.9", internalIP, dnsStatus); err != nil {
		t.Fatalf("seed dns_node: %v", err)
	}
}

func deprovisionTargets(t *testing.T, db *sql.DB, clusterID string) []string {
	t.Helper()
	rows, err := db.Query(deprovisionActiveNodesQuery, clusterID)
	if err != nil {
		t.Fatalf("deprovision query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var nodeID, ip string
		if err := rows.Scan(&nodeID, &ip); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, nodeID)
	}
	return out
}

// The vrf708 shape: every node departed, so there is nothing to talk to.
func TestDeprovisionTargets_skipsDepartedNodes(t *testing.T) {
	db := newDeprovisionDB(t)
	seedDeprovisionNode(t, db, "vrf708-cluster", "gone-1", "10.0.0.13", "inactive")
	seedDeprovisionNode(t, db, "vrf708-cluster", "gone-2", "10.0.0.7", "inactive")
	seedDeprovisionNode(t, db, "vrf708-cluster", "gone-3", "10.0.0.9", "inactive")

	got := deprovisionTargets(t, db, "vrf708-cluster")
	if len(got) != 0 {
		t.Errorf("would send stop RPCs to %v — each waits a 60s timeout against a dead host, ~18 min for three nodes", got)
	}
}

// A live cluster must still be torn down properly on every node.
func TestDeprovisionTargets_includesActiveNodes(t *testing.T) {
	db := newDeprovisionDB(t)
	seedDeprovisionNode(t, db, "c1", "node-1", "10.0.0.1", "active")
	seedDeprovisionNode(t, db, "c1", "node-2", "10.0.0.2", "active")

	got := deprovisionTargets(t, db, "c1")
	if len(got) != 2 {
		t.Errorf("got %v, want both active nodes — a live teardown must still stop their services", got)
	}
}

// The mixed case: stop the live one, skip the dead one.
func TestDeprovisionTargets_mixedFleetOnlyTalksToLiveNodes(t *testing.T) {
	db := newDeprovisionDB(t)
	seedDeprovisionNode(t, db, "c1", "alive", "10.0.0.1", "active")
	seedDeprovisionNode(t, db, "c1", "departed", "10.0.0.6", "inactive")
	seedDeprovisionNode(t, db, "c1", "offline", "10.0.0.11", "offline")

	got := deprovisionTargets(t, db, "c1")
	if len(got) != 1 || got[0] != "alive" {
		t.Errorf("got %v, want only [alive]", got)
	}
}

// Another cluster's nodes must never be dragged into this teardown.
func TestDeprovisionTargets_scopedToTheCluster(t *testing.T) {
	db := newDeprovisionDB(t)
	seedDeprovisionNode(t, db, "c1", "mine", "10.0.0.1", "active")
	seedDeprovisionNode(t, db, "c2", "theirs", "10.0.0.2", "active")

	got := deprovisionTargets(t, db, "c1")
	if len(got) != 1 || got[0] != "mine" {
		t.Errorf("got %v, want only this cluster's node", got)
	}
}
