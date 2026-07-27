package storage

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// mockStorageDB is a minimal rqlite.Client for the eviction-path tests. It
// embeds the interface (so unimplemented methods panic if ever hit) and answers
// only the two SELECTs the evict path issues, plus the ownership SELECT and the
// pin-status UPDATE that UnpinHandler runs first.
type mockStorageDB struct {
	rqlite.Client
	pinCount      int      // remaining is_pinned=1 rows across ALL namespaces
	otherPinCount int      // is_pinned=1 rows in OTHER namespaces (bugboard #156)
	otherQueryErr error    // error to return from the cross-namespace check only
	nodeIPs       []string // active node internal IPs
	queryErr      error

	remainingQueried bool
	otherQueried     bool
	nodesQueried     bool
}

func (m *mockStorageDB) Query(_ context.Context, dest any, query string, _ ...any) error {
	if m.queryErr != nil {
		return m.queryErr
	}
	out, ok := dest.(*[]map[string]interface{})
	if !ok {
		return nil
	}
	switch {
	case strings.Contains(query, "namespace != ?") && strings.Contains(query, "ipfs_content_ownership"):
		// cidPinnedByOtherNamespace (bugboard #156) — must be checked before the
		// generic is_pinned branch (both contain "is_pinned = 1").
		m.otherQueried = true
		if m.otherQueryErr != nil {
			return m.otherQueryErr
		}
		*out = []map[string]interface{}{{"count": float64(m.otherPinCount)}}
	case strings.Contains(query, "is_pinned = 1") && strings.Contains(query, "ipfs_content_ownership"):
		m.remainingQueried = true
		*out = []map[string]interface{}{{"count": float64(m.pinCount)}}
	case strings.Contains(query, "dns_nodes"):
		m.nodesQueried = true
		rows := make([]map[string]interface{}, 0, len(m.nodeIPs))
		for _, ip := range m.nodeIPs {
			rows = append(rows, map[string]interface{}{"ip": ip})
		}
		*out = rows
	case strings.Contains(query, "ipfs_content_ownership"):
		// checkCIDOwnership: grant access (caller owns the CID).
		*out = []map[string]interface{}{{"count": float64(1)}}
	}
	return nil
}

func (m *mockStorageDB) Exec(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil // updatePinStatus UPDATE — no-op in tests
}

func newHandlersWithDB(client IPFSClient, db rqlite.Client) *Handlers {
	return New(client, newTestLogger(), Config{IPFSReplicationFactor: 3, IPFSAPIURL: "http://localhost:5001"}, db)
}

// --- remainingPinsForCID ------------------------------------------------------

func TestRemainingPinsForCID(t *testing.T) {
	for _, tc := range []struct {
		name string
		pin  int
		want int
	}{
		{"none", 0, 0},
		{"shared", 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandlersWithDB(&mockIPFSClient{}, &mockStorageDB{pinCount: tc.pin})
			got, err := h.remainingPinsForCID(context.Background(), "QmCID")
			if err != nil {
				t.Fatalf("remainingPinsForCID: %v", err)
			}
			if got != tc.want {
				t.Errorf("count = %d, want %d", got, tc.want)
			}
		})
	}
}

// --- maybeImmediateEvict gate -------------------------------------------------

func TestMaybeImmediateEvict_skippedWhenNotRequested(t *testing.T) {
	db := &mockStorageDB{pinCount: 0}
	h := newHandlersWithDB(&mockIPFSClient{}, db)
	if got := h.maybeImmediateEvict(context.Background(), "QmCID", false); got != "skipped" {
		t.Errorf("evicted = %q, want skipped", got)
	}
	if db.remainingQueried || db.nodesQueried {
		t.Error("no DB work should happen when immediate is not requested")
	}
}

func TestMaybeImmediateEvict_sharedCIDNotEvicted(t *testing.T) {
	// Another namespace still pins the CID → must NOT fan out an eviction.
	db := &mockStorageDB{pinCount: 2}
	h := newHandlersWithDB(&mockIPFSClient{}, db)
	if got := h.maybeImmediateEvict(context.Background(), "QmShared", true); got != "shared" {
		t.Errorf("evicted = %q, want shared", got)
	}
	if !db.remainingQueried {
		t.Error("expected the cross-namespace reference check to run")
	}
	if db.nodesQueried {
		t.Error("shared CID must short-circuit BEFORE fan-out (dns_nodes must not be queried)")
	}
}

func TestMaybeImmediateEvict_zeroPins_noNodes_partial(t *testing.T) {
	// Zero remaining pins but no active nodes to fan out to → best-effort partial.
	db := &mockStorageDB{pinCount: 0, nodeIPs: nil}
	h := newHandlersWithDB(&mockIPFSClient{}, db)
	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "partial" {
		t.Errorf("evicted = %q, want partial", got)
	}
	if !db.remainingQueried || !db.nodesQueried {
		t.Error("zero-pin path must check references AND attempt fan-out")
	}
}

// --- EvictHandler (per-node internal endpoint) --------------------------------

func evictReq(t *testing.T, body, remoteAddr, marker string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/internal/storage/evict", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	if marker != "" {
		req.Header.Set("X-Orama-Internal-Auth", marker)
	}
	return httptest.NewRecorder(), req
}

func TestEvictHandler_forbiddenWithoutWireGuard(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	rec, req := evictReq(t, `{"cid":"QmX"}`, "203.0.113.9:5000", storageInternalAuthMarker) // public IP
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestEvictHandler_forbiddenWithoutMarker(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	rec, req := evictReq(t, `{"cid":"QmX"}`, "10.0.0.7:5000", "") // WG IP but no marker
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestEvictHandler_wrongMethod(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	req := httptest.NewRequest(http.MethodGet, "/v1/internal/storage/evict", nil)
	req.RemoteAddr = "10.0.0.7:5000"
	req.Header.Set("X-Orama-Internal-Auth", storageInternalAuthMarker)
	rec := httptest.NewRecorder()
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestEvictHandler_missingCID(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	rec, req := evictReq(t, `{}`, "10.0.0.7:5000", storageInternalAuthMarker)
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEvictHandler_success(t *testing.T) {
	mock := &mockIPFSClient{evictRemoved: 4}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, `{"cid":"QmGone"}`, "10.0.0.7:5000", storageInternalAuthMarker)
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mock.evictCalls != 1 || len(mock.evictedCIDs) != 1 || mock.evictedCIDs[0] != "QmGone" {
		t.Errorf("EvictLocal not called with the CID; calls=%d cids=%v", mock.evictCalls, mock.evictedCIDs)
	}
	body := decodeBody(t, rec)
	if body["removed"] != float64(4) {
		t.Errorf("removed = %v, want 4", body["removed"])
	}
}

// --- UnpinHandler default (no immediate) --------------------------------------

func TestUnpinHandler_defaultDoesNotEvict(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock) // db=nil
	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/QmCID", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()
	h.UnpinHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mock.evictCalls != 0 {
		t.Errorf("default unpin must not evict; evictCalls=%d", mock.evictCalls)
	}
	if got := decodeBody(t, rec)["evicted"]; got != "skipped" {
		t.Errorf("evicted = %v, want skipped", got)
	}
}

// --- #156: cross-namespace shared-pin protection on PLAIN unpin ---------------

func unpinReq(ns string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/QmShared", nil)
	req = withNamespace(req, ns)
	return httptest.NewRecorder(), req
}

// A CID still pinned by ANOTHER namespace must NOT have its cluster pin removed
// (removing it would orphan the other namespace's data at the next GC).
func TestUnpinHandler_sharedByOtherNamespace_keepsClusterPin(t *testing.T) {
	mock := &mockIPFSClient{}
	db := &mockStorageDB{otherPinCount: 1} // another namespace still pins it
	h := newHandlersWithDB(mock, db)
	rec, req := unpinReq("ns-A")
	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mock.unpinCalls != 0 {
		t.Errorf("cluster Unpin must NOT be called for a CID another namespace still pins; unpinCalls=%d", mock.unpinCalls)
	}
	if mock.evictCalls != 0 {
		t.Errorf("shared CID must not be evicted; evictCalls=%d", mock.evictCalls)
	}
	body := decodeBody(t, rec)
	if body["shared"] != true || body["evicted"] != "shared" {
		t.Errorf("expected shared=true evicted=shared, got %v", body)
	}
}

// The LAST pinner (no other namespace) DOES remove the cluster pin.
func TestUnpinHandler_lastPinner_removesClusterPin(t *testing.T) {
	mock := &mockIPFSClient{}
	db := &mockStorageDB{otherPinCount: 0}
	h := newHandlersWithDB(mock, db)
	rec, req := unpinReq("ns-A")
	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mock.unpinCalls != 1 {
		t.Errorf("last pinner must remove the cluster pin exactly once; unpinCalls=%d", mock.unpinCalls)
	}
	if !db.otherQueried {
		t.Error("expected the cross-namespace reference check to run")
	}
}

// If the cross-namespace check errors, fail safe: do NOT remove the shared
// cluster pin, but still return 200 (this namespace is logically unpinned).
func TestUnpinHandler_refcountError_failsSafeLeavingPin(t *testing.T) {
	mock := &mockIPFSClient{}
	db := &mockStorageDB{otherQueryErr: errStorageTest}
	h := newHandlersWithDB(mock, db)
	rec, req := unpinReq("ns-A")
	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (logical unpin still succeeds)", rec.Code)
	}
	if mock.unpinCalls != 0 {
		t.Errorf("on refcount error the cluster pin must be left intact; unpinCalls=%d", mock.unpinCalls)
	}
}

var errStorageTest = errStorage("boom")

type errStorage string

func (e errStorage) Error() string { return string(e) }
