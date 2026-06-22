package node

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newDNSTestDB builds an in-memory sqlite3 with the dns_records / dns_nameservers
// / dns_nodes schemas (mirroring migrations 009 / 011 / 005) so pinPushDesignated
// (bugboard #858) can be exercised against real SQL.
func newDNSTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE dns_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT, fqdn TEXT NOT NULL,
			record_type TEXT NOT NULL DEFAULT 'A', value TEXT NOT NULL,
			ttl INTEGER NOT NULL DEFAULT 300, priority INTEGER DEFAULT 0,
			namespace TEXT NOT NULL DEFAULT 'system', deployment_id TEXT, node_id TEXT,
			is_active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_by TEXT NOT NULL DEFAULT 'system',
			UNIQUE(fqdn, record_type, value))`,
		`CREATE TABLE dns_nameservers (
			hostname TEXT PRIMARY KEY, node_id TEXT NOT NULL, ip_address TEXT NOT NULL,
			domain TEXT NOT NULL, UNIQUE(node_id, domain))`,
		`CREATE TABLE dns_nodes (
			id TEXT PRIMARY KEY, ip_address TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// seedNS adds a nameserver + its node-health row. lastSeenExpr is a SQL datetime
// expression (e.g. "datetime('now')" or "datetime('now','-300 seconds')").
func seedNS(t *testing.T, db *sql.DB, host, ip, domain, lastSeenExpr string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO dns_nameservers (hostname, node_id, ip_address, domain) VALUES (?,?,?,?)`,
		host, "node-"+host, ip, domain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, status, last_seen) VALUES (?,?, 'active', `+lastSeenExpr+`)`,
		"node-"+host, ip); err != nil {
		t.Fatal(err)
	}
}

func pushRecords(t *testing.T, db *sql.DB, domain string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT value FROM dns_records WHERE fqdn=? AND record_type='A' AND is_active=1`, "push."+domain+".")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestPinPushDesignated_picksLowestHealthyNameserver(t *testing.T) {
	db := newDNSTestDB(t)
	seedNS(t, db, "ns1", "10.0.0.30", "ex.com", "datetime('now')")
	seedNS(t, db, "ns2", "10.0.0.10", "ex.com", "datetime('now')")
	seedNS(t, db, "ns3", "10.0.0.20", "ex.com", "datetime('now')")

	got, err := pinPushDesignated(context.Background(), db, "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.10" {
		t.Fatalf("designated = %q, want the lowest 10.0.0.10", got)
	}
	if recs := pushRecords(t, db, "ex.com"); len(recs) != 1 || recs[0] != "10.0.0.10" {
		t.Fatalf("push records = %v, want exactly [10.0.0.10]", recs)
	}
}

func TestPinPushDesignated_failsOverAndPrunes(t *testing.T) {
	db := newDNSTestDB(t)
	seedNS(t, db, "ns1", "10.0.0.10", "ex.com", "datetime('now')")
	seedNS(t, db, "ns2", "10.0.0.20", "ex.com", "datetime('now')")

	if got, _ := pinPushDesignated(context.Background(), db, "ex.com"); got != "10.0.0.10" {
		t.Fatalf("initial designated = %q, want 10.0.0.10", got)
	}
	// The designated node stops heartbeating.
	if _, err := db.Exec(`UPDATE dns_nodes SET last_seen=datetime('now','-300 seconds') WHERE ip_address='10.0.0.10'`); err != nil {
		t.Fatal(err)
	}

	got, err := pinPushDesignated(context.Background(), db, "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.20" {
		t.Fatalf("after failover designated = %q, want 10.0.0.20", got)
	}
	// The stale record must be pruned — exactly one push record, pointing at the survivor.
	if recs := pushRecords(t, db, "ex.com"); len(recs) != 1 || recs[0] != "10.0.0.20" {
		t.Fatalf("after failover push records = %v, want exactly [10.0.0.20]", recs)
	}
}

func TestPinPushDesignated_noHealthyNameserver_leavesWildcard(t *testing.T) {
	db := newDNSTestDB(t)
	seedNS(t, db, "ns1", "10.0.0.10", "ex.com", "datetime('now','-300 seconds')") // stale

	got, err := pinPushDesignated(context.Background(), db, "ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("designated = %q, want empty when no healthy nameserver", got)
	}
	if recs := pushRecords(t, db, "ex.com"); len(recs) != 0 {
		t.Fatalf("must not pin when no healthy nameserver; got %v", recs)
	}
}

func TestPinPushDesignated_idempotent(t *testing.T) {
	db := newDNSTestDB(t)
	seedNS(t, db, "ns1", "10.0.0.10", "ex.com", "datetime('now')")
	seedNS(t, db, "ns2", "10.0.0.20", "ex.com", "datetime('now')")
	for i := 0; i < 3; i++ {
		if _, err := pinPushDesignated(context.Background(), db, "ex.com"); err != nil {
			t.Fatal(err)
		}
	}
	if recs := pushRecords(t, db, "ex.com"); len(recs) != 1 || recs[0] != "10.0.0.10" {
		t.Fatalf("repeated runs must converge to exactly [10.0.0.10], got %v", recs)
	}
}
