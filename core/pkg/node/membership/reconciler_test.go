package membership

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func membershipDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE dns_nodes (
			id TEXT PRIMARY KEY,
			ip_address TEXT NOT NULL DEFAULT '',
			internal_ip TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE wireguard_peers (
			node_id TEXT PRIMARY KEY,
			wg_ip TEXT NOT NULL UNIQUE,
			public_key TEXT NOT NULL UNIQUE,
			public_ip TEXT NOT NULL DEFAULT '',
			wg_port INTEGER DEFAULT 51820
		);
		CREATE TABLE raft_evicted_nodes (
			node_id TEXT PRIMARY KEY,
			raft_addr TEXT NOT NULL DEFAULT '',
			peer_id TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL,
			evicted_by TEXT NOT NULL DEFAULT '',
			evicted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func seedNode(t *testing.T, db *sql.DB, peerID, ip string, secondsAgo int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, last_seen)
		 VALUES (?, '203.0.113.1', ?, 'active', datetime('now', ?))`,
		peerID, ip, secondsAgoModifier(secondsAgo)); err != nil {
		t.Fatalf("seed dns_nodes: %v", err)
	}
}

func seedWG(t *testing.T, db *sql.DB, ip string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO wireguard_peers (node_id, wg_ip, public_key) VALUES (?, ?, ?)`,
		"node-"+ip, ip, "key-"+ip); err != nil {
		t.Fatalf("seed wireguard_peers: %v", err)
	}
}

func seedTombstone(t *testing.T, db *sql.DB, raftAddr, peerID string, secondsAgo int) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_at)
		 VALUES (?, ?, ?, 'dead-voter', datetime('now', ?))`,
		raftAddr, raftAddr, peerID, secondsAgoModifier(secondsAgo)); err != nil {
		t.Fatalf("seed raft_evicted_nodes: %v", err)
	}
}

func secondsAgoModifier(secondsAgo int) string {
	return "-" + itoa(secondsAgo) + " seconds"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

type fakeDiscovery map[string]struct{}

func (f fakeDiscovery) LivePeerIDs() map[string]struct{} { return f }

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestReconcile_onlyTheLeaderWrites(t *testing.T) {
	db := membershipDB(t)
	seedNode(t, db, "peerGone", "10.0.0.9", 86400)
	seedWG(t, db, "10.0.0.9")
	seedTombstone(t, db, "10.0.0.9:10101", "peerGone", 3600)

	r := NewReconciler(db, fakeDiscovery{}, func() bool { return false }, nil)
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := countRows(t, db, "wireguard_peers"); got != 1 {
		t.Fatalf("a follower changed the cluster's membership: %d rows left", got)
	}
}

func TestReconcile_removesADepartedNodesWireGuardPeer(t *testing.T) {
	db := membershipDB(t)
	seedNode(t, db, "peerLive", "10.0.0.1", 30)
	seedWG(t, db, "10.0.0.1")
	seedNode(t, db, "peerGone", "10.0.0.9", 86400)
	seedWG(t, db, "10.0.0.9")
	seedTombstone(t, db, "10.0.0.9:10101", "peerGone", 3600)

	r := NewReconciler(db, fakeDiscovery{"peerLive": {}}, func() bool { return true }, nil)
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var remaining string
	if err := db.QueryRow(`SELECT node_id FROM wireguard_peers`).Scan(&remaining); err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if remaining != "node-10.0.0.1" {
		t.Fatalf("wrong peer survived: %q", remaining)
	}
	// The dns_nodes row is still inside TombstoneGrace.
	if got := countRows(t, db, "dns_nodes"); got != 2 {
		t.Fatalf("dns_nodes rows = %d, want 2 while inside the grace period", got)
	}
}

func TestReconcile_removesTheNodeRecordAfterTheGracePeriod(t *testing.T) {
	db := membershipDB(t)
	seedNode(t, db, "peerGone", "10.0.0.9", 86400)
	seedTombstone(t, db, "10.0.0.9:10101", "peerGone", int(TombstoneGrace.Seconds())+3600)

	r := NewReconciler(db, fakeDiscovery{}, func() bool { return true }, nil)
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := countRows(t, db, "dns_nodes"); got != 0 {
		t.Fatalf("dns_nodes rows = %d, want 0 after the grace period", got)
	}
}

// The reconciler must be safe to run repeatedly against a fleet that is fine.
func TestReconcile_isANoOpOnAHealthyFleet(t *testing.T) {
	db := membershipDB(t)
	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		seedNode(t, db, "peer"+itoa(i), ip, 30)
		seedWG(t, db, ip)
	}

	live := fakeDiscovery{"peer0": {}, "peer1": {}, "peer2": {}}
	r := NewReconciler(db, live, func() bool { return true }, nil)

	for i := 0; i < 3; i++ {
		if err := r.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile %d: %v", i, err)
		}
	}
	if got := countRows(t, db, "wireguard_peers"); got != 3 {
		t.Fatalf("wireguard_peers = %d, want 3 untouched", got)
	}
	if got := countRows(t, db, "dns_nodes"); got != 3 {
		t.Fatalf("dns_nodes = %d, want 3 untouched", got)
	}
}

// A tombstone written before peer ids were recorded still has to resolve, via
// the overlay address in the raft address.
func TestReconcile_resolvesATombstoneWithNoPeerIDThroughTheOverlayAddress(t *testing.T) {
	db := membershipDB(t)
	seedNode(t, db, "peerGone", "10.0.0.9", 86400)
	seedWG(t, db, "10.0.0.9")
	if _, err := db.Exec(
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_at)
		 VALUES (?, ?, '', 'dead-voter', datetime('now', '-3600 seconds'))`,
		"10.0.0.9:10101", "10.0.0.9:10101"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := NewReconciler(db, fakeDiscovery{}, func() bool { return true }, nil)
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := countRows(t, db, "wireguard_peers"); got != 0 {
		t.Fatalf("wireguard_peers = %d, want 0; the tombstone did not resolve", got)
	}
}

func TestReconcile_needsADatabase(t *testing.T) {
	r := NewReconciler(nil, fakeDiscovery{}, func() bool { return true }, nil)
	if err := r.Reconcile(context.Background()); err == nil {
		t.Fatal("a missing database handle must be reported")
	}
}

func TestParseTimestamp(t *testing.T) {
	tests := map[string]bool{
		"2026-09-03 12:00:00":  true,
		"2026-09-03T12:00:00Z": true,
		"":                     false,
		"not a time":           false,
	}
	for in, wantParsed := range tests {
		t.Run(in, func(t *testing.T) {
			got := parseTimestamp(in)
			if parsed := !got.IsZero(); parsed != wantParsed {
				t.Fatalf("parseTimestamp(%q) parsed = %v, want %v", in, parsed, wantParsed)
			}
			if wantParsed && got.Location() != time.UTC {
				t.Errorf("timestamp is not UTC: %v", got.Location())
			}
		})
	}
}
