package namespace

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Bugboard #158: EnsureTURNRecordForNode must re-advertise a live TURN node's
// own A record (additive, idempotent) without disturbing other TURN nodes'
// records for the same round-robin fqdn. Exercises the exact production query.
func TestEnsureTURNRecordSQL_additiveIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE dns_records (fqdn TEXT, record_type TEXT, value TEXT, ttl INT, namespace TEXT, created_by TEXT, created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// Another TURN node already advertises for the same fqdn.
	db.Exec(`INSERT INTO dns_records VALUES ('turn.ns-x.d.','A','2.2.2.2',60,'namespace-turn:x','cluster-manager','t','t')`)

	fqdn, tag, ip := "turn.ns-x.d.", "namespace-turn:x", "1.1.1.1"
	run := func() int64 {
		res, err := db.Exec(ensureTURNRecordSQL, fqdn, ip, tag, "t", "t", fqdn, ip, tag)
		if err != nil {
			t.Fatalf("ensure exec: %v", err)
		}
		n, _ := res.RowsAffected()
		return n
	}

	if got := run(); got != 1 {
		t.Errorf("first ensure inserted %d rows, want 1 (adds this node's record)", got)
	}
	if got := run(); got != 0 {
		t.Errorf("second ensure inserted %d rows, want 0 (idempotent)", got)
	}

	count := func(where string, args ...interface{}) int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if count(`value = '2.2.2.2'`) != 1 {
		t.Error("the other TURN node's record must be left untouched")
	}
	if count(`fqdn = ? AND record_type = 'A'`, fqdn) != 2 {
		t.Error("both TURN nodes should advertise for the fqdn after ensure")
	}
}

// The namespace-host ensure is the recoverability counterpart that makes purging
// `ns-<ns>` rows safe: without it a node marked non-active transiently would lose
// its gateway A record permanently. Exercises the exact production query.
func TestEnsureNamespaceHostRecordSQL_additiveIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE dns_records (fqdn TEXT, record_type TEXT, value TEXT, ttl INT, namespace TEXT, created_by TEXT, created_at TEXT, updated_at TEXT, is_active BOOLEAN NOT NULL DEFAULT TRUE)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// Another gateway node already advertises for the same round-robin fqdn.
	if _, err := db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,ttl,namespace,created_by,created_at,updated_at) VALUES ('ns-x.d.','A','2.2.2.2',60,'namespace:x','cluster-manager','t','t')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fqdn, tag, ip := "ns-x.d.", "namespace:x", "1.1.1.1"
	run := func() int64 {
		res, err := db.Exec(ensureNamespaceHostRecordSQL, fqdn, ip, tag, "t", "t", fqdn, ip, tag)
		if err != nil {
			t.Fatalf("ensure exec: %v", err)
		}
		n, _ := res.RowsAffected()
		return n
	}

	if got := run(); got != 1 {
		t.Errorf("first ensure inserted %d rows, want 1 (adds this node's record)", got)
	}
	if got := run(); got != 0 {
		t.Errorf("second ensure inserted %d rows, want 0 (idempotent)", got)
	}

	var other int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE value='2.2.2.2'`).Scan(&other); err != nil {
		t.Fatalf("count: %v", err)
	}
	if other != 1 {
		t.Error("the other gateway node's record must be left untouched")
	}
	// New rows must be resolvable (is_active defaults TRUE).
	var active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE value=? AND is_active=1`, ip).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 1 {
		t.Errorf("ensured record must be resolvable (is_active=1), got %d", active)
	}
}
