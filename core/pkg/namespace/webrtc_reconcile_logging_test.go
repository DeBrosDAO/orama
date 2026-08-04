package namespace

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Bugboard #161 follow-up: the outage was invisible because both of
// ReconcileWebRTCAllocations' "do nothing" branches (no quorum, not the
// coordinator) returned silently — six hours of devnet gateway logs across
// both live nodes had zero lines matching reconcil|webrtc|turn|sfu|alloc.
// These tests pin that a skip is now always observable.
//
// Bugboard #170/#171 follow-up: viable and live members are now read from a
// SINGLE query (webrtcViableMemberSQL, extended to also select dn.status) so
// live is structurally a subset of viable — see getWebRTCMemberStatus. The
// mock router below matches on EXACT query text (query == webrtcViableMemberSQL
// / query == webrtcRawMemberCountSQL) rather than a substring like the
// original version of this file did — a substring on something as generic as
// "last_seen" could ambiguously match more than one query as the production
// SQL evolves, which is exactly what made the original two-query version of
// this mock fragile.

// newReconcileTestDB builds a mock DB that answers the two queries
// ReconcileWebRTCAllocations issues before it can reach a quorum/majority
// decision: the merged viable/live member-status read (one row per VIABLE
// node, keyed by node ID, with its dns_nodes.status), and the raw
// (dns_nodes-independent) member count.
func newReconcileTestDB(t *testing.T, viableStatus map[string]string, rawMemberCount int) *recoveryMockDB {
	t.Helper()
	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch query {
		case webrtcViableMemberSQL:
			for id, status := range viableStatus {
				appendToSlice(dest, map[string]any{"NodeID": id, "Status": status})
			}
		case webrtcRawMemberCountSQL:
			appendToSlice(dest, map[string]any{"Count": rawMemberCount})
		default:
			t.Fatalf("unexpected query in reconcile test: %s", query)
		}
		return nil
	}
	return db
}

// The exact live production shape that motivated bugboard #161: 3 viable
// members (a, b, c) but only 1 of them (a) currently active/live. Quorum
// requires live*2 > viable (1*2 > 3 is false), so this must skip loudly
// instead of silently no-op'ing — and it must never reach the raw-member-
// count query or touch the DB, since the decision is already made.
func TestReconcileWebRTCAllocations_noQuorumLogsReason(t *testing.T) {
	db := newReconcileTestDB(t, map[string]string{
		"a": "active",
		"b": "inactive",
		"c": "inactive",
	}, 3)

	core, observed := observer.New(zapcore.WarnLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core), localNodeID: "a"}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error, want nil (no-quorum is not a failure): %v", err)
	}

	entries := observed.FilterMessageSnippet("no quorum").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 'no quorum' log line, got %d: %+v", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["live"] != int64(1) || fields["viable_members"] != int64(3) {
		t.Errorf("quorum-skip log fields = %+v, want live=1 viable_members=3 (the actual numbers that blocked it)", fields)
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("no quorum must not touch the DB, got %d exec calls", len(db.getExecCalls()))
	}
	// Must never reach the raw-member-count query either — the decision was
	// already made and reached from a strictly smaller majority.
	for _, qc := range db.getQueryCalls() {
		if qc.Query == webrtcRawMemberCountSQL {
			t.Errorf("no-quorum skip queried raw member count, want it to return before that read")
		}
	}
}

// An empty viable set (every recorded member has aged out, or the cluster
// has no recorded members at all) must be a loud no-op, not grounds for
// treating the plan as "deallocate everything" — planWebRTCReallocation would
// happily produce exactly that plan if it were ever invoked with an empty
// member set, so ReconcileWebRTCAllocations must return before reaching it.
func TestReconcileWebRTCAllocations_emptyViableSetIsNoOp(t *testing.T) {
	db := newReconcileTestDB(t, map[string]string{}, 3)

	core, observed := observer.New(zapcore.WarnLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core), localNodeID: "a"}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error, want nil: %v", err)
	}

	entries := observed.FilterMessageSnippet("no viable cluster members recorded").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 empty-viable-set log line, got %d: %+v", len(entries), observed.All())
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("empty viable set must not deallocate anything, got %d exec calls: %+v", len(db.getExecCalls()), db.getExecCalls())
	}
	// Must never reach the quorum/majority/raw-count reads either — the
	// decision is made from the member-status read alone.
	for _, qc := range db.getQueryCalls() {
		if qc.Query == webrtcRawMemberCountSQL {
			t.Errorf("empty-viable-set skip queried raw member count, want it to return before that read")
		}
	}
}

// Every node computes the coordinator locally; the N-1 nodes that are not
// elected must also explain themselves in the logs, not just vanish.
func TestReconcileWebRTCAllocations_notCoordinatorLogsReason(t *testing.T) {
	// Two live, two-of-two viable — quorum and majority both pass — but this
	// node ("b") is not the lowest-sorted live member, so it must defer.
	db := newReconcileTestDB(t, map[string]string{
		"a": "active",
		"b": "active",
	}, 2)

	core, observed := observer.New(zapcore.DebugLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core), localNodeID: "b"}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error: %v", err)
	}

	entries := observed.FilterMessageSnippet("not the elected coordinator").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 'not coordinator' log line, got %d: %+v", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["coordinator"] != "a" || fields["local_node"] != "b" {
		t.Errorf("coordinator-skip log fields = %+v, want coordinator=a local_node=b", fields)
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("deferring to another coordinator must not touch the DB, got %d exec calls", len(db.getExecCalls()))
	}
}

// Bugboard #171: once live and viable are both derived from the same
// liveness signal, a LONE surviving node always passes the plain quorum
// check (1*2 > 1). This reproduces a cluster-wide outage where only node "a"
// has reported back in (viable={a}, live={a}) against 3 historically recorded
// members (b and c are simply absent from the viable read — long gone) and
// asserts the majority-held guard refuses to treat "a" as the whole cluster.
func TestReconcileWebRTCAllocations_massOutageFirstNodeBackDoesNotReallocate(t *testing.T) {
	db := newReconcileTestDB(t, map[string]string{
		"a": "active",
	}, 3)

	core, observed := observer.New(zapcore.WarnLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core), localNodeID: "a"}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error, want nil: %v", err)
	}

	entries := observed.FilterMessageSnippet("majority of recorded cluster membership is not viable").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 majority-guard log line, got %d: %+v", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["viable_members"] != int64(1) || fields["raw_members"] != int64(3) {
		t.Errorf("majority-guard log fields = %+v, want viable_members=1 raw_members=3", fields)
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("mass-outage skip must not reallocate anything, got %d exec calls: %+v", len(db.getExecCalls()), db.getExecCalls())
	}
}

// A node freshly restarted (startedAt within webrtcReconcileStartupGrace)
// must not act on its first read even when quorum and majority both pass —
// see webrtcReconcileStartupGrace. Single-node cluster (viable=live={a},
// raw=1) trivially satisfies both the quorum and majority checks, isolating
// the startup-grace gate as the thing under test.
func TestReconcileWebRTCAllocations_startupGraceSkipsFreshlyBootedNode(t *testing.T) {
	db := newReconcileTestDB(t, map[string]string{
		"a": "active",
	}, 1)

	core, observed := observer.New(zapcore.DebugLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core), localNodeID: "a", startedAt: time.Now()}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error, want nil: %v", err)
	}

	entries := observed.FilterMessageSnippet("startup grace period").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 startup-grace log line, got %d: %+v", len(entries), observed.All())
	}
	if len(db.getQueryCalls()) != 0 {
		t.Errorf("startup grace must return before issuing any query, got %d query calls: %+v", len(db.getQueryCalls()), db.getQueryCalls())
	}
	if len(db.getExecCalls()) != 0 {
		t.Errorf("startup grace must not touch the DB, got %d exec calls", len(db.getExecCalls()))
	}
}

// End-to-end test through ReconcileWebRTCAllocations for the FIXED devnet
// case (bugboard #170/#171/#173): 4 raw recorded members (2 of them
// permanently-dead corpses that have aged out of the viable set entirely —
// they simply don't appear in the viable read), 2 viable, 2 live. This is
// exactly the shape that used to deadlock forever under the old
// live*2 > RAW-members check (2*2 > 4 is false); under the fixed
// live*2 > VIABLE-members check it passes (2*2 > 2), the majority guard also
// passes (2 viable >= (4+1)/2 = 2), and node "nodeA" (lowest-sorted live
// member) is elected coordinator and must produce and apply a REAL plan —
// not just have the pure quorum helper return true in isolation.
func TestReconcileWebRTCAllocations_devnetFixedCaseProducesRealPlan(t *testing.T) {
	db := newReconcileTestDB(t, map[string]string{
		"nodeA": "active",
		"nodeB": "active",
	}, 4)
	baseQueryFunc := db.queryFunc
	db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "FROM webrtc_port_allocations") {
			// Current allocations: nodeA and nodeB (both viable/live) already
			// hold SFU + TURN. nodeC is a departed corpse — not in the viable
			// set above — that still holds a stray SFU role and must be
			// dropped.
			appendToSlice(dest, map[string]any{"NodeID": "nodeA", "ServiceType": "sfu"})
			appendToSlice(dest, map[string]any{"NodeID": "nodeA", "ServiceType": "turn"})
			appendToSlice(dest, map[string]any{"NodeID": "nodeB", "ServiceType": "sfu"})
			appendToSlice(dest, map[string]any{"NodeID": "nodeB", "ServiceType": "turn"})
			appendToSlice(dest, map[string]any{"NodeID": "nodeC", "ServiceType": "sfu"})
			return nil
		}
		return baseQueryFunc(dest, query, args...)
	}

	core, observed := observer.New(zapcore.InfoLevel)
	cm := &ClusterManager{
		db:                  db,
		webrtcPortAllocator: NewWebRTCPortAllocator(db, zap.NewNop()),
		logger:              zap.New(core),
		localNodeID:         "nodeA",
	}

	if err := cm.ReconcileWebRTCAllocations(context.Background(), "cluster-1", "anchat-test", DefaultTURNNodeCount); err != nil {
		t.Fatalf("ReconcileWebRTCAllocations returned error, want nil: %v", err)
	}

	entries := observed.FilterMessageSnippet("reallocating (bugboard #161)").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 'reallocating' log line proving a real plan was applied, got %d: %+v", len(entries), observed.All())
	}

	execCalls := db.getExecCalls()
	if len(execCalls) != 1 {
		t.Fatalf("expected exactly 1 exec call (deallocate nodeC's stray sfu role), got %d: %+v", len(execCalls), execCalls)
	}
	if !strings.Contains(execCalls[0].Query, "DELETE FROM webrtc_port_allocations") {
		t.Errorf("exec call = %q, want a webrtc_port_allocations delete", execCalls[0].Query)
	}
	if len(execCalls[0].Args) < 2 || execCalls[0].Args[1] != "nodeC" {
		t.Errorf("exec call args = %+v, want node_id=nodeC (the corpse) as the second bound arg", execCalls[0].Args)
	}
}
