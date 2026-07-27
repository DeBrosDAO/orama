package node

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// purgeInactiveTURNQuery is the exact production query under test (shared const,
// so a change to the production SQL is exercised here automatically).
const purgeInactiveTURNQuery = purgeInactiveTURNRecordsSQL

// purgeNamespaceHostQuery is the exact production query for the namespace GATEWAY
// host records (bugboard #158 follow-up: ns-<ns> still served a removed node).
const purgeNamespaceHostQuery = purgeInactiveNamespaceHostRecordsSQL

// testCutoff is the staleness cutoff passed to the purge queries. Fixtures whose
// nodes default to last_seen=2000 are therefore "long gone" relative to it.
const testCutoff = "2020-01-01 00:00:00"

func setupDNSTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// last_seen drives the staleness gate: records are only purged for nodes that
	// have been silent longer than purgeStaleAfter, so a heartbeat blip can't
	// delete a live node's records. Default it far in the past so a fixture that
	// doesn't care about staleness behaves like a long-gone node.
	if _, err := db.Exec(`CREATE TABLE dns_nodes (id TEXT PRIMARY KEY, ip_address TEXT, status TEXT, last_seen TIMESTAMP NOT NULL DEFAULT '2000-01-01 00:00:00')`); err != nil {
		t.Fatalf("create dns_nodes: %v", err)
	}
	// Mirrors migrations/009_dns_records_multi.sql for the columns these queries
	// touch. is_active is ESSENTIAL: the resolver serves only is_active=TRUE rows,
	// so a schema without it makes the "never NXDOMAIN" guard untestable — an
	// earlier version of this fixture omitted it and the safety test passed
	// vacuously while the production guard was actually bypassable.
	if _, err := db.Exec(`CREATE TABLE dns_records (
		fqdn TEXT, record_type TEXT, value TEXT, ttl INTEGER DEFAULT 60,
		namespace TEXT, created_by TEXT, created_at TEXT, updated_at TEXT,
		is_active BOOLEAN NOT NULL DEFAULT TRUE,
		UNIQUE(fqdn, record_type, value))`); err != nil {
		t.Fatalf("create dns_records: %v", err)
	}
	return db
}

// mustExec fails the test on a bad fixture insert. Silent fixture failures would
// make the row-count assertions pass vacuously.
func mustExec(t *testing.T, db *sql.DB, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("fixture exec failed: %v\nquery: %s", err, q)
	}
}

func countRecords(t *testing.T, db *sql.DB, where string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestPurgeInactiveNodeTURNRecords_query(t *testing.T) {
	db := setupDNSTestDB(t)

	// One active TURN node, one inactive (removed) TURN node.
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n-active','51.38.128.56','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n-dead','109.123.239.61','inactive')`)

	// TURN + stealth records for BOTH nodes, plus a system record on the dead IP
	// (must be left to cleanupStaleNodeRecords, not this purge).
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-anchat-test.d.','A','51.38.128.56','namespace-turn:anchat-test')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-anchat-test.d.','A','109.123.239.61','namespace-turn:anchat-test')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('cdn-x.d.','A','109.123.239.61','namespace-turn-stealth:anchat-test')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','109.123.239.61','system')`)

	res, err := db.Exec(purgeInactiveTURNQuery, testCutoff)
	if err != nil {
		t.Fatalf("purge exec: %v", err)
	}
	affected, _ := res.RowsAffected()
	if affected != 2 {
		t.Errorf("rows removed = %d, want 2 (the dead-node TURN + stealth records)", affected)
	}

	// Dead-node TURN + stealth records gone.
	if got := countRecords(t, db, `value = ? AND namespace LIKE 'namespace-turn%'`, "109.123.239.61"); got != 0 {
		t.Errorf("dead-node TURN/stealth records remaining = %d, want 0", got)
	}
	// Active-node TURN record preserved — calls still resolve to a live relay.
	if got := countRecords(t, db, `value = ? AND namespace = 'namespace-turn:anchat-test'`, "51.38.128.56"); got != 1 {
		t.Errorf("active-node TURN record = %d, want 1 (must be preserved)", got)
	}
	// System record on the dead IP is NOT this purge's job.
	if got := countRecords(t, db, `namespace = 'system'`); got != 1 {
		t.Errorf("system record count = %d, want 1 (untouched by TURN purge)", got)
	}
}

// Idempotent: a second run removes nothing and errors nothing.
func TestPurgeInactiveNodeTURNRecords_idempotent(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n-dead','109.123.239.61','inactive')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-x.d.','A','109.123.239.61','namespace-turn:x')`)

	if _, err := db.Exec(purgeInactiveTURNQuery, testCutoff); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	res, err := db.Exec(purgeInactiveTURNQuery, testCutoff)
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("second run removed %d rows, want 0 (idempotent)", affected)
	}
}

// Dup-IP guard: when a departed node's public IP is reused by an ACTIVE node,
// the active node's TURN record must be preserved (bugboard #158 review).
func TestPurgeInactiveNodeTURNRecords_reusedIP_preservesActive(t *testing.T) {
	db := setupDNSTestDB(t)
	// Same IP appears as an old inactive row AND a new active row (IP reuse).
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('old','203.0.113.5','inactive')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('new','203.0.113.5','active')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-z.d.','A','203.0.113.5','namespace-turn:z')`)

	res, err := db.Exec(purgeInactiveTURNQuery, testCutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows for a reused-but-now-active IP, want 0 (must not purge)", affected)
	}
}

// An active node whose records happen to share nothing with inactive nodes is
// never touched, even if it is the sole TURN node.
func TestPurgeInactiveNodeTURNRecords_allActive_noop(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n1','1.1.1.1','active')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-y.d.','A','1.1.1.1','namespace-turn:y')`)

	res, _ := db.Exec(purgeInactiveTURNQuery, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows for an all-active cluster, want 0", affected)
	}
}

// --- Namespace GATEWAY host records (ns-<ns> + *.ns-<ns>) ---
//
// Bugboard #158 follow-up: the original purge covered only the namespace-turn*
// tags, so `ns-anchat-test` kept serving a removed node (109.123.239.61) and
// ~1-in-3 WebSocket/RPC connections landed on a host that refuses :443.

// Reproduces the live devnet bug: 3 A records, one pointing at a removed node.
func TestPurgeInactiveNamespaceHostRecords_removesDeadNode(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n1','57.129.7.232','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n2','51.38.128.56','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)

	// The namespace host and its deployment wildcard both carry all three IPs.
	for _, fqdn := range []string{"ns-anchat-test.d.", "*.ns-anchat-test.d."} {
		for _, ip := range []string{"57.129.7.232", "51.38.128.56", "109.123.239.61"} {
			db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES (?,'A',?,'namespace:anchat-test')`, fqdn, ip)
		}
	}

	res, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("purge exec: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 2 {
		t.Errorf("rows removed = %d, want 2 (dead IP from both the host and its wildcard)", affected)
	}
	if got := countRecords(t, db, `value = ?`, "109.123.239.61"); got != 0 {
		t.Errorf("dead-node records remaining = %d, want 0", got)
	}
	// Both live nodes preserved on both names — the app must keep resolving.
	for _, fqdn := range []string{"ns-anchat-test.d.", "*.ns-anchat-test.d."} {
		if got := countRecords(t, db, `fqdn = ?`, fqdn); got != 2 {
			t.Errorf("%s has %d records, want 2 live", fqdn, got)
		}
	}
}

// THE CRITICAL SAFETY PROPERTY: if every node were flipped non-active (e.g. a
// cluster-wide heartbeat failure), the purge must NOT delete the last records and
// turn the namespace host into NXDOMAIN — nothing recreates them automatically,
// so that would be an unrecoverable total outage.
func TestPurgeInactiveNamespaceHostRecords_neverEmptiesTheFqdn(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n1','57.129.7.232','inactive')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('n2','51.38.128.56','inactive')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','57.129.7.232','namespace:anchat-test')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','51.38.128.56','namespace:anchat-test')`)

	res, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("purge exec: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows with NO live survivor, want 0 — must never NXDOMAIN the namespace host", affected)
	}
	if got := countRecords(t, db, `fqdn = 'ns-anchat-test.d.'`); got != 2 {
		t.Errorf("records = %d, want both kept (fail-safe)", got)
	}
}

// A single dead record with no survivor is likewise kept (same guard, N=1).
func TestPurgeInactiveNamespaceHostRecords_soleDeadRecordKept(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x')`)

	res, _ := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows, want 0 (deleting the only record would NXDOMAIN the namespace)", affected)
	}
}

// Tag isolation: the namespace-host purge must not touch system or TURN rows.
func TestPurgeInactiveNamespaceHostRecords_tagIsolation(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','1.1.1.1','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','system')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-x.d.','A','109.123.239.61','namespace-turn:x')`)

	if _, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := countRecords(t, db, `namespace = 'system'`); got != 1 {
		t.Error("system rows are cleanupStaleNodeRecords' job — must be untouched")
	}
	if got := countRecords(t, db, `namespace = 'namespace-turn:x'`); got != 1 {
		t.Error("TURN rows belong to the TURN purge — must be untouched here")
	}
}

// Reused-IP guard carries over: an IP held by a live node is never purged.
func TestPurgeInactiveNamespaceHostRecords_reusedIPPreserved(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('old','203.0.113.5','inactive')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('new','203.0.113.5','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('other','1.1.1.1','active')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-z.d.','A','203.0.113.5','namespace:z')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-z.d.','A','1.1.1.1','namespace:z')`)

	res, _ := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows for a reused-but-now-active IP, want 0", affected)
	}
}

// Idempotent: a second run is a no-op.
func TestPurgeInactiveNamespaceHostRecords_idempotent(t *testing.T) {
	db := setupDNSTestDB(t)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','1.1.1.1','active')`)
	db.Exec(`INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x')`)
	db.Exec(`INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x')`)

	if _, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff); err != nil {
		t.Fatalf("first purge: %v", err)
	}
	res, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("second purge: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("second run removed %d rows, want 0 (idempotent)", affected)
	}
}

// REGRESSION (caught in review): a soft-disabled row must NOT count as a
// survivor. The resolver serves only is_active=TRUE rows, and recovery's
// suspect-node path (DisableNamespaceRecord) sets is_active=FALSE on exactly
// these fqdns — so counting dark rows let the purge delete the last row that
// actually answered queries, turning degradation into a hard NXDOMAIN.
func TestPurgeInactiveNamespaceHostRecords_disabledRowIsNotASurvivor(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','51.38.128.56','active')`)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	// The only LIVE-node row was soft-disabled by recovery; the dead-node row is
	// the sole resolvable record. Deleting it would NXDOMAIN the namespace.
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('ns-x.d.','A','51.38.128.56','namespace:x',0)`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x',1)`)

	res, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows, want 0 — a disabled row is not a survivor, so this would have caused NXDOMAIN", affected)
	}
	// Prove the invariant in resolver terms: at least one resolvable A record left.
	if got := countRecords(t, db, `fqdn = 'ns-x.d.' AND record_type = 'A' AND is_active = 1`); got < 1 {
		t.Error("no resolvable A record left — NXDOMAIN")
	}
}

// A dark (is_active=0) dead row is never itself deleted: removing it changes
// nothing observable, and keeping it preserves recovery's ability to re-enable.
func TestPurgeInactiveNamespaceHostRecords_darkDeadRowUntouched(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','1.1.1.1','active')`)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x',1)`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x',0)`)

	res, _ := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d dark rows, want 0 (deleting a dark row changes nothing observable)", affected)
	}
}

// status='offline' (markNodeOffline, used by the dead-node path) must be treated
// as non-active too — documents the intended breadth of `status != 'active'`.
func TestPurgeInactiveNamespaceHostRecords_offlineStatusCounts(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','1.1.1.1','active')`)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('off','109.123.239.61','offline')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x')`)

	res, _ := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 1 {
		t.Errorf("removed %d rows, want 1 ('offline' is non-active)", affected)
	}
}

// A NULL ip_address must not collapse the dead-IP set (SQL three-valued logic)
// and silently disable the whole purge.
func TestPurgeInactiveNamespaceHostRecords_nullIPDoesNotDisablePurge(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('live','1.1.1.1','active')`)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('weird',NULL,'active')`)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x')`)

	res, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected != 1 {
		t.Errorf("removed %d rows, want 1 (a NULL ip_address must not disable the purge)", affected)
	}
}

// Non-A records on the same fqdn are neither deleted nor counted as survivors.
func TestPurgeInactiveNamespaceHostRecords_nonARecordsIgnored(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status) VALUES ('gone','109.123.239.61','inactive')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','TXT','hello','namespace:x')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','109.123.239.61','namespace:x')`)

	res, _ := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if affected, _ := res.RowsAffected(); affected != 0 {
		t.Errorf("removed %d rows, want 0 (a TXT record is not an A-record survivor)", affected)
	}
	if got := countRecords(t, db, `record_type = 'TXT'`); got != 1 {
		t.Error("TXT record must be untouched")
	}
}

// REGRESSION (caught in re-audit): the purge runs every 30s on every node, but the
// re-add counterpart (EnsureNamespaceHostRecordForNode) only runs at process boot.
// Without a staleness gate, a LIVE node whose heartbeat blipped for >120s would be
// flipped 'inactive' by a peer, have its records deleted within 30s, and never get
// them back until it restarted — permanently eroding the round-robin one node per
// blip. The gate means only a node silent past the cutoff is ever purged.
func TestPurge_recentlyFlappedNodeIsProtected(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status, last_seen) VALUES ('live','1.1.1.1','active','2020-06-01 00:00:00')`)
	// Marked inactive moments ago (a blip), NOT long-gone: last_seen is AFTER the cutoff.
	mustExec(t, db, `INSERT INTO dns_nodes (id, ip_address, status, last_seen) VALUES ('flapped','2.2.2.2','inactive','2020-06-01 00:00:00')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','1.1.1.1','namespace:x')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A','2.2.2.2','namespace:x')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn.ns-x.d.','A','2.2.2.2','namespace-turn:x')`)

	// cutoff is BEFORE last_seen → the node is not yet considered gone.
	nsRes, err := db.Exec(purgeNamespaceHostQuery, testCutoff, testCutoff)
	if err != nil {
		t.Fatalf("ns purge: %v", err)
	}
	if affected, _ := nsRes.RowsAffected(); affected != 0 {
		t.Errorf("namespace purge removed %d rows for a recently-flapped node, want 0", affected)
	}
	turnRes, err := db.Exec(purgeInactiveTURNQuery, testCutoff)
	if err != nil {
		t.Fatalf("turn purge: %v", err)
	}
	if affected, _ := turnRes.RowsAffected(); affected != 0 {
		t.Errorf("TURN purge removed %d rows for a recently-flapped node, want 0", affected)
	}

	// Once the node has been silent past the cutoff, it IS purged.
	lateCutoff := "2020-12-31 00:00:00"
	nsRes2, _ := db.Exec(purgeNamespaceHostQuery, lateCutoff, lateCutoff)
	if affected, _ := nsRes2.RowsAffected(); affected != 1 {
		t.Errorf("after the staleness window the dead record must be purged, removed %d want 1", affected)
	}
}

// Pins the REAL cutoff computation against a REAL SQLite clock. Every other purge
// test passes a literal cutoff, so without this the single highest-risk line in the
// change (staleCutoff: .UTC(), the layout, the sign of Add) is untested — get any
// of them wrong and the gate fails OPEN while the whole suite stays green.
func TestStaleCutoff_matchesSQLiteClockAndHonorsWindow(t *testing.T) {
	db := setupDNSTestDB(t)
	mustExec(t, db, `INSERT INTO dns_nodes (id,ip_address,status,last_seen) VALUES ('live','1.1.1.1','active',datetime('now'))`)
	// Silent longer than purgeStaleAfter → genuinely gone.
	mustExec(t, db, `INSERT INTO dns_nodes (id,ip_address,status,last_seen) VALUES ('gone','2.2.2.2','inactive',datetime('now','-16 minutes'))`)
	// Silent, but INSIDE the window → a blip, must be protected.
	mustExec(t, db, `INSERT INTO dns_nodes (id,ip_address,status,last_seen) VALUES ('blip','3.3.3.3','inactive',datetime('now','-14 minutes'))`)
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-x.d.','A',?,'namespace:x')`, ip)
	}

	cutoff := staleCutoff() // the exact production expression
	res, err := db.Exec(purgeNamespaceHostQuery, cutoff, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("removed %d rows, want exactly 1 — a cutoff format/timezone/sign mismatch vs datetime('now') would show up here", n)
	}
	if countRecords(t, db, `value = '2.2.2.2'`) != 0 {
		t.Error("the 16-min-silent node should have been purged")
	}
	if countRecords(t, db, `value = '3.3.3.3'`) != 1 {
		t.Error("the 14-min-silent node was purged — the staleness window is not being honored")
	}
	if countRecords(t, db, `value = '1.1.1.1'`) != 1 {
		t.Error("the live node's record must never be touched")
	}
}

// --- Periodic re-advertisement (ensureNamespaceHostRecords) ---
//
// This is the counterpart that makes the purge safe. It was originally boot-only,
// which let a healthy node that lost its record sit out of the round-robin until
// its next restart (observed on devnet). Exercises the exact production SQL.
func setupNamespaceClusterTables(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `CREATE TABLE namespace_clusters (id TEXT PRIMARY KEY, namespace_name TEXT, status TEXT)`)
	mustExec(t, db, `CREATE TABLE namespace_cluster_nodes (id TEXT PRIMARY KEY, namespace_cluster_id TEXT, node_id TEXT, role TEXT, status TEXT)`)
	mustExec(t, db, `INSERT INTO namespace_clusters VALUES ('c1','anchat-test','ready')`)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n1','c1','peerA','gateway','running')`)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n2','c1','peerB','gateway','running')`)
	// Not a gateway, and a stopped gateway — neither may be advertised.
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n3','c1','peerC','olric','running')`)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n4','c1','peerD','gateway','stopped')`)
}

func runEnsure(t *testing.T, db *sql.DB, prefix, base, ip, nodeID string) int64 {
	t.Helper()
	res, err := db.Exec(ensureNamespaceHostRecordsSQL,
		prefix, base, ip, "t", "t", nodeID, prefix, base, ip)
	if err != nil {
		t.Fatalf("ensure exec: %v", err)
	}
	n, _ := res.RowsAffected()
	return n
}

func TestEnsureNamespaceHostRecords_readvertisesAndIsIdempotent(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	// peerA's record is missing (the devnet situation); peerB already advertises.
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','2.2.2.2','namespace:anchat-test')`)

	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 1 {
		t.Fatalf("first ensure added %d rows, want 1 (peerA rejoins the round-robin)", got)
	}
	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 0 {
		t.Errorf("second ensure added %d rows, want 0 (idempotent)", got)
	}
	if countRecords(t, db, `fqdn='ns-anchat-test.d.'`) != 2 {
		t.Error("both gateway nodes should advertise")
	}
	// The other node's record is untouched.
	if countRecords(t, db, `value='2.2.2.2'`) != 1 {
		t.Error("must never disturb another node's record")
	}
}

func TestEnsureNamespaceHostRecords_onlyRunningGateways(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	// peerC is olric-only, peerD is a stopped gateway — neither may advertise.
	if got := runEnsure(t, db, "", "d", "3.3.3.3", "peerC"); got != 0 {
		t.Errorf("non-gateway node added %d rows, want 0", got)
	}
	if got := runEnsure(t, db, "", "d", "4.4.4.4", "peerD"); got != 0 {
		t.Errorf("stopped gateway added %d rows, want 0", got)
	}
	if countRecords(t, db, `1=1`) != 0 {
		t.Error("no records should have been created")
	}
}

// A soft-disabled row must block re-insert, so the reconcile can never resurrect
// traffic to a node the health monitor deliberately drained.
func TestEnsureNamespaceHostRecords_doesNotResurrectDisabledRow(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace,is_active) VALUES ('ns-anchat-test.d.','A','1.1.1.1','namespace:anchat-test',0)`)

	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 0 {
		t.Errorf("added %d rows over a deliberately disabled record, want 0", got)
	}
	if countRecords(t, db, `is_active=1`) != 0 {
		t.Error("the drained node must stay dark")
	}
}

// The wildcard is advertised alongside the apex.
func TestEnsureNamespaceHostRecords_wildcardPrefix(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	if got := runEnsure(t, db, "*.", "d", "1.1.1.1", "peerA"); got != 1 {
		t.Fatalf("wildcard ensure added %d rows, want 1", got)
	}
	if countRecords(t, db, `fqdn='*.ns-anchat-test.d.' AND value='1.1.1.1'`) != 1 {
		t.Error("wildcard record not created with the expected fqdn")
	}
}

// REGRESSION (both reviewers reproduced this): the NOT EXISTS guard matches on the
// namespace tag, but the table constraint is UNIQUE(fqdn,record_type,value) with NO
// tag — so a same-(fqdn,A,ip) row under a DIFFERENT tag slips past the guard and
// hits the constraint. As one INSERT..SELECT spanning every namespace this node
// gateways, that would abort ALL of them every 30s. ON CONFLICT DO NOTHING must
// degrade it to a no-op, and crucially must NOT stop other namespaces inserting.
func TestEnsureNamespaceHostRecords_tagCollisionDoesNotBlockOtherNamespaces(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	// peerA also gateways a SECOND namespace.
	mustExec(t, db, `INSERT INTO namespace_clusters VALUES ('c2','other-ns','ready')`)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n5','c2','peerA','gateway','running')`)
	// A colliding row for anchat-test only, under a foreign tag.
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','1.1.1.1','system')`)

	res, err := db.Exec(ensureNamespaceHostRecordsSQL, "", "d", "1.1.1.1", "t", "t", "peerA", "", "d", "1.1.1.1")
	if err != nil {
		t.Fatalf("a tag collision must not error the whole statement: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("inserted %d rows, want 1 — other-ns must still be advertised despite anchat-test colliding", n)
	}
	if countRecords(t, db, `fqdn='ns-other-ns.d.' AND value='1.1.1.1'`) != 1 {
		t.Error("the non-colliding namespace must still have been advertised")
	}
}

// One statement must cover EVERY namespace this node gateways, not just the first.
func TestEnsureNamespaceHostRecords_coversAllHostedNamespaces(t *testing.T) {
	db := setupDNSTestDB(t)
	setupNamespaceClusterTables(t, db)
	mustExec(t, db, `INSERT INTO namespace_clusters VALUES ('c2','other-ns','ready')`)
	mustExec(t, db, `INSERT INTO namespace_cluster_nodes VALUES ('n5','c2','peerA','gateway','running')`)

	if got := runEnsure(t, db, "", "d", "1.1.1.1", "peerA"); got != 2 {
		t.Fatalf("added %d rows, want 2 (one per hosted namespace)", got)
	}
	for _, f := range []string{"ns-anchat-test.d.", "ns-other-ns.d."} {
		if countRecords(t, db, `fqdn=? AND value='1.1.1.1'`, f) != 1 {
			t.Errorf("%s not advertised", f)
		}
	}
}

// --- Self-retraction of TURN records (membership, not liveness) ---
//
// Devnet: 57.129.7.232 was advertised on turn-anchat-test but held NO TURN
// allocation (it had been removed from the namespace cluster), so ~50% of relay
// connections hit a dead endpoint. The inactive-node purge cannot catch this — the
// node is alive and heartbeating. Each node retracts its OWN stale TURN records.
func setupWebRTCAllocTables(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `CREATE TABLE namespace_clusters (id TEXT PRIMARY KEY, namespace_name TEXT)`)
	mustExec(t, db, `CREATE TABLE webrtc_port_allocations (node_id TEXT, namespace_cluster_id TEXT, service_type TEXT)`)
	mustExec(t, db, `INSERT INTO namespace_clusters VALUES ('c1','anchat-test')`)
	// peerTurn is a real TURN node; peerSFU only has an SFU allocation.
	mustExec(t, db, `INSERT INTO webrtc_port_allocations VALUES ('peerTurn','c1','turn')`)
	mustExec(t, db, `INSERT INTO webrtc_port_allocations VALUES ('peerSFU','c1','sfu')`)
}

func TestRetractForeignTURNRecords_removesOwnStaleRecords(t *testing.T) {
	db := setupDNSTestDB(t)
	setupWebRTCAllocTables(t, db)
	// peerSFU (1.1.1.1) is wrongly advertised for TURN and stealth.
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn-anchat-test.d.','A','1.1.1.1','namespace-turn:anchat-test')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('cdn-x.d.','A','1.1.1.1','namespace-turn-stealth:anchat-test')`)
	// The real TURN node's record, and an unrelated gateway record.
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn-anchat-test.d.','A','2.2.2.2','namespace-turn:anchat-test')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('ns-anchat-test.d.','A','1.1.1.1','namespace:anchat-test')`)

	res, err := db.Exec(retractForeignTURNRecordsSQL, "1.1.1.1", "peerSFU")
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 2 {
		t.Errorf("removed %d rows, want 2 (own turn + stealth records)", n)
	}
	if countRecords(t, db, `value='2.2.2.2'`) != 1 {
		t.Error("the real TURN node's record must survive — a node may only retract ITSELF")
	}
	if countRecords(t, db, `namespace='namespace:anchat-test'`) != 1 {
		t.Error("gateway records are not this reconciler's business")
	}
}

// A genuine TURN node must never retract itself.
func TestRetractForeignTURNRecords_realTURNNodeKeepsRecord(t *testing.T) {
	db := setupDNSTestDB(t)
	setupWebRTCAllocTables(t, db)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn-anchat-test.d.','A','2.2.2.2','namespace-turn:anchat-test')`)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('cdn-x.d.','A','2.2.2.2','namespace-turn-stealth:anchat-test')`)

	res, err := db.Exec(retractForeignTURNRecordsSQL, "2.2.2.2", "peerTurn")
	if err != nil {
		t.Fatalf("retract: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("removed %d rows from a real TURN node, want 0", n)
	}
}

// Self-limited: passing another node's IP must not delete that node's records.
func TestRetractForeignTURNRecords_cannotTouchAPeer(t *testing.T) {
	db := setupDNSTestDB(t)
	setupWebRTCAllocTables(t, db)
	mustExec(t, db, `INSERT INTO dns_records (fqdn,record_type,value,namespace) VALUES ('turn-anchat-test.d.','A','2.2.2.2','namespace-turn:anchat-test')`)
	// peerSFU runs the statement but with its OWN ip (1.1.1.1) — 2.2.2.2 is untouched.
	if _, err := db.Exec(retractForeignTURNRecordsSQL, "1.1.1.1", "peerSFU"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if countRecords(t, db, `value='2.2.2.2'`) != 1 {
		t.Error("a node must never be able to retract a peer's record")
	}
}
