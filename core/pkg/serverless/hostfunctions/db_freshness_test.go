package hostfunctions

// db_freshness_test.go covers the #1021 freshness boundary on db_query_batch:
// parsing/validation of the freshness field, routing to BatchQueryFresh, and
// the structured StaleRejected envelope on a freshness violation.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// freshAwareClient implements BatchQuery, BatchQueryConsistency, AND the
// optional BatchQueryFresh capability, recording how it was called and
// optionally returning a freshness violation.
type freshAwareClient struct {
	rqlite.Client
	batchQueryCalls  int
	consistencyCalls int
	freshCalls       int
	lastFreshness    time.Duration
	lastStrict       bool
	violate          bool
}

func (c *freshAwareClient) BatchQuery(ctx context.Context, ops []rqlite.BatchOp) ([]rqlite.OpResult, error) {
	c.batchQueryCalls++
	return []rqlite.OpResult{}, nil
}

func (c *freshAwareClient) BatchQueryConsistency(ctx context.Context, ops []rqlite.BatchOp, rc rqlite.ReadConsistency) ([]rqlite.OpResult, error) {
	c.consistencyCalls++
	return []rqlite.OpResult{}, nil
}

func (c *freshAwareClient) BatchQueryFresh(ctx context.Context, ops []rqlite.BatchOp, freshness time.Duration, strict bool) ([]rqlite.OpResult, error) {
	c.freshCalls++
	c.lastFreshness = freshness
	c.lastStrict = strict
	if c.violate {
		return nil, &rqlite.FreshnessError{Bound: freshness, Detail: "node stale"}
	}
	return []rqlite.OpResult{{Kind: rqlite.BatchOpQuery, Rows: []map[string]interface{}{{"ok": int64(1)}}}}, nil
}

func TestResolveBatchQuery_freshnessRoutesToBatchQueryFresh(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	ops := []rqlite.BatchOp{{Kind: rqlite.BatchOpQuery, SQL: "SELECT 1"}}
	if _, err := h.resolveBatchQuery(context.Background(), ops, "none", "2s", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.freshCalls != 1 || fake.consistencyCalls != 0 || fake.batchQueryCalls != 0 {
		t.Fatalf("freshness must route to BatchQueryFresh; fresh=%d cons=%d weak=%d",
			fake.freshCalls, fake.consistencyCalls, fake.batchQueryCalls)
	}
	if fake.lastFreshness != 2*time.Second {
		t.Errorf("freshness = %v; want 2s", fake.lastFreshness)
	}
	if !fake.lastStrict {
		t.Error("freshness_strict must be threaded through")
	}
}

func TestResolveBatchQuery_emptyFreshnessUnchanged(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	// consistency=none, freshness="" → ordinary none path (BatchQueryConsistency).
	if _, err := h.resolveBatchQuery(context.Background(), nil, "none", "", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.consistencyCalls != 1 || fake.freshCalls != 0 {
		t.Errorf("empty freshness must use the plain none path; cons=%d fresh=%d",
			fake.consistencyCalls, fake.freshCalls)
	}
}

func TestResolveBatchQuery_invalidFreshnessRejected(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	_, err := h.resolveBatchQuery(context.Background(), nil, "none", "not-a-duration", false)
	if err == nil {
		t.Fatal("an unparseable freshness must be rejected at the boundary")
	}
	if fake.freshCalls != 0 {
		t.Error("invalid freshness must not run any query")
	}
}

func TestResolveBatchQuery_freshnessWithWeakRejected(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	for _, consistency := range []string{"weak", ""} {
		_, err := h.resolveBatchQuery(context.Background(), nil, consistency, "2s", false)
		if err == nil {
			t.Fatalf("freshness with consistency=%q must be rejected", consistency)
		}
	}
	if fake.freshCalls != 0 || fake.batchQueryCalls != 0 {
		t.Error("a rejected freshness/consistency combo must not run any query")
	}
}

func TestParseFreshness(t *testing.T) {
	cases := []struct {
		name        string
		consistency string
		freshness   string
		want        time.Duration
		wantErr     bool
	}{
		{"empty is zero", "none", "", 0, false},
		{"empty with weak ok", "weak", "", 0, false},
		{"valid with none", "none", "500ms", 500 * time.Millisecond, false},
		{"valid with weak rejected", "weak", "500ms", 0, true},
		{"invalid duration", "none", "bad", 0, true},
		{"negative rejected", "none", "-1s", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFreshness(tc.consistency, tc.freshness)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for consistency=%q freshness=%q", tc.consistency, tc.freshness)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseFreshness(%q,%q) = %v; want %v", tc.consistency, tc.freshness, got, tc.want)
			}
		})
	}
}

func TestDBQueryBatch_freshnessViolationReturnsStaleRejected(t *testing.T) {
	fake := &freshAwareClient{violate: true}
	h := newHFWithDB(fake)

	in := []byte(`{"consistency":"none","freshness":"2s","ops":[{"sql":"SELECT 1"}]}`)
	out, err := h.DBQueryBatch(context.Background(), in)
	if err != nil {
		t.Fatalf("a freshness violation must be a structured envelope, not a Go error: %v", err)
	}
	var res dbQueryBatchResult
	if uErr := json.Unmarshal(out, &res); uErr != nil {
		t.Fatalf("unmarshal result: %v", uErr)
	}
	if !res.StaleRejected {
		t.Error("freshness violation must set stale_rejected=true")
	}
	if res.StaleDetail == "" {
		t.Error("freshness violation must carry a detail")
	}
	if len(res.Results) != 0 {
		t.Error("a freshness rejection must return no results (not a per-op success)")
	}
}

func TestDBQueryBatch_freshnessHappyPath(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	in := []byte(`{"consistency":"none","freshness":"1s","ops":[{"sql":"SELECT 1"}]}`)
	out, err := h.DBQueryBatch(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.freshCalls != 1 {
		t.Errorf("expected BatchQueryFresh to be called once, got %d", fake.freshCalls)
	}
	var res dbQueryBatchResult
	if uErr := json.Unmarshal(out, &res); uErr != nil {
		t.Fatalf("unmarshal result: %v", uErr)
	}
	if res.StaleRejected {
		t.Error("a successful fresh read must not be stale_rejected")
	}
	if len(res.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(res.Results))
	}
}

func TestDBQueryBatch_freshnessWithWeakRejectedAtHostBoundary(t *testing.T) {
	fake := &freshAwareClient{}
	h := newHFWithDB(fake)

	in := []byte(`{"consistency":"weak","freshness":"2s","ops":[{"sql":"SELECT 1"}]}`)
	if _, err := h.DBQueryBatch(context.Background(), in); err == nil {
		t.Fatal("freshness with consistency=weak must error at the host boundary")
	}
}
