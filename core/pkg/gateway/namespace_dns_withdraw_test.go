package gateway

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// dnsFixture builds an in-memory dns_records table holding the two host records
// for one namespace on each of the given IPs.
func dnsFixture(t *testing.T, ips ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE dns_records (
		fqdn TEXT, record_type TEXT, value TEXT, is_active INTEGER, updated_at TIMESTAMP
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, ip := range ips {
		for _, fqdn := range namespaceHostFQDNs("demo", "example.net") {
			if _, err := db.Exec(
				`INSERT INTO dns_records (fqdn, record_type, value, is_active, updated_at) VALUES (?, 'A', ?, 1, '2026-01-01 00:00:00')`,
				fqdn, ip); err != nil {
				t.Fatalf("seed row: %v", err)
			}
		}
	}
	return db
}

func activeIPs(t *testing.T, db *sql.DB, fqdn string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT value FROM dns_records WHERE fqdn = ? AND is_active = 1 ORDER BY value`, fqdn)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	return out
}

// An unhealthy node must stop being advertised, so clients stop resolving to a
// gateway that cannot answer.
func TestWithdrawRemovesThisNodeWhenOthersRemain(t *testing.T) {
	db := dnsFixture(t, "203.0.113.1", "203.0.113.2", "203.0.113.3")
	host := namespaceHostFQDNs("demo", "example.net")[0]

	res, err := db.Exec(withdrawNamespaceHostRecordSQL, "2026-02-02 00:00:00", host, "203.0.113.2", host)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("withdraw affected %d rows, want 1", n)
	}
	got := activeIPs(t, db, host)
	if len(got) != 2 || got[0] != "203.0.113.1" || got[1] != "203.0.113.3" {
		t.Errorf("active = %v, want the other two nodes", got)
	}
}

// Advertising a node that might still answer beats having no answer at all.
func TestWithdrawRefusesToRemoveTheLastRecord(t *testing.T) {
	db := dnsFixture(t, "203.0.113.1")
	host := namespaceHostFQDNs("demo", "example.net")[0]

	res, err := db.Exec(withdrawNamespaceHostRecordSQL, "2026-02-02 00:00:00", host, "203.0.113.1", host)
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("withdraw removed the last record (%d rows)", n)
	}
	if got := activeIPs(t, db, host); len(got) != 1 {
		t.Errorf("active = %v, want the single record kept", got)
	}
}

// Every node probes independently. Two nodes deciding "I am unhealthy" against
// separately-read snapshots must not be able to withdraw the last two records
// between them. The count lives inside the UPDATE precisely so the second write
// sees what the first one left.
func TestConcurrentWithdrawsCannotEmptyTheRoundRobin(t *testing.T) {
	db := dnsFixture(t, "203.0.113.1", "203.0.113.2")
	host := namespaceHostFQDNs("demo", "example.net")[0]

	for _, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		if _, err := db.Exec(withdrawNamespaceHostRecordSQL, "2026-02-02 00:00:00", host, ip, host); err != nil {
			t.Fatalf("withdraw %s: %v", ip, err)
		}
	}
	if got := activeIPs(t, db, host); len(got) != 1 {
		t.Errorf("active = %v, want exactly one survivor", got)
	}
}

// A record this process withdrew comes back as soon as the probe recovers,
// without waiting out the peer-verdict staleness window.
func TestRestoreOwnRecordNeedsNoStalenessWait(t *testing.T) {
	db := dnsFixture(t, "203.0.113.1", "203.0.113.2")
	host := namespaceHostFQDNs("demo", "example.net")[0]

	if _, err := db.Exec(withdrawNamespaceHostRecordSQL, "2026-02-02 00:00:00", host, "203.0.113.2", host); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := activeIPs(t, db, host); len(got) != 1 {
		t.Fatalf("setup: active = %v, want 1", got)
	}

	if _, err := db.Exec(restoreOwnNamespaceHostRecordSQL, "2026-02-02 00:00:01", host, "203.0.113.2"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := activeIPs(t, db, host); len(got) != 2 {
		t.Errorf("active = %v, want both records back", got)
	}
}

// The host and its wildcard must move together, or a client resolving the
// wildcard reaches a node the apex no longer advertises.
func TestNamespaceHostFQDNsCoversHostAndWildcard(t *testing.T) {
	got := namespaceHostFQDNs("demo", "example.net")
	want := []string{"ns-demo.example.net.", "*.ns-demo.example.net."}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("fqdns = %v, want %v", got, want)
	}
}
