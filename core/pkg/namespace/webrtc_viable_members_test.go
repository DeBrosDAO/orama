package namespace

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Bugboard #161 follow-up: the reconciler's original quorum/allocation math was
// denominated on EVERY row ever written to namespace_cluster_nodes, including
// members that died and were never cleaned up (RepairCluster only ever ADDS
// members; the dead-node health monitor did not fire for every node that
// actually died on devnet). Verified live on devnet anchat-test
// (cluster 40d47c32-9ac8-432a-8caa-68f51ac1ab6d): 4 recorded members, 2
// permanently dead (nAaTgW4M, v2pSvwWm), 2 live (YMHCnqiG, ZDbcmbJq) — an exact
// 50/50 split that can NEVER satisfy `live*2 > members`, so the reconciler
// silently no-op'd forever. These tests exercise the exact production query
// (webrtcViableMemberSQL) that fixes it: a member is only "viable" (counts
// toward quorum and toward "already holds this role") if it is live or was
// seen within webrtcMemberGracePeriod.
//
// webrtcViableMemberSQL also selects dn.status (bugboard #170) so production
// can derive the live subset from the same read without a second query —
// queryViableMembers below scans both columns to match.

func newViableMemberTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE namespace_cluster_nodes (
		namespace_cluster_id TEXT, node_id TEXT, role TEXT, status TEXT
	)`); err != nil {
		t.Fatalf("create namespace_cluster_nodes: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE dns_nodes (
		id TEXT, status TEXT, last_seen TEXT
	)`); err != nil {
		t.Fatalf("create dns_nodes: %v", err)
	}
	return db
}

// queryViableMembers runs the exact production query and returns the node
// IDs of every viable row. It scans both columns webrtcViableMemberSQL
// selects (node_id, status) even though most callers here only assert on the
// IDs — getWebRTCMemberStatus (cluster_manager_webrtc.go) relies on both
// being present in every row.
func queryViableMembers(t *testing.T, db *sql.DB, clusterID string) []string {
	t.Helper()
	graceModifier := fmt.Sprintf("-%d seconds", int(webrtcMemberGracePeriod.Seconds()))
	rows, err := db.Query(webrtcViableMemberSQL, clusterID, graceModifier)
	if err != nil {
		t.Fatalf("query viable members: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// Reproduces the exact devnet anchat-test membership: 2 members that have been
// dead for a long time (last_seen far outside the grace period) plus 2 live
// members. Only the 2 live members must count as viable — the permanently dead
// ones must not keep inflating the quorum/allocation denominator forever.
func TestWebRTCViableMemberSQL_reproducesDevnetAnchatTestState(t *testing.T) {
	db := newViableMemberTestDB(t)
	const clusterID = "40d47c32-9ac8-432a-8caa-68f51ac1ab6d"

	seedMember := func(nodeID, status, lastSeen string) {
		if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status) VALUES (?, ?, 'gateway', 'running')`, clusterID, nodeID); err != nil {
			t.Fatalf("seed member %s: %v", nodeID, err)
		}
		if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES (?, ?, ?)`, nodeID, status, lastSeen); err != nil {
			t.Fatalf("seed dns_node %s: %v", nodeID, err)
		}
	}

	// Dead weeks ago — well outside any reasonable grace period.
	seedMember("peer-nAaTgW4M", "inactive", "2026-07-01 00:00:00")
	seedMember("peer-v2pSvwWm", "inactive", "2026-07-01 00:00:00")
	// Live now. Uses datetime('now') rather than a hardcoded literal so this
	// asserts membership via the 'active' status branch regardless of when the
	// test runs — a fixed literal a day or two in the past would happen to
	// also pass through the grace-period OR branch, silently making that
	// branch untested here (bugboard #172 review).
	seedMember("peer-YMHCnqiG", "active", nowLiteral(t, db))
	seedMember("peer-ZDbcmbJq", "active", nowLiteral(t, db))

	// The value that MUST have permanently blocked the old (raw-membership)
	// quorum check: 4 recorded members total.
	var rawMemberCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM namespace_cluster_nodes WHERE namespace_cluster_id = ?`, clusterID).Scan(&rawMemberCount); err != nil {
		t.Fatalf("count raw members: %v", err)
	}
	if rawMemberCount != 4 {
		t.Fatalf("test setup: raw member count = %d, want 4", rawMemberCount)
	}
	if webrtcReconcileQuorumOK(2, rawMemberCount) {
		t.Fatal("test setup sanity check failed: 2 live of 4 raw members should NOT be a majority (that was the deadlock)")
	}

	viable := queryViableMembers(t, db, clusterID)
	if len(viable) != 2 {
		t.Fatalf("viable members = %v, want exactly the 2 live nodes (permanently dead members must not count)", viable)
	}
	for _, id := range viable {
		if id != "peer-YMHCnqiG" && id != "peer-ZDbcmbJq" {
			t.Errorf("unexpected viable member %q — only the live nodes should qualify", id)
		}
	}

	// With the fix, quorum is computed against viable members, not raw ones:
	// 2 live of 2 viable IS a majority, so the reconciler can now act.
	if !webrtcReconcileQuorumOK(2, len(viable)) {
		t.Error("quorum over viable members should pass — this is what unblocks the reconciler")
	}
}

// nowLiteral renders SQLite's current UTC instant as the same
// "YYYY-MM-DD HH:MM:SS" format last_seen writers use, so a seeded row reads
// as "live right now" without depending on the wall-clock time this test
// happens to run at.
func nowLiteral(t *testing.T, db *sql.DB) string {
	t.Helper()
	var now string
	if err := db.QueryRow(`SELECT datetime('now')`).Scan(&now); err != nil {
		t.Fatalf("select datetime('now'): %v", err)
	}
	return now
}

// A member that is merely mid-restart (down for seconds, well within the grace
// period) must still count as viable, or a routine service bounce would trigger
// a TURN/SFU migration — the exact thrash this reconciler exists to prevent.
func TestWebRTCViableMemberSQL_gracePeriodKeepsRecentlyDownMember(t *testing.T) {
	db := newViableMemberTestDB(t)
	const clusterID = "cluster-1"

	if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status) VALUES (?, 'peer-restarting', 'gateway', 'running')`, clusterID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	// Inactive (missed a heartbeat) but seen 30 seconds ago — inside the grace period.
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES ('peer-restarting', 'inactive', datetime('now', '-30 seconds'))`); err != nil {
		t.Fatalf("seed dns_node: %v", err)
	}

	viable := queryViableMembers(t, db, clusterID)
	if len(viable) != 1 || viable[0] != "peer-restarting" {
		t.Errorf("viable members = %v, want [peer-restarting] — a member down for 30s (inside the grace period) must still count", viable)
	}
}

// A member down for longer than the grace period must drop out of the viable
// set, freeing its role for a live node — this is what makes recovery from a
// node that never gets cleanly removed self-healing instead of permanent.
func TestWebRTCViableMemberSQL_excludesLongDeadMember(t *testing.T) {
	db := newViableMemberTestDB(t)
	const clusterID = "cluster-1"

	if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status) VALUES (?, 'peer-gone', 'gateway', 'running')`, clusterID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	// Inactive, and last seen well beyond the grace period.
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES ('peer-gone', 'inactive', datetime('now', '-1 hour'))`); err != nil {
		t.Fatalf("seed dns_node: %v", err)
	}

	viable := queryViableMembers(t, db, clusterID)
	if len(viable) != 0 {
		t.Errorf("viable members = %v, want none — a member dead for an hour must not count as viable", viable)
	}
}

// A node that is not a recorded cluster member at all must never appear, grace
// period or not — the JOIN is scoped to namespace_cluster_nodes for this cluster.
func TestWebRTCViableMemberSQL_excludesNonMembers(t *testing.T) {
	db := newViableMemberTestDB(t)
	const clusterID = "cluster-1"

	// Live node, but never recorded as a member of this cluster.
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES ('peer-outsider', 'active', datetime('now'))`); err != nil {
		t.Fatalf("seed dns_node: %v", err)
	}

	viable := queryViableMembers(t, db, clusterID)
	if len(viable) != 0 {
		t.Errorf("viable members = %v, want none — a live node that isn't a cluster member must never be counted", viable)
	}
}

// Bugboard #172 review: the pre-existing grace-period tests used -30s and
// -1h, wide enough that they would still pass even if webrtcMemberGracePeriod
// were wrong by an order of magnitude (e.g. 1m or 30m instead of 10m), or if
// the SQL modifier accidentally used Minutes() instead of Seconds() (a 60x
// error). These pin the exact boundary: a member last seen 9 minutes ago must
// still be viable, and one last seen 11 minutes ago must not — a test that
// only a genuinely-correct 10-minute grace period (measured in seconds) can
// pass.
func TestWebRTCViableMemberSQL_gracePeriodBoundary(t *testing.T) {
	if webrtcMemberGracePeriod.Minutes() != 10 {
		t.Fatalf("webrtcMemberGracePeriod = %s, want 10m — this test's -9m/-11m boundary assumes exactly 10 minutes", webrtcMemberGracePeriod)
	}

	tests := []struct {
		name       string
		lastSeen   string
		wantViable bool
	}{
		{"9m ago is inside the grace period", "-9 minutes", true},
		{"11m ago is outside the grace period", "-11 minutes", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newViableMemberTestDB(t)
			const clusterID = "cluster-1"

			if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status) VALUES (?, 'peer-boundary', 'gateway', 'running')`, clusterID); err != nil {
				t.Fatalf("seed member: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES ('peer-boundary', 'inactive', datetime('now', ?))`, tt.lastSeen); err != nil {
				t.Fatalf("seed dns_node: %v", err)
			}

			viable := queryViableMembers(t, db, clusterID)
			gotViable := len(viable) == 1 && viable[0] == "peer-boundary"
			if gotViable != tt.wantViable {
				t.Errorf("last_seen=%s: viable members = %v, want viable=%v", tt.lastSeen, viable, tt.wantViable)
			}
		})
	}
}
