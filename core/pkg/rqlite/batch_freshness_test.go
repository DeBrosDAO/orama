package rqlite

// batch_freshness_test.go covers the batch-layer wiring of the #1022 freshness
// gate (auto-degrade none→weak) and the #1021 BatchQueryFresh delegation rule.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rqlite/gorqlite"
)

// staleGateFor builds a gate whose injected check always returns the given
// freshness verdict, with a long ttl so the first verdict sticks.
func staleGateFor(fresh bool) *followerFreshnessGate {
	return newFollowerFreshnessGate(9999, func(int) (bool, string, error) {
		return fresh, "test", nil
	}, time.Hour)
}

func TestGateNoneConn_staleFollowerDegradesToWeak(t *testing.T) {
	weak := &gorqlite.Connection{}
	none := &gorqlite.Connection{}
	c := &client{conn: weak, connNone: none, staleGate: staleGateFor(false)}

	// A none-read whose initial pick is the none conn must flip to weak.
	got := c.gateNoneConn(ReadConsistencyNone, none)
	if got != weak {
		t.Error("stale follower must degrade the none-read to the weak (leader-routed) conn")
	}
}

func TestGateNoneConn_freshFollowerKeepsNone(t *testing.T) {
	weak := &gorqlite.Connection{}
	none := &gorqlite.Connection{}
	c := &client{conn: weak, connNone: none, staleGate: staleGateFor(true)}

	got := c.gateNoneConn(ReadConsistencyNone, none)
	if got != none {
		t.Error("fresh follower must keep the dedicated none connection")
	}
}

func TestGateNoneConn_nilGateIsNoOp(t *testing.T) {
	weak := &gorqlite.Connection{}
	none := &gorqlite.Connection{}
	c := &client{conn: weak, connNone: none, staleGate: nil}

	if got := c.gateNoneConn(ReadConsistencyNone, none); got != none {
		t.Error("with no gate configured, the none read must be left untouched")
	}
}

func TestGateNoneConn_weakReadUnaffected(t *testing.T) {
	weak := &gorqlite.Connection{}
	none := &gorqlite.Connection{}
	// Even a stale gate must not touch a weak read — only none-reads are gated.
	c := &client{conn: weak, connNone: none, staleGate: staleGateFor(false)}

	if got := c.gateNoneConn(ReadConsistencyWeak, weak); got != weak {
		t.Error("weak reads must never be rerouted by the freshness gate")
	}
}

func TestGateNoneConn_noNoneConnIsNoOp(t *testing.T) {
	weak := &gorqlite.Connection{}
	// connNone nil → the none-read already degraded to weak via queryConn; the
	// gate must not fire (nothing to degrade).
	c := &client{conn: weak, connNone: nil, staleGate: staleGateFor(false)}

	if got := c.gateNoneConn(ReadConsistencyNone, weak); got != weak {
		t.Error("with connNone nil the gate must be a no-op")
	}
}

func TestBatchQueryFresh_zeroFreshnessDelegatesToNonePath(t *testing.T) {
	// freshness <= 0 must take the existing BatchQueryConsistency(None) path,
	// NOT queryNoneFresh. With no native conn configured, the none path returns
	// the "native gorqlite connection not configured" error — proving delegation
	// (queryNoneFresh would instead error about the freshness HTTP path).
	c := &client{} // no conn, no connNone, no freshHTTP
	ops := []BatchOp{{Kind: BatchOpQuery, SQL: "SELECT 1"}}

	_, err := c.BatchQueryFresh(context.Background(), ops, 0, false)
	if err == nil {
		t.Fatal("expected an error from the delegated none path")
	}
	if !strings.Contains(err.Error(), "native gorqlite connection not configured") {
		t.Errorf("freshness<=0 must delegate to BatchQueryConsistency; got %v", err)
	}
}

func TestBatchQueryFresh_rejectsNonQueryOps(t *testing.T) {
	c := &client{}
	ops := []BatchOp{{Kind: BatchOpExec, SQL: "DELETE FROM t"}}

	_, err := c.BatchQueryFresh(context.Background(), ops, time.Second, false)
	if err == nil || !strings.Contains(err.Error(), "only \"query\" allowed") {
		t.Errorf("BatchQueryFresh must reject exec ops; got %v", err)
	}
}

func TestBatchQueryFresh_tooManyOps(t *testing.T) {
	c := &client{}
	ops := make([]BatchOp, MaxBatchOps+1)
	for i := range ops {
		ops[i] = BatchOp{Kind: BatchOpQuery, SQL: "SELECT 1"}
	}
	if _, err := c.BatchQueryFresh(context.Background(), ops, time.Second, false); err == nil {
		t.Error("BatchQueryFresh must enforce the MaxBatchOps cap")
	}
}

func TestBatchQueryFresh_emptyOps(t *testing.T) {
	c := &client{}
	out, err := c.BatchQueryFresh(context.Background(), nil, time.Second, false)
	if err != nil {
		t.Fatalf("empty ops must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty ops must yield empty results, got %d", len(out))
	}
}
