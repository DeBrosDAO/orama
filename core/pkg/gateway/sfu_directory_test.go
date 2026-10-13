package gateway

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func seedSFU(t *testing.T, db *sql.DB, nodeID, serviceType string, port int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO webrtc_port_allocations (id, node_id, namespace_cluster_id, service_type, sfu_signaling_port, sfu_media_port_start, sfu_media_port_end)
		 VALUES (?, ?, ?, ?, ?, 20000, 20499)`,
		"wpa-"+serviceType+"-"+nodeID, nodeID, testClusterID, serviceType, port); err != nil {
		t.Fatalf("insert webrtc allocation %s: %v", nodeID, err)
	}
}

func TestRegistrySFUDirectory_listsSFURolesOnActiveNodesOnly(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{"node-1": "running", "node-2": "running", "node-3": "running"})
	seedSFU(t, db, "node-1", "sfu", 30001)
	seedSFU(t, db, "node-2", "sfu", 30002)
	seedSFU(t, db, "node-3", "turn", 0) // TURN role is not an SFU
	if _, err := db.Exec(`UPDATE dns_nodes SET status = 'inactive' WHERE id = 'node-2'`); err != nil {
		t.Fatal(err)
	}

	nodes, err := newRegistrySFUDirectory(db, time.Minute).SFUNodes(context.Background(), testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-1" || nodes[0].Host != "10.0.0.1" || nodes[0].Port != 30001 {
		t.Fatalf("nodes = %+v, want only node-1 at its WireGuard IP and SFU port", nodes)
	}
}

func TestRegistrySFUDirectory_otherNamespaceAndEmpty(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{"node-1": "running"})
	seedSFU(t, db, "node-1", "sfu", 30001)

	nodes, err := newRegistrySFUDirectory(db, time.Minute).SFUNodes(context.Background(), "someone-else")
	if err != nil || len(nodes) != 0 {
		t.Fatalf("nodes = %+v err = %v, want none for a namespace with no SFU", nodes, err)
	}
}

func TestRegistrySFUDirectory_cachesWithinTTLAndRefreshesAfter(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{"node-1": "running", "node-2": "running"})
	seedSFU(t, db, "node-1", "sfu", 30001)

	d := newRegistrySFUDirectory(db, 40*time.Millisecond)
	ctx := context.Background()
	if nodes, _ := d.SFUNodes(ctx, testNamespace); len(nodes) != 1 {
		t.Fatalf("first read = %d nodes, want 1", len(nodes))
	}
	seedSFU(t, db, "node-2", "sfu", 30002)
	if nodes, _ := d.SFUNodes(ctx, testNamespace); len(nodes) != 1 {
		t.Fatalf("read inside the TTL saw %d nodes, want the cached 1", len(nodes))
	}
	time.Sleep(60 * time.Millisecond)
	if nodes, _ := d.SFUNodes(ctx, testNamespace); len(nodes) != 2 {
		t.Fatalf("read after the TTL saw %d nodes, want 2", len(nodes))
	}
}

func TestRegistrySFUDirectory_queryErrorIsReturned(t *testing.T) {
	db := newRoutingDB(t)
	_ = db.Close()
	if _, err := newRegistrySFUDirectory(db, time.Minute).SFUNodes(context.Background(), testNamespace); err == nil {
		t.Fatal("a closed registry returned no error")
	}
}

// An SFU is reached only over WireGuard: a node with no overlay address is not
// listed, and its public IP is never substituted.
func TestRegistrySFUDirectory_nodeWithoutOverlayAddressIsNotListed(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{"node-1": "running", "node-2": "running"})
	seedSFU(t, db, "node-1", "sfu", 30001)
	seedSFU(t, db, "node-2", "sfu", 30002)
	if _, err := db.Exec(`UPDATE dns_nodes SET internal_ip = '' WHERE id = 'node-2'`); err != nil {
		t.Fatal(err)
	}

	nodes, err := newRegistrySFUDirectory(db, time.Minute).SFUNodes(context.Background(), testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-1" {
		t.Fatalf("nodes = %+v, want only node-1", nodes)
	}
}
