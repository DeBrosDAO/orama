package namespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

// Bugboard #173: namespace_cluster_nodes accumulates rows for permanently-dead
// nodes because RepairCluster only ever ADDS members, and removeClusterNodeAssignment
// was otherwise only reachable through ReplaceClusterNode — which only runs when
// HandleDeadNode's ring-based quorum confirms a node dead, something a node whose
// dns_nodes.status flips to 'inactive' via the DNS heartbeat sweep can permanently
// evade (see pruneStaleClusterNodes doc comment in cluster_recovery.go for the full
// race). pruneStaleClusterNodes closes that gap directly from dns_nodes staleness.
//
// These tests exercise the exact production query (staleClusterNodeSQL) against a
// real SQLite DB, mirroring the pattern webrtc_viable_members_test.go uses for
// webrtcViableMemberSQL, plus the ClusterManager method against a mock DB.

func newStaleClusterNodeTestDB(t *testing.T) *sql.DB {
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

func queryStaleClusterNodes(t *testing.T, db *sql.DB, clusterID string) []string {
	t.Helper()
	cutoff := time.Now().UTC().Add(-clusterNodePurgeStaleAfter).Format("2006-01-02 15:04:05")
	rows, err := db.Query(staleClusterNodeSQL, clusterID, cutoff)
	if err != nil {
		t.Fatalf("query stale cluster nodes: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func seedClusterNode(t *testing.T, db *sql.DB, clusterID, nodeID, dnsStatus, lastSeen string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO namespace_cluster_nodes (namespace_cluster_id, node_id, role, status) VALUES (?, ?, 'gateway', 'running')`, clusterID, nodeID); err != nil {
		t.Fatalf("seed member %s: %v", nodeID, err)
	}
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, status, last_seen) VALUES (?, ?, ?)`, nodeID, dnsStatus, lastSeen); err != nil {
		t.Fatalf("seed dns_node %s: %v", nodeID, err)
	}
}

// A node down for less than clusterNodePurgeStaleAfter (15m) — including the
// entire span a routine rolling restart or brief outage occupies — must NOT be
// pruned. This is the exact scenario the ticket calls out: "does NOT evict
// during a rolling restart."
func TestStaleClusterNodeSQL_doesNotEvictDuringRollingRestart(t *testing.T) {
	if clusterNodePurgeStaleAfter.Minutes() != 15 {
		t.Fatalf("clusterNodePurgeStaleAfter = %s, want 15m", clusterNodePurgeStaleAfter)
	}

	db := newStaleClusterNodeTestDB(t)
	const clusterID = "cluster-1"

	// Inactive for 10 minutes — well into a plausible rolling-restart or
	// transient-outage window, and still short of the 15m purge horizon.
	seedClusterNode(t, db, clusterID, "peer-restarting", "inactive", "")
	if _, err := db.Exec(`UPDATE dns_nodes SET last_seen = datetime('now', '-10 minutes') WHERE id = 'peer-restarting'`); err != nil {
		t.Fatalf("set last_seen: %v", err)
	}

	stale := queryStaleClusterNodes(t, db, clusterID)
	if len(stale) != 0 {
		t.Errorf("stale nodes = %v, want none — a node down 10m (inside the 15m purge horizon) must not be evicted", stale)
	}
}

// A node genuinely gone for well beyond the purge horizon must be selected so
// its row can be removed and stop inflating the quorum/allocation denominator
// forever (the devnet #173 deadlock).
func TestStaleClusterNodeSQL_selectsPermanentlyGoneMember(t *testing.T) {
	db := newStaleClusterNodeTestDB(t)
	const clusterID = "cluster-1"

	seedClusterNode(t, db, clusterID, "peer-gone", "inactive", "2026-07-01 00:00:00")

	stale := queryStaleClusterNodes(t, db, clusterID)
	if len(stale) != 1 || stale[0] != "peer-gone" {
		t.Errorf("stale nodes = %v, want [peer-gone]", stale)
	}
}

// A member that is 'active' in dns_nodes must never be selected regardless of
// last_seen — status is checked first, matching webrtcViableMemberSQL's
// structure.
func TestStaleClusterNodeSQL_neverSelectsActiveMember(t *testing.T) {
	db := newStaleClusterNodeTestDB(t)
	const clusterID = "cluster-1"

	seedClusterNode(t, db, clusterID, "peer-live", "active", "2026-01-01 00:00:00")

	stale := queryStaleClusterNodes(t, db, clusterID)
	if len(stale) != 0 {
		t.Errorf("stale nodes = %v, want none — status='active' must never be pruned regardless of last_seen", stale)
	}
}

// Exact boundary: 14m inactive stays inside the horizon, 16m inactive is
// outside it. Wide enough apart to not be flaky on test execution time, tight
// enough that a wrong constant (e.g. 5m or 30m) would fail this.
func TestStaleClusterNodeSQL_purgeHorizonBoundary(t *testing.T) {
	tests := []struct {
		name        string
		inactiveFor string
		wantStale   bool
	}{
		{"14m inactive is inside the purge horizon", "-14 minutes", false},
		{"16m inactive is outside the purge horizon", "-16 minutes", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newStaleClusterNodeTestDB(t)
			const clusterID = "cluster-1"
			seedClusterNode(t, db, clusterID, "peer-boundary", "inactive", "")
			if _, err := db.Exec(`UPDATE dns_nodes SET last_seen = datetime('now', ?) WHERE id = 'peer-boundary'`, tt.inactiveFor); err != nil {
				t.Fatalf("set last_seen: %v", err)
			}

			stale := queryStaleClusterNodes(t, db, clusterID)
			gotStale := len(stale) == 1 && stale[0] == "peer-boundary"
			if gotStale != tt.wantStale {
				t.Errorf("inactiveFor=%s: stale=%v, want stale=%v", tt.inactiveFor, stale, tt.wantStale)
			}
		})
	}
}

// pruneStaleClusterNodes (the ClusterManager method) must delete exactly the
// rows the query returns and report them back to the caller.
func TestPruneStaleClusterNodes_removesReturnedRowsAndReportsThem(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if query != staleClusterNodeSQL {
			t.Fatalf("unexpected query: %s", query)
		}
		appendToSlice(dest, map[string]any{"NodeID": "peer-gone-1"})
		appendToSlice(dest, map[string]any{"NodeID": "peer-gone-2"})
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	removed, err := cm.pruneStaleClusterNodes(context.Background(), "cluster-1")
	if err != nil {
		t.Fatalf("pruneStaleClusterNodes returned error: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v, want 2 node IDs", removed)
	}

	execCalls := db.getExecCalls()
	if len(execCalls) != 2 {
		t.Fatalf("expected 2 delete exec calls, got %d: %+v", len(execCalls), execCalls)
	}
	gotNodeIDs := make(map[string]bool, len(execCalls))
	for _, ec := range execCalls {
		if !strings.Contains(ec.Query, "DELETE FROM namespace_cluster_nodes") {
			t.Errorf("exec query = %q, want a namespace_cluster_nodes delete", ec.Query)
		}
		if len(ec.Args) >= 2 {
			gotNodeIDs[ec.Args[1].(string)] = true
		}
	}
	if !gotNodeIDs["peer-gone-1"] || !gotNodeIDs["peer-gone-2"] {
		t.Errorf("deleted node IDs = %v, want peer-gone-1 and peer-gone-2", gotNodeIDs)
	}
}

// A cluster with no stale members must not touch the DB beyond the read.
func TestPruneStaleClusterNodes_noStaleMembersIsNoop(t *testing.T) {
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		if query != staleClusterNodeSQL {
			t.Fatalf("unexpected query: %s", query)
		}
		return nil
	}

	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	removed, err := cm.pruneStaleClusterNodes(context.Background(), "cluster-1")
	if err != nil {
		t.Fatalf("pruneStaleClusterNodes returned error: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want none", removed)
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("expected no exec calls, got %d: %+v", len(db.getExecCalls()), db.getExecCalls())
	}
}
