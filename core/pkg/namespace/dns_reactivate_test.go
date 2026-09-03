package namespace

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Bugboard #286, second attempt. The first fix called
// EnsureNamespaceHostRecordForNode from the periodic sweep — and did nothing,
// because that statement is INSERT-if-absent and deliberately never re-enables a
// soft-disabled row. The row for a restarted node already exists with
// is_active = 0, so the ensure matched nothing and the node stayed out of the
// round-robin exactly as before.
//
// The original test only asserted "a dns_records write happened", which an
// INSERT-if-absent satisfies against a mock. It passed while the fix did nothing.
// These run the real statements against real SQLite with the row in the state
// that actually occurs.

func newDNSTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE dns_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		fqdn TEXT, record_type TEXT, value TEXT, ttl INTEGER,
		namespace TEXT, created_by TEXT,
		created_at TIMESTAMP, updated_at TIMESTAMP,
		is_active BOOLEAN DEFAULT 1
	)`); err != nil {
		t.Fatalf("create dns_records: %v", err)
	}
	return db
}

func isActive(t *testing.T, db *sql.DB, fqdn, value string) bool {
	t.Helper()
	var active bool
	err := db.QueryRow(
		`SELECT is_active FROM dns_records WHERE fqdn = ? AND value = ?`, fqdn, value,
	).Scan(&active)
	if err != nil {
		t.Fatalf("read is_active for %s -> %s: %v", fqdn, value, err)
	}
	return active
}

// The exact production state: a node restarted, its record was soft-disabled, and
// the node is serving again. The reactivation must flip it back.
func TestReactivateNamespaceHostRecord_reenablesThisNodesDisabledRecord(t *testing.T) {
	db := newDNSTestDB(t)
	const (
		fqdn  = "ns-anchat-v2.orama-devnet.network."
		value = "169.58.118.206"
		tag   = "namespace:anchat-v2"
	)
	if _, err := db.Exec(
		`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active)
		 VALUES (?, 'A', ?, 60, ?, 'test', 0)`, fqdn, value, tag); err != nil {
		t.Fatalf("seed disabled record: %v", err)
	}
	if isActive(t, db, fqdn, value) {
		t.Fatal("seed did not produce a disabled record")
	}

	if _, err := db.Exec(reactivateNamespaceHostRecordSQL, "2026-09-03 07:00:00", fqdn, value, tag); err != nil {
		t.Fatalf("reactivate: %v", err)
	}

	if !isActive(t, db, fqdn, value) {
		t.Error("record still disabled — the node stays out of the ns-<ns> round-robin forever after a restart")
	}
}

// This is what the FIRST fix did, and why it failed: the additive ensure leaves a
// disabled row untouched. Pinning it so the distinction cannot be lost again.
func TestEnsureNamespaceHostRecordSQL_doesNotReenableDisabledRow(t *testing.T) {
	db := newDNSTestDB(t)
	const (
		fqdn  = "ns-anchat-v2.orama-devnet.network."
		value = "169.58.118.206"
		tag   = "namespace:anchat-v2"
	)
	if _, err := db.Exec(
		`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active)
		 VALUES (?, 'A', ?, 60, ?, 'test', 0)`, fqdn, value, tag); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := db.Exec(ensureNamespaceHostRecordSQL,
		fqdn, value, tag, "2026-09-03 07:00:00", "2026-09-03 07:00:00", fqdn, value, tag); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if isActive(t, db, fqdn, value) {
		t.Error("the additive ensure re-enabled a disabled row — it must not; reactivation is a separate, evidence-gated step")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE fqdn = ? AND value = ?`, fqdn, value).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("ensure created a duplicate row (count=%d)", n)
	}
}

// Reactivation must be scoped to this node's own row — never a peer's.
func TestReactivateNamespaceHostRecord_doesNotTouchOtherNodes(t *testing.T) {
	db := newDNSTestDB(t)
	const (
		fqdn = "ns-anchat-v2.orama-devnet.network."
		tag  = "namespace:anchat-v2"
		mine = "169.58.118.206"
		peer = "57.129.7.232"
	)
	for _, ip := range []string{mine, peer} {
		if _, err := db.Exec(
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active)
			 VALUES (?, 'A', ?, 60, ?, 'test', 0)`, fqdn, ip, tag); err != nil {
			t.Fatalf("seed %s: %v", ip, err)
		}
	}

	if _, err := db.Exec(reactivateNamespaceHostRecordSQL, "2026-09-03 07:00:00", fqdn, mine, tag); err != nil {
		t.Fatalf("reactivate: %v", err)
	}

	if !isActive(t, db, fqdn, mine) {
		t.Error("this node's record was not re-enabled")
	}
	if isActive(t, db, fqdn, peer) {
		t.Error("re-enabled a PEER's record — a node may only make claims about itself")
	}
}

// A record belonging to a different namespace must not be touched.
func TestReactivateNamespaceHostRecord_scopedToNamespace(t *testing.T) {
	db := newDNSTestDB(t)
	const ip = "169.58.118.206"
	for _, r := range []struct{ fqdn, tag string }{
		{"ns-anchat-v2.orama-devnet.network.", "namespace:anchat-v2"},
		{"ns-anchat-test.orama-devnet.network.", "namespace:anchat-test"},
	} {
		if _, err := db.Exec(
			`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active)
			 VALUES (?, 'A', ?, 60, ?, 'test', 0)`, r.fqdn, ip, r.tag); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if _, err := db.Exec(reactivateNamespaceHostRecordSQL,
		"2026-09-03 07:00:00", "ns-anchat-v2.orama-devnet.network.", ip, "namespace:anchat-v2"); err != nil {
		t.Fatalf("reactivate: %v", err)
	}

	if isActive(t, db, "ns-anchat-test.orama-devnet.network.", ip) {
		t.Error("reactivating one namespace's record also re-enabled another's")
	}
}

// An already-active record must stay active and not be needlessly rewritten.
func TestReactivateNamespaceHostRecord_activeRecordIsUntouched(t *testing.T) {
	db := newDNSTestDB(t)
	const (
		fqdn  = "ns-anchat-v2.orama-devnet.network."
		value = "169.58.118.206"
		tag   = "namespace:anchat-v2"
	)
	if _, err := db.Exec(
		`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, is_active)
		 VALUES (?, 'A', ?, 60, ?, 'test', 1)`, fqdn, value, tag); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := db.Exec(reactivateNamespaceHostRecordSQL, "2026-09-03 07:00:00", fqdn, value, tag)
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 0 {
		t.Errorf("rewrote an already-active record (%d rows) — the statement should be a no-op there", n)
	}
	if !isActive(t, db, fqdn, value) {
		t.Error("an active record was disabled")
	}
}
