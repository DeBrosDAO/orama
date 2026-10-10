package gateway

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// Bugboard #278. The namespace router used to select gateway targets with
// `WHERE nc.namespace_name = ? AND nc.status = 'ready'`. When one node's gateway row
// was marked failed, the recovery path set the cluster to 'degraded' — and the query
// then returned zero rows, so the namespace answered "Namespace gateway not found" on
// EVERY node, including the two whose gateways were healthy and serving on :10004.
// A single node restart took a whole tenant offline.
//
// These tests run the real query (namespaceGatewayTargetsQuery) against the real
// schema, so they fail if the predicate regresses.

const (
	testClusterID = "cluster-1"
	testNamespace = "anchat-test"
)

// newRoutingDB builds an in-memory database with the platform schema applied.
func newRoutingDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// seedCluster inserts a namespace cluster whose three nodes each have a gateway
// allocation. gatewayStatuses maps node id -> namespace_cluster_nodes.status.
func seedCluster(t *testing.T, db *sql.DB, clusterStatus string, gatewayStatuses map[string]string) {
	t.Helper()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, rqlite_node_count, olric_node_count, gateway_node_count, provisioned_by)
		 VALUES (?, 1, ?, ?, 3, 3, 3, 'test')`,
		testClusterID, testNamespace, clusterStatus); err != nil {
		t.Fatalf("insert cluster: %v", err)
	}

	for nodeID, status := range gatewayStatuses {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES (?, ?, ?, 'active')`,
			nodeID, "203.0.113."+nodeID[len(nodeID)-1:], "10.0.0."+nodeID[len(nodeID)-1:]); err != nil {
			t.Fatalf("insert dns_node %s: %v", nodeID, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO namespace_port_allocations
			   (id, node_id, namespace_cluster_id, port_start, port_end,
			    rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
			 VALUES (?, ?, ?, 10000, 10004, 10000, 10001, 10002, 10003, 10004)`,
			"alloc-"+nodeID, nodeID, testClusterID); err != nil {
			t.Fatalf("insert allocation %s: %v", nodeID, err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO namespace_cluster_nodes
			   (id, namespace_cluster_id, node_id, role, rqlite_http_port, rqlite_raft_port,
			    olric_http_port, olric_memberlist_port, gateway_http_port, status)
			 VALUES (?, ?, ?, 'gateway', 10000, 10001, 10002, 10003, 10004, ?)`,
			"ncn-"+nodeID, testClusterID, nodeID, status); err != nil {
			t.Fatalf("insert cluster_node %s: %v", nodeID, err)
		}
	}
}

func countTargets(t *testing.T, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), namespaceGatewayTargetsQuery, testNamespace)
	if err != nil {
		t.Fatalf("routing query: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var ip string
		var port int
		if err := rows.Scan(&ip, &port); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return n
}

// TestNamespaceRouting_degradedClusterStillServesHealthyNodes is the exact incident:
// cluster degraded, one gateway failed, two healthy. The two healthy ones must remain
// routable.
func TestNamespaceRouting_degradedClusterStillServesHealthyNodes(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "degraded", map[string]string{
		"node-1": "running",
		"node-2": "running",
		"node-3": "failed",
	})

	if got := countTargets(t, db); got != 2 {
		t.Errorf("got %d routable gateways, want 2 — a degraded cluster must still be served by its healthy members, not 404 on every node", got)
	}
}

// TestNamespaceRouting_readyClusterUnchanged guards the normal path.
func TestNamespaceRouting_readyClusterUnchanged(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{
		"node-1": "running",
		"node-2": "running",
		"node-3": "running",
	})

	if got := countTargets(t, db); got != 3 {
		t.Errorf("got %d routable gateways, want 3", got)
	}
}

// TestNamespaceRouting_failedNodesExcluded: a node whose gateway is down must not be
// handed traffic even while the cluster is nominally ready.
func TestNamespaceRouting_failedNodesExcluded(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "ready", map[string]string{
		"node-1": "running",
		"node-2": "failed",
		"node-3": "failed",
	})

	if got := countTargets(t, db); got != 1 {
		t.Errorf("got %d routable gateways, want 1 (only the running node)", got)
	}
}

// TestNamespaceRouting_noLiveGatewayReturnsNothing is the genuine 404 case — every
// gateway down. The handler's "not found" branch should still be reachable.
func TestNamespaceRouting_noLiveGatewayReturnsNothing(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "degraded", map[string]string{
		"node-1": "failed",
		"node-2": "failed",
		"node-3": "failed",
	})

	if got := countTargets(t, db); got != 0 {
		t.Errorf("got %d routable gateways, want 0 when nothing is live", got)
	}
}

// TestNamespaceRouting_failedClusterNotServed: a cluster that never provisioned
// (status 'failed') must not be routable even if stale node rows say running.
func TestNamespaceRouting_failedClusterNotServed(t *testing.T) {
	db := newRoutingDB(t)
	seedCluster(t, db, "failed", map[string]string{
		"node-1": "running",
		"node-2": "running",
	})

	if got := countTargets(t, db); got != 0 {
		t.Errorf("got %d routable gateways, want 0 for a failed cluster", got)
	}
}
