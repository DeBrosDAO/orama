package namespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

// newRealSchemaClusterManager builds a ClusterManager over an in-memory SQLite
// carrying the real migrations, so the statements under test run against the
// production table definitions (UNIQUE(fqdn, record_type, value) and all).
func newRealSchemaClusterManager(t *testing.T) (*ClusterManager, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyMigrations(context.Background(), db, "../../migrations", zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	rc := rqlite.NewClient(db)
	logger := zap.NewNop()
	cm := &ClusterManager{
		db:            rc,
		logger:        logger,
		baseDomain:    "d",
		dnsManager:    NewDNSRecordManager(rc, "d", logger),
		portAllocator: NewNamespacePortAllocator(rc, logger),
	}
	return cm, db
}

func seedRealNamespace(t *testing.T, db *sql.DB, name, clusterID, status string, nodes ...string) *NamespaceCluster {
	t.Helper()
	res, err := db.Exec(`INSERT INTO namespaces (name) VALUES (?)`, name)
	if err != nil {
		t.Fatalf("seed namespace %s: %v", name, err)
	}
	nsID, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by)
		VALUES (?, ?, ?, ?, 'w')`, clusterID, nsID, name, status); err != nil {
		t.Fatalf("seed cluster %s: %v", name, err)
	}
	for i, n := range nodes {
		if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (id, namespace_cluster_id, node_id, role, status)
			VALUES (?, ?, ?, 'gateway', 'running')`, clusterID+"-"+n, clusterID, n); err != nil {
			t.Fatalf("seed member %d of %s: %v", i, name, err)
		}
	}
	// The writers' records: gateway host + wildcard, TURN, stealth, per node.
	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		for _, r := range []struct{ fqdn, tag string }{
			{"ns-" + name + ".d.", "namespace:" + name},
			{"*.ns-" + name + ".d.", "namespace:" + name},
			{"turn.ns-" + name + ".d.", "namespace-turn:" + name},
			{"cdn-" + name + ".d.", "namespace-turn-stealth:" + name},
		} {
			if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES (?, 'A', ?, ?, 't')`,
				r.fqdn, ip, r.tag); err != nil {
				t.Fatalf("seed record %d %s: %v", i, r.fqdn, err)
			}
		}
	}
	return &NamespaceCluster{ID: clusterID, NamespaceID: int(nsID), NamespaceName: name, Status: ClusterStatus(status)}
}

func countWhere(t *testing.T, db *sql.DB, table, where string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func seedSystemRecords(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, fqdn := range []string{"*.d.", "ns1.d."} {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES (?, 'A', '9.9.9.9', 'system', 't')`, fqdn); err != nil {
			t.Fatalf("seed system record: %v", err)
		}
	}
}

func TestRemoveClusterServingRecords_removesEveryTagAndMembership(t *testing.T) {
	cm, db := newRealSchemaClusterManager(t)
	dead := seedRealNamespace(t, db, "dead", "c-dead", "failed", "n1", "n2", "n3")
	seedRealNamespace(t, db, "live", "c-live", "ready", "n1", "n2", "n3")
	seedSystemRecords(t, db)

	if err := cm.removeClusterServingRecords(context.Background(), dead); err != nil {
		t.Fatalf("removeClusterServingRecords: %v", err)
	}

	if n := countWhere(t, db, "dns_records", `namespace IN ('namespace:dead','namespace-turn:dead','namespace-turn-stealth:dead')`); n != 0 {
		t.Errorf("%d records of the removed cluster survived", n)
	}
	if n := countWhere(t, db, "namespace_cluster_nodes", `namespace_cluster_id = 'c-dead'`); n != 0 {
		t.Errorf("%d membership rows survived; the node sweep would re-advertise from them", n)
	}
	if n := countWhere(t, db, "dns_records", `namespace = 'namespace:live'`); n != 6 {
		t.Errorf("a live namespace lost records: %d left, want 6", n)
	}
	if n := countWhere(t, db, "namespace_cluster_nodes", `namespace_cluster_id = 'c-live'`); n != 3 {
		t.Errorf("a live cluster lost membership rows: %d left, want 3", n)
	}
	if n := countWhere(t, db, "dns_records", `namespace = 'system'`); n != 2 {
		t.Errorf("system records touched: %d left, want 2", n)
	}
}

func TestRemoveClusterServingRecords_idempotentOnAnEmptyCluster(t *testing.T) {
	cm, _ := newRealSchemaClusterManager(t)
	ghost := &NamespaceCluster{ID: "nope", NamespaceName: "ghost"}
	if err := cm.removeClusterServingRecords(context.Background(), ghost); err != nil {
		t.Fatalf("a cluster with nothing to remove must not fail: %v", err)
	}
}

func TestRemoveClusterServingRecords_reportsFailureButStillTriesEveryStep(t *testing.T) {
	cm, db := newRealSchemaClusterManager(t)
	dead := seedRealNamespace(t, db, "dead", "c-dead", "failed", "n1")
	// Make the membership delete fail; the DNS deletes must still run.
	if _, err := db.Exec(`DROP TABLE namespace_cluster_nodes`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	err := cm.removeClusterServingRecords(context.Background(), dead)
	if err == nil || !strings.Contains(err.Error(), "node membership") {
		t.Fatalf("want the membership failure reported, got %v", err)
	}
	if n := countWhere(t, db, "dns_records", `namespace LIKE 'namespace%:dead'`); n != 0 {
		t.Errorf("the DNS deletes were skipped after the membership failure: %d left", n)
	}
}

// The leak: a provision fails, the cluster row is left 'failed', and the next
// create attempt deletes that row. Before the fix the DNS rows outlived it, and a
// later DeprovisionCluster finds no cluster and returns without touching DNS.
func TestCheckNamespaceCluster_failedClusterLeavesNoDNSRecords(t *testing.T) {
	cm, db := newRealSchemaClusterManager(t)
	seedRealNamespace(t, db, "e2e-x", "c-x", "failed", "n1", "n2", "n3")
	seedSystemRecords(t, db)

	_, _, needs, err := cm.CheckNamespaceCluster(context.Background(), "e2e-x")
	if err != nil {
		t.Fatalf("CheckNamespaceCluster: %v", err)
	}
	if !needs {
		t.Fatal("a failed cluster must be re-provisioned")
	}
	if n := countWhere(t, db, "namespace_clusters", `id = 'c-x'`); n != 0 {
		t.Errorf("failed cluster row survived")
	}
	if n := countWhere(t, db, "dns_records", `namespace LIKE 'namespace%:e2e-x'`); n != 0 {
		t.Errorf("%d DNS records leaked past the failed cluster's row", n)
	}
	if n := countWhere(t, db, "namespace_cluster_nodes", `namespace_cluster_id = 'c-x'`); n != 0 {
		t.Errorf("%d membership rows leaked", n)
	}
	if n := countWhere(t, db, "dns_records", `namespace = 'system'`); n != 2 {
		t.Errorf("system records touched: %d left", n)
	}
}

func TestCheckNamespaceCluster_readyClusterKeepsItsRecords(t *testing.T) {
	cm, db := newRealSchemaClusterManager(t)
	seedRealNamespace(t, db, "live", "c-live", "ready", "n1", "n2", "n3")

	_, status, needs, err := cm.CheckNamespaceCluster(context.Background(), "live")
	if err != nil || needs || status != "ready" {
		t.Fatalf("got status=%q needs=%v err=%v, want ready/false/nil", status, needs, err)
	}
	if n := countWhere(t, db, "dns_records", `namespace = 'namespace:live'`); n != 6 {
		t.Errorf("a ready cluster's records were touched: %d left, want 6", n)
	}
}

// The per-node re-advertise must not resurrect the records of a namespace that
// was deleted while the node was mid-spawn.
func TestEnsureNamespaceHostRecordForNode_refusesADeletedNamespace(t *testing.T) {
	cm, db := newRealSchemaClusterManager(t)
	seedRealNamespace(t, db, "live", "c-live", "ready")
	if _, err := db.Exec(`DELETE FROM dns_records`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	ctx := context.Background()

	if err := cm.dnsManager.EnsureNamespaceHostRecordForNode(ctx, "gone", "5.5.5.5"); err != nil {
		t.Fatalf("ensure for a deleted namespace must be a quiet no-op, got %v", err)
	}
	if n := countWhere(t, db, "dns_records", `1=1`); n != 0 {
		t.Errorf("records written for a namespace absent from namespaces: %d", n)
	}

	if err := cm.dnsManager.EnsureNamespaceHostRecordForNode(ctx, "live", "5.5.5.5"); err != nil {
		t.Fatalf("ensure for a live namespace: %v", err)
	}
	if n := countWhere(t, db, "dns_records", `namespace = 'namespace:live' AND value = '5.5.5.5'`); n != 2 {
		t.Errorf("live namespace host + wildcard not written: %d rows", n)
	}
}
