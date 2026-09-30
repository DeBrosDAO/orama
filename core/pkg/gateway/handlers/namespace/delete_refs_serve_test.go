package namespace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

type countingDeprov struct{ calls int }

func (c *countingDeprov) DeprovisionCluster(context.Context, int64) error { c.calls++; return nil }

func deleteRequest(ns string) *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/v1/namespace/delete", nil)
	return req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, ns))
}

func seedNamespaces(t *testing.T, h *DeleteHandler, names ...string) {
	t.Helper()
	// The migrations seed the "default" namespace, whose gateway has loaded.
	if err := h.refs.Register(context.Background(), "", "default", "backfilled"); err != nil {
		t.Fatal(err)
	}
	for i, n := range names {
		if _, err := h.ormClient.Exec(context.Background(), `INSERT INTO namespaces (id, name) VALUES (?, ?)`, 100+i, n); err != nil {
			t.Fatal(err)
		}
	}
}

// S1: deleting a namespace while another live namespace has not loaded its
// references into the index would count without what that namespace holds. It
// is refused before the cluster is touched.
//
// Mutation check: remove the readiness check from ServeHTTP and this fails.
func TestServeHTTP_refusedWhileAnotherNamespaceIsNotLoaded(t *testing.T) {
	db := migratedDB(t)
	dp := &countingDeprov{}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), &unpinRecorder{}, nil, zap.NewNop())
	seedNamespaces(t, h, "gone", "other")
	addRefs(t, h, "gone", "QmA")
	if err := h.refs.Register(context.Background(), "", "gone", "backfilled"); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusServiceUnavailable || dp.calls != 0 {
		t.Fatalf("status %d, deprovisions %d; want a 503 before the cluster is touched", rec.Code, dp.calls)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'gone'`); n != 1 {
		t.Fatal("the namespace was deleted")
	}

	if err := h.refs.Register(context.Background(), "", "other", "backfilled"); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusOK || dp.calls != 1 {
		t.Fatalf("after the other namespace loaded: status %d (%s), deprovisions %d", rec.Code, rec.Body.String(), dp.calls)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'gone'`); n != 0 {
		t.Fatalf("%d rows, marker included, survived the delete", n)
	}
}

// C: a failure to release the namespace's references fails the delete, with a
// retryable answer, instead of leaving rows and the marker behind.
//
// Mutation check: make unpinNamespaceContent swallow the error and this fails.
func TestServeHTTP_failedReleaseFailsTheDeleteRetryably(t *testing.T) {
	db := migratedDB(t)
	dp := &countingDeprov{}
	h := NewDeleteHandler(dp, rqlite.NewClient(db), &unpinRecorder{}, nil, zap.NewNop())
	seedNamespaces(t, h, "gone")
	addRefs(t, h, "gone", "QmA")
	if err := h.refs.Register(context.Background(), "", "gone", "backfilled"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON ipfs_cid_refs BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 for a retry", rec.Code)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM namespaces WHERE name = 'gone'`); n != 1 {
		t.Fatal("the namespace row was deleted although its references were not released")
	}

	if _, err := db.Exec(`DROP TRIGGER refuse_delete`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, deleteRequest("gone"))
	if rec.Code != http.StatusOK {
		t.Fatalf("the retry answered %d: %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM ipfs_cid_refs WHERE namespace = 'gone'`); n != 0 {
		t.Fatalf("%d rows survived the retried delete", n)
	}
}
