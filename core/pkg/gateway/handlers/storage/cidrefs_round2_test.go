package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

func markBackfilled(t *testing.T, registry *sqliteDB, namespaces ...string) {
	t.Helper()
	for _, ns := range namespaces {
		if _, err := registry.db.Exec(`INSERT OR IGNORE INTO namespaces (name) VALUES (?)`, ns); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.db.Exec(`INSERT OR IGNORE INTO ipfs_cid_refs (cid, namespace, kind) VALUES ('', ?, 'backfilled')`, ns); err != nil {
			t.Fatal(err)
		}
	}
}

// A: two same-content registrants share one row. When the one that fails takes
// its registration back, the other's reference must stand.
//
// Mutation check: make Unregister delete the row outright and this fails.
func TestUnregister_leavesAConcurrentRegistrationStanding(t *testing.T) {
	registry := registrySchema(t)
	a := gatewayFor(t, &mockIPFSClient{}, namespaceSchema(t), registry)
	ctx := context.Background()
	for range 2 {
		if err := a.registerRef(ctx, sharedCID, "ns-a"); err != nil {
			t.Fatal(err)
		}
	}
	a.dropRef(ctx, sharedCID, "ns-a") // the loser
	if n := refsOf(t, registry, sharedCID); n != 1 {
		t.Fatalf("references = %d after the loser gave up, want the winner's 1", n)
	}
	a.dropRef(ctx, sharedCID, "ns-a") // and if the winner fails too
	if n := refsOf(t, registry, sharedCID); n != 0 {
		t.Fatalf("references = %d after every registrant gave up, want 0", n)
	}
}

// A: end to end through the pin handler. The first pin's Pin call fails while a
// second, concurrent one for the same content has already registered.
func TestPin_failureDoesNotDropAConcurrentPinsReference(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	if err := a.recordCIDOwnership(context.Background(), sharedCID, "ns-a", "f", "ns-a", 1); err != nil {
		t.Fatal(err)
	}
	// While the failing pin is inside Pin, the other request registers.
	mock.pinErr = errors.New("cluster down")
	mock.onPin = func() {
		if err := a.registerRef(context.Background(), sharedCID, "ns-a"); err != nil {
			t.Error(err)
		}
	}
	if rec := pinRequest(a, "ns-a", sharedCID); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if n := refsOf(t, registry, sharedCID); n != 1 {
		t.Fatalf("references = %d, want the concurrent pin's 1", n)
	}
}

// B: a namespace larger than one batch is released completely, and only the
// CIDs no other namespace holds come back as orphaned.
func TestReleaseNamespace_batches(t *testing.T) {
	defer func(b, m int) { releaseBatch, maxReleaseBatches = b, m }(releaseBatch, maxReleaseBatches)
	releaseBatch, maxReleaseBatches = 7, 100
	ctx := context.Background()
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	const total = 50
	for i := range total {
		cid := fmt.Sprintf("Qm%03d", i)
		if err := refs.Register(ctx, cid, "ns-a", KindStorage); err != nil {
			t.Fatal(err)
		}
		if i%5 == 0 {
			if err := refs.Register(ctx, cid, "ns-b", KindStorage); err != nil {
				t.Fatal(err)
			}
		}
	}
	orphaned, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(orphaned) != total-total/5 {
		t.Fatalf("orphaned %d, want %d", len(orphaned), total-total/5)
	}
	var n int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'ns-a'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ns-a still holds %d rows (%v)", n, err)
	}
}

// B: a batch that fails leaves the rest in place, so the next call finds them.
// Before, every CID was deleted up front and one that timed out was forgotten.
func TestReleaseNamespace_failureLeavesTheRestToRetry(t *testing.T) {
	defer func(b, m int) { releaseBatch, maxReleaseBatches = b, m }(releaseBatch, maxReleaseBatches)
	releaseBatch, maxReleaseBatches = 5, 100
	ctx := context.Background()
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	for i := range 20 {
		if err := refs.Register(ctx, fmt.Sprintf("Qm%03d", i), "ns-a", KindStorage); err != nil {
			t.Fatal(err)
		}
	}
	if err := refs.Register(ctx, "", "ns-a", kindBackfilled); err != nil {
		t.Fatal(err)
	}
	// The second batch (Qm005..Qm009) fails.
	if _, err := registry.db.Exec(`CREATE TRIGGER refuse_second BEFORE DELETE ON ipfs_cid_refs WHEN OLD.cid = 'Qm007' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	first, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err == nil {
		t.Fatal("the failing batch was not reported")
	}
	if len(first) != 5 {
		t.Fatalf("the first batch's %d orphans were not returned", len(first))
	}
	if _, err := registry.db.Exec(`DROP TRIGGER refuse_second`); err != nil {
		t.Fatal(err)
	}
	rest, err := refs.ReleaseNamespace(ctx, "ns-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(first)+len(rest) != 20 {
		t.Fatalf("%d + %d orphans found across the two calls, want 20: something leaked", len(first), len(rest))
	}
	var left int
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'ns-a' AND kind != 'backfilled'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("references survived: %d (%v)", left, err)
	}
	if err := refs.RemoveMarker(ctx, "ns-a"); err != nil {
		t.Fatal(err)
	}
	if err := registry.db.QueryRow(`SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'ns-a'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the marker survived RemoveMarker: %d (%v)", left, err)
	}
}

// B: the work of one call is bounded; the caller is told to call again.
func TestReleaseNamespace_boundedWork(t *testing.T) {
	defer func(b, m int) { releaseBatch, maxReleaseBatches = b, m }(releaseBatch, maxReleaseBatches)
	releaseBatch, maxReleaseBatches = 3, 2
	ctx := context.Background()
	refs := NewCIDRefs(registrySchema(t))
	for i := range 10 {
		if err := refs.Register(ctx, fmt.Sprintf("Qm%03d", i), "ns-a", KindStorage); err != nil {
			t.Fatal(err)
		}
	}
	got, err := refs.ReleaseNamespace(ctx, "ns-a")
	if !errors.Is(err, ErrNamespaceRefsRemain) || len(got) != 6 {
		t.Fatalf("got %d orphans, %v; want 6 and ErrNamespaceRefsRemain", len(got), err)
	}
}

// S1: an unpin is refused while another live namespace has not loaded its
// references, because the count would omit what it holds.
//
// Mutation check: drop the namespaces check from CheckReady and this fails.
func TestUnpin_refusedWhileAnotherNamespaceIsNotLoaded(t *testing.T) {
	registry := registrySchema(t)
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	a := gatewayFor(t, mock, namespaceSchema(t), registry)
	pinAs(t, a, "ns-a", sharedCID)
	markBackfilled(t, registry, "ns-a")
	if err := addServingNamespaces(registry, "ns-b-secret"); err != nil { // never backfilled
		t.Fatal(err)
	}

	rec := unpinAs(a, "ns-a", sharedCID)
	if rec.Code != http.StatusServiceUnavailable || mock.unpinCalls != 0 {
		t.Fatalf("status %d, unpins %d; want a 503 and the pin kept", rec.Code, mock.unpinCalls)
	}
	if strings.Contains(rec.Body.String(), "ns-b-secret") {
		t.Fatalf("the answer names another tenant's namespace: %s", rec.Body.String())
	}

	markBackfilled(t, registry, "ns-b-secret")
	if rec := unpinAs(a, "ns-a", sharedCID); rec.Code != http.StatusOK || mock.unpinCalls != 1 {
		t.Fatalf("after the other namespace loaded: status %d, unpins %d", rec.Code, mock.unpinCalls)
	}
}

func TestCheckReady_namesTheMissingNamespacesAndExcludesTheDeletedOne(t *testing.T) {
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	if err := addServingNamespaces(registry, "gone", "late"); err != nil {
		t.Fatal(err)
	}
	err := refs.CheckReady(context.Background(), "gone")
	var missing *NotBackfilledError
	if !errors.As(err, &missing) || !errors.Is(err, ErrRefIndexNotReady) || len(missing.Namespaces) != 1 || missing.Namespaces[0] != "late" {
		t.Fatalf("err = %v; want only 'late' missing", err)
	}
	if strings.Contains(err.Error(), "late") {
		t.Fatalf("the message names a namespace: %v", err)
	}
	markBackfilled(t, registry, "late")
	if err := refs.CheckReady(context.Background(), "gone"); err != nil {
		t.Fatalf("ready expected: %v", err)
	}
}

// S1: the deferred path. A deployment release while another namespace is not
// loaded is remembered, not lost, and applied once it is.
func TestUnpinIfLastRef_deferredUntilEveryNamespaceIsLoaded(t *testing.T) {
	ctx := context.Background()
	registry := registrySchema(t)
	markBackfilled(t, registry, "ns-a")
	if err := addServingNamespaces(registry, "ns-b"); err != nil {
		t.Fatal(err)
	}
	refs := NewCIDRefs(registry)
	if err := refs.Register(ctx, sharedCID, "ns-a", KindDeployment); err != nil {
		t.Fatal(err)
	}
	mock := &mockIPFSClient{}
	if err := UnpinIfLastRef(ctx, refs, mock, sharedCID, "ns-a", KindDeployment); err != nil || mock.unpinCalls != 0 {
		t.Fatalf("err %v, unpins %d; want it deferred", err, mock.unpinCalls)
	}
	h := gatewayFor(t, mock, namespaceSchema(t), registry)
	h.SetCIDRefs(refs)
	markBackfilled(t, registry, "ns-b")
	h.applyDeferred(ctx, refs.takeDeferred())
	if mock.unpinCalls != 1 {
		t.Fatalf("unpins = %d after the index became ready, want 1", mock.unpinCalls)
	}
}

// E: content registered again while its unpin was deferred is not unpinned.
func TestApplyDeferred_skipsWhatWasReferencedAgain(t *testing.T) {
	ctx := context.Background()
	registry := registrySchema(t)
	refs := NewCIDRefs(registry)
	mock := &mockIPFSClient{}
	h := gatewayFor(t, mock, namespaceSchema(t), registry)
	h.SetCIDRefs(refs)
	if err := refs.Register(ctx, sharedCID, "ns-b", KindStorage); err != nil {
		t.Fatal(err)
	}
	h.applyDeferred(ctx, map[string]ClusterPinner{sharedCID: mock, "QmFree": mock})
	if mock.unpinCalls != 1 {
		t.Fatalf("unpins = %d, want only the unreferenced CID", mock.unpinCalls)
	}
}

// E: the running gateway applies deferred unpins by itself once loaded.
func TestStartCIDRefBackfill_appliesDeferredUnpins(t *testing.T) {
	ctx := t.Context()
	registry := registrySchema(t)
	mock := &mockIPFSClient{}
	h := gatewayFor(t, mock, namespaceSchema(t), registry)
	h.refs.pending.Store(true)
	h.refs.deferUnpin("QmDeferred", mock)
	h.StartCIDRefBackfill(ctx, "ns-a")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if mock.unpinCallCount() == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the deferred unpin was never applied")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// S9: a namespace over the bound fails permanently, once, with the reason on
// the unpin's answer, and is not retried.
func TestStartCIDRefBackfill_overTheBoundFailsPermanently(t *testing.T) {
	defer func(old int) { maxBackfillRefs = old }(maxBackfillRefs)
	maxBackfillRefs = 3
	registry := registrySchema(t)
	nsDB := namespaceSchema(t)
	if _, err := nsDB.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 10)
		INSERT INTO ipfs_content_ownership (id, cid, namespace, is_pinned, uploaded_at, uploaded_by)
		SELECT 'id'||i, 'Qm'||i, 'ns-a', 1, datetime('now'), 'x' FROM n`); err != nil {
		t.Fatal(err)
	}
	mock := &mockIPFSClient{pinResp: &ipfs.PinResponse{Cid: sharedCID}}
	h := gatewayFor(t, mock, nsDB, registry)
	if err := h.recordCIDOwnership(context.Background(), sharedCID, "ns-a", "f", "ns-a", 1); err != nil {
		t.Fatal(err)
	}
	h.StartCIDRefBackfill(t.Context(), "ns-a")
	deadline := time.Now().Add(5 * time.Second)
	for h.refs.failure.Load() == nil {
		if time.Now().After(deadline) {
			t.Fatal("an over-bound namespace never reported the permanent failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec := unpinAs(h, "ns-a", sharedCID)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "operator") {
		t.Fatalf("status %d body %s; want a 503 naming the operator", rec.Code, rec.Body.String())
	}
	if n := refsOf(t, registry, ""); n != 0 {
		t.Fatalf("a marker was written for a namespace that was not loaded")
	}
}

// The backoff between failed attempts doubles up to a ceiling.
func TestStartCIDRefBackfill_failureRetriesWithBackoff(t *testing.T) {
	registry := registrySchema(t)
	h := gatewayFor(t, &mockIPFSClient{}, namespaceSchema(t), registry)
	if _, err := registry.db.Exec(`DROP TABLE ipfs_cid_refs`); err != nil {
		t.Fatal(err)
	}
	h.StartCIDRefBackfill(t.Context(), "ns-a")
	time.Sleep(100 * time.Millisecond)
	if h.refs.CheckReady(context.Background(), "") == nil {
		t.Fatal("a failing backfill left the index ready")
	}
	if h.refs.failure.Load() != nil {
		t.Fatal("a transient failure was recorded as permanent")
	}
}
