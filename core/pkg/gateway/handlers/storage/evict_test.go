package storage

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
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

	// namespaceScoped marks this mock as a NAMESPACE gateway's own database.
	// dns_nodes exists there but is never written, so it answers topology reads
	// with zero rows — exactly what production did before bugboard #153. Any
	// code path that reads topology from this handle is therefore silently
	// broken, and topologyReadHere records that it happened.
	namespaceScoped  bool
	topologyReadHere bool
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
		if m.namespaceScoped {
			m.topologyReadHere = true
			*out = nil
			return nil
		}
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

// newHandlersWithDB builds handlers the way a NAMESPACE gateway is wired: `db`
// is the namespace's own RQLite and a separate handle carries the main
// cluster's topology. The namespace handle is marked namespaceScoped so it
// answers dns_nodes with nothing, reproducing production; the node list moves
// to the global handle.
func newHandlersWithDB(client IPFSClient, db rqlite.Client) *Handlers {
	global := &mockStorageDB{}
	if m, ok := db.(*mockStorageDB); ok {
		m.namespaceScoped = true
		global.nodeIPs = m.nodeIPs
	}
	return newHandlersWithDBs(client, db, global)
}

func newHandlersWithDBs(client IPFSClient, db, globalDB rqlite.Client) *Handlers {
	return New(client, newTestLogger(), Config{IPFSReplicationFactor: 3, IPFSAPIURL: "http://localhost:5001"}, db, globalDB)
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
	// A genuinely empty cluster topology cannot reclaim anything → partial.
	// This is the honest failure signal; the bug it used to hide is covered by
	// TestActiveNodeInternalIPs_readsGlobalNotNamespaceDB below.
	db := &mockStorageDB{pinCount: 0}
	global := &mockStorageDB{nodeIPs: nil}
	db.namespaceScoped = true
	h := newHandlersWithDBs(&mockIPFSClient{}, db, global)
	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "partial" {
		t.Errorf("evicted = %q, want partial", got)
	}
	if !db.remainingQueried {
		t.Error("zero-pin path must check references")
	}
	if !global.nodesQueried {
		t.Error("zero-pin path must attempt fan-out against the global topology")
	}
}

// Bugboard #153 root cause. dns_nodes is written ONLY to the main cluster; a
// namespace gateway's own RQLite has the table and never a row. Reading
// topology from the namespace handle therefore returned an empty target set on
// every call, so the fan-out reached nobody, `evicted` was permanently
// "partial", and no block was ever reclaimed — while every unit test passed,
// because the mock answered both roles from one handle.
//
// Mutation check: point activeNodeInternalIPs back at h.db and this fails.
func TestActiveNodeInternalIPs_readsGlobalNotNamespaceDB(t *testing.T) {
	nsDB := &mockStorageDB{namespaceScoped: true, nodeIPs: []string{"10.0.0.99"}}
	global := &mockStorageDB{nodeIPs: []string{"10.0.0.1", "10.0.0.2", "10.0.0.17"}}
	h := newHandlersWithDBs(&mockIPFSClient{}, nsDB, global)

	ips, err := h.activeNodeInternalIPs(context.Background())
	if err != nil {
		t.Fatalf("activeNodeInternalIPs: %v", err)
	}
	if len(ips) != 3 {
		t.Fatalf("got %d node IPs %v, want the 3 from the GLOBAL database", len(ips), ips)
	}
	if nsDB.topologyReadHere {
		t.Error("topology was read from the namespace database, which is empty in production")
	}
	if !global.nodesQueried {
		t.Error("topology must be read from the global database")
	}
}

func TestActiveNodeInternalIPs_noGlobalHandleIsAnError(t *testing.T) {
	// A missing global handle must surface, not read as "cluster has no nodes".
	h := &Handlers{logger: newTestLogger()}
	if _, err := h.activeNodeInternalIPs(context.Background()); err == nil {
		t.Fatal("want an error when no global database handle is configured")
	}
}

func TestMaybeImmediateEvict_evictsUsingGlobalTopology(t *testing.T) {
	// End-to-end of the fixed path: zero remaining pins, topology resolved from
	// the global handle, every node confirms → "true".
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","cid":"QmGone","removed":3}`))
	}))
	defer srv.Close()

	h := newHandlersWithDBs(&mockIPFSClient{},
		&mockStorageDB{namespaceScoped: true, pinCount: 0},
		&mockStorageDB{nodeIPs: []string{"127.0.0.1"}})
	h.evictPort = portOf(t, srv.URL)

	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "true" {
		t.Errorf("evicted = %q, want true", got)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("fan-out hit the node %d times, want 1", hits)
	}
}

// A node that answers 200 while reporting an INCOMPLETE local reclaim must not
// be counted as success: "true" is a promise to the tenant that the bytes are
// gone. Before bugboard #153 the fan-out looked only at the status code, so a
// node that removed some blocks and kept others still produced "true".
func TestMaybeImmediateEvict_nodePartialBodyIsNotTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"partial","cid":"QmGone","removed":1}`))
	}))
	defer srv.Close()

	h := newHandlersWithDBs(&mockIPFSClient{},
		&mockStorageDB{namespaceScoped: true, pinCount: 0},
		&mockStorageDB{nodeIPs: []string{"127.0.0.1"}})
	h.evictPort = portOf(t, srv.URL)

	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "partial" {
		t.Errorf("evicted = %q, want partial (a node kept blocks)", got)
	}
}

// portOf extracts the TCP port a httptest server bound, so the fan-out (which
// dials a fixed internal port in production) can be aimed at it.
func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("port of %q: %v", rawURL, err)
	}
	return p
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
