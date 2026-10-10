package node

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
)

// newRealSchemaDNSDB is an in-memory SQLite carrying the real migrations, so the
// orphan purge and the ensure run against the production table definitions.
func newRealSchemaDNSDB(t *testing.T) *sql.DB {
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
	return db
}

func addNamespaceRow(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO namespaces (name) VALUES (?)`, name)
}

// addNamespaceRecords writes the records the three writers create for one
// namespace on one node IP.
func addNamespaceRecords(t *testing.T, db *sql.DB, name, ip string) {
	t.Helper()
	for _, r := range []struct{ fqdn, tag string }{
		{"ns-" + name + ".d.", "namespace:" + name},
		{"*.ns-" + name + ".d.", "namespace:" + name},
		{"turn.ns-" + name + ".d.", "namespace-turn:" + name},
		{"turn-" + name + ".d.", "namespace-turn:" + name},
		{"cdn-" + name + ".d.", "namespace-turn-stealth:" + name},
	} {
		mustExec(t, db, `INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES (?, 'A', ?, ?, 't')`,
			r.fqdn, ip, r.tag)
	}
}

func TestPurgeOrphanedNamespaceRecords_removesEveryTagOfAGoneNamespace(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	// The live stagenet shape: a namespace that is gone, records on all three nodes.
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		addNamespaceRecords(t, db, "e2e-gone", ip)
	}

	res, err := db.Exec(purgeOrphanedNamespaceRecordsSQL)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 15 {
		t.Errorf("purged %d rows, want 15", n)
	}
	if countRecords(t, db, `namespace LIKE 'namespace%'`) != 0 {
		t.Error("orphaned namespace records survived")
	}
}

func TestPurgeOrphanedNamespaceRecords_neverTouchesALiveNamespace(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	addNamespaceRow(t, db, "live")
	addNamespaceRecords(t, db, "live", "1.1.1.1")
	addNamespaceRecords(t, db, "gone", "1.1.1.1")

	mustExec(t, db, purgeOrphanedNamespaceRecordsSQL)

	if got := countRecords(t, db, `namespace IN ('namespace:live','namespace-turn:live','namespace-turn-stealth:live')`); got != 5 {
		t.Errorf("live namespace has %d records, want 5", got)
	}
	if got := countRecords(t, db, `namespace LIKE 'namespace%:gone'`); got != 0 {
		t.Errorf("gone namespace still has %d records", got)
	}
}

// Records of a live namespace stay even when the node they point at is dead or
// disabled: the orphan purge is about the namespace, not liveness.
func TestPurgeOrphanedNamespaceRecords_keepsDisabledRowsOfALiveNamespace(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	addNamespaceRow(t, db, "live")
	addNamespaceRecords(t, db, "live", "1.1.1.1")
	mustExec(t, db, `UPDATE dns_records SET is_active = 0`)

	mustExec(t, db, purgeOrphanedNamespaceRecordsSQL)

	if got := countRecords(t, db, `namespace LIKE 'namespace%:live'`); got != 5 {
		t.Errorf("a live namespace's disabled records were purged: %d left, want 5", got)
	}
}

// System records and every non-namespace tag are out of scope, including the
// base wildcard the resolver falls back to.
func TestPurgeOrphanedNamespaceRecords_leavesNonNamespaceRecordsAlone(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	mustExec(t, db, `INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES ('*.d.', 'A', '9.9.9.9', 'system', 't')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES ('d.', 'NS', 'ns1.d.', 'system', 't')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES ('app.d.', 'A', '9.9.9.9', 'someapp', 't')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn, record_type, value, namespace, created_by) VALUES ('dom.example.', 'A', '9.9.9.9', 'namespaceish', 't')`)

	res, err := db.Exec(purgeOrphanedNamespaceRecordsSQL)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("purged %d non-namespace rows, want 0", n)
	}
}

func TestPurgeOrphanedNamespaceRecords_isIdempotentAndHandlesAnEmptyTable(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	for i := 0; i < 2; i++ {
		res, err := db.Exec(purgeOrphanedNamespaceRecordsSQL)
		if err != nil {
			t.Fatalf("purge on an empty table: %v", err)
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Errorf("purged %d rows from an empty table", n)
		}
	}
	addNamespaceRecords(t, db, "gone", "1.1.1.1")
	mustExec(t, db, purgeOrphanedNamespaceRecordsSQL)
	res, err := db.Exec(purgeOrphanedNamespaceRecordsSQL)
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("second purge removed %d rows, want 0", n)
	}
}

// Names that resemble one another must not shield each other: matching is by
// equality on the tag, so `a_b` being live keeps nothing of `axb`, and `a` being
// live keeps nothing of `a-b`.
func TestPurgeOrphanedNamespaceRecords_similarNamesAreDistinct(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	addNamespaceRow(t, db, "a_b")
	addNamespaceRow(t, db, "a")
	addNamespaceRecords(t, db, "a_b", "1.1.1.1")
	addNamespaceRecords(t, db, "axb", "1.1.1.1")
	addNamespaceRecords(t, db, "a", "1.1.1.1")
	addNamespaceRecords(t, db, "a-b", "1.1.1.1")

	mustExec(t, db, purgeOrphanedNamespaceRecordsSQL)

	for _, live := range []string{"a_b", "a"} {
		if got := countRecords(t, db, `namespace LIKE 'namespace%:'||?`, live); got != 5 {
			t.Errorf("live %q has %d records, want 5", live, got)
		}
	}
	for _, gone := range []string{"axb", "a-b"} {
		if got := countRecords(t, db, `namespace LIKE 'namespace%:'||?`, gone); got != 0 {
			t.Errorf("orphaned %q still has %d records", gone, got)
		}
	}
}

// The writer half: the periodic per-node ensure was what re-created these
// records after a delete or a rollback, because it trusted the cluster's
// membership rows.
func setupRealSchemaCluster(t *testing.T, db *sql.DB, name, status string) {
	t.Helper()
	res, err := db.Exec(`INSERT INTO namespaces (name) VALUES (?)`, name)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	id, _ := res.LastInsertId()
	mustExec(t, db, `INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by) VALUES (?, ?, ?, ?, 'w')`,
		"c-"+name, id, name, status)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes (id, namespace_cluster_id, node_id, role, status) VALUES (?, ?, 'peerA', 'gateway', 'running')`,
		"n-"+name, "c-"+name)
}

func TestEnsureNamespaceHostRecords_doesNotAdvertiseAFailedCluster(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	// A rolled-back provision: cluster 'failed', membership still 'running'.
	setupRealSchemaCluster(t, db, "e2e-failed", "failed")
	setupRealSchemaCluster(t, db, "e2e-going", "deprovisioning")

	if got := runEnsure(t, db, "*.", "d", "1.1.1.1", "peerA"); got != 0 {
		t.Errorf("advertised %d records for failed/deprovisioning clusters, want 0", got)
	}
	if countRecords(t, db, `1=1`) != 0 {
		t.Error("records were created for a failed or deprovisioning cluster")
	}
}

// The other leak: a namespace row deleted while a stale cluster row and its
// membership are still readable must not be advertised.
func TestEnsureNamespaceHostRecords_doesNotAdvertiseAGoneNamespace(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	setupRealSchemaCluster(t, db, "e2e-gone", "ready")
	mustExec(t, db, `DELETE FROM namespaces WHERE name = 'e2e-gone'`)

	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 0 {
		t.Errorf("advertised %d records for a namespace absent from namespaces, want 0", got)
	}
}

func TestEnsureNamespaceHostRecords_stillAdvertisesReadyAndDegradedClusters(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	setupRealSchemaCluster(t, db, "ok-ns", "ready")
	setupRealSchemaCluster(t, db, "degraded-ns", "degraded")
	setupRealSchemaCluster(t, db, "new-ns", "provisioning")

	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 3 {
		t.Errorf("advertised %d records, want 3 (ready, degraded and provisioning clusters serve)", got)
	}
}

// The full sequence the evidence shows: 1 of 3 node records leaked after a
// failed provision. The sweep's purge heals it and the ensure does not undo it.
func TestOrphanPurge_healsALeakedRecordAndTheEnsureDoesNotResurrectIt(t *testing.T) {
	db := newRealSchemaDNSDB(t)
	setupRealSchemaCluster(t, db, "e2e-leak", "failed")
	addNamespaceRecords(t, db, "e2e-leak", "1.1.1.1")
	mustExec(t, db, `DELETE FROM namespaces WHERE name = 'e2e-leak'`)
	mustExec(t, db, `DELETE FROM namespace_clusters WHERE namespace_name = 'e2e-leak'`)

	mustExec(t, db, purgeOrphanedNamespaceRecordsSQL)
	runEnsure(t, db, "", "d", "1.1.1.1", "peerA")
	runEnsure(t, db, "*.", "d", "1.1.1.1", "peerA")

	if got := countRecords(t, db, `namespace LIKE 'namespace%:e2e-leak'`); got != 0 {
		t.Errorf("%d records of a deleted namespace remain", got)
	}
}
