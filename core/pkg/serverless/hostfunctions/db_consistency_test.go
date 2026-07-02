package hostfunctions

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// feat-6: DBQueryBatch gained an opt-in "consistency":"none" field so
// read-heavy functions can skip the cross-region leader hop. These pin the
// routing: "none" must reach the consistency-capable path, the default must
// stay on the always-fresh leader read, an incapable client must degrade
// safely, and an unknown value must be rejected at the boundary (never
// silently downgraded).

// consistencyAwareClient implements BatchQuery AND the optional
// BatchQueryConsistency capability, recording which path was taken.
type consistencyAwareClient struct {
	rqlite.Client
	batchQueryCalls  int
	consistencyCalls int
	lastConsistency  rqlite.ReadConsistency
}

func (c *consistencyAwareClient) BatchQuery(ctx context.Context, ops []rqlite.BatchOp) ([]rqlite.OpResult, error) {
	c.batchQueryCalls++
	return []rqlite.OpResult{}, nil
}

func (c *consistencyAwareClient) BatchQueryConsistency(ctx context.Context, ops []rqlite.BatchOp, rc rqlite.ReadConsistency) ([]rqlite.OpResult, error) {
	c.consistencyCalls++
	c.lastConsistency = rc
	return []rqlite.OpResult{}, nil
}

// weakOnlyClient implements only BatchQuery (no consistency capability), so a
// none-read must degrade to the leader-routed BatchQuery rather than failing.
type weakOnlyClient struct {
	rqlite.Client
	batchQueryCalls int
}

func (w *weakOnlyClient) BatchQuery(ctx context.Context, ops []rqlite.BatchOp) ([]rqlite.OpResult, error) {
	w.batchQueryCalls++
	return []rqlite.OpResult{}, nil
}

func TestResolveBatchQuery_noneRoutesToConsistencyPath(t *testing.T) {
	fake := &consistencyAwareClient{}
	h := newHFWithDB(fake)

	if _, err := h.resolveBatchQuery(context.Background(), []rqlite.BatchOp{{Kind: rqlite.BatchOpQuery, SQL: "SELECT 1"}}, "none", "", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.consistencyCalls != 1 || fake.batchQueryCalls != 0 {
		t.Fatalf("none must route to BatchQueryConsistency; got consistency=%d weak=%d", fake.consistencyCalls, fake.batchQueryCalls)
	}
	if fake.lastConsistency != rqlite.ReadConsistencyNone {
		t.Errorf("expected ReadConsistencyNone, got %q", fake.lastConsistency)
	}
}

func TestResolveBatchQuery_defaultAndWeakUseLeaderRoutedRead(t *testing.T) {
	for _, consistency := range []string{"", "weak"} {
		fake := &consistencyAwareClient{}
		h := newHFWithDB(fake)
		if _, err := h.resolveBatchQuery(context.Background(), nil, consistency, "", false); err != nil {
			t.Fatalf("consistency=%q unexpected error: %v", consistency, err)
		}
		if fake.batchQueryCalls != 1 || fake.consistencyCalls != 0 {
			t.Errorf("consistency=%q must use weak BatchQuery; got weak=%d consistency=%d",
				consistency, fake.batchQueryCalls, fake.consistencyCalls)
		}
	}
}

func TestResolveBatchQuery_noneDegradesWhenClientLacksCapability(t *testing.T) {
	fake := &weakOnlyClient{}
	h := newHFWithDB(fake)

	if _, err := h.resolveBatchQuery(context.Background(), nil, "none", "", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.batchQueryCalls != 1 {
		t.Errorf("none must degrade to BatchQuery when capability absent; got %d calls", fake.batchQueryCalls)
	}
}

func TestResolveBatchQuery_invalidConsistencyRejected(t *testing.T) {
	fake := &consistencyAwareClient{}
	h := newHFWithDB(fake)

	_, err := h.resolveBatchQuery(context.Background(), nil, "bogus", "", false)
	if err == nil {
		t.Fatal("invalid consistency must return an error, not silently downgrade")
	}
	if fake.batchQueryCalls != 0 || fake.consistencyCalls != 0 {
		t.Error("invalid consistency must not run any query")
	}
}

func TestDBQueryBatch_consistencyNoneRoutesLocal(t *testing.T) {
	fake := &consistencyAwareClient{}
	h := newHFWithDB(fake)

	in := []byte(`{"consistency":"none","ops":[{"sql":"SELECT 1"}]}`)
	if _, err := h.DBQueryBatch(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.consistencyCalls != 1 {
		t.Errorf("DBQueryBatch with consistency=none must route to the local read; got %d", fake.consistencyCalls)
	}
	if fake.lastConsistency != rqlite.ReadConsistencyNone {
		t.Errorf("expected ReadConsistencyNone, got %q", fake.lastConsistency)
	}
}

func TestDBQueryBatch_invalidConsistencyErrors(t *testing.T) {
	fake := &consistencyAwareClient{}
	h := newHFWithDB(fake)

	in := []byte(`{"consistency":"bogus","ops":[{"sql":"SELECT 1"}]}`)
	if _, err := h.DBQueryBatch(context.Background(), in); err == nil {
		t.Fatal("DBQueryBatch must reject an unknown consistency value")
	}
}
