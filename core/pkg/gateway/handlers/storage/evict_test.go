package storage

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// testClusterSecret is the cluster secret every test gateway shares.
const testClusterSecret = "test-cluster-secret-for-evict-macs"

// mockStorageDB is a minimal rqlite.Client for the eviction-path tests. It
// embeds the interface (so unimplemented methods panic if ever hit) and answers
// only the two SELECTs the evict path issues, plus the ownership SELECT and the
// pin-status UPDATE that UnpinHandler runs first.
type mockStorageDB struct {
	rqlite.Client
	refCount      int      // references to the CID across ALL namespaces (ipfs_cid_refs)
	refQueryErr   error    // error from the cluster reference index
	otherPinCount int      // is_pinned=1 rows in OTHER namespaces (bugboard #156)
	otherQueryErr error    // error to return from the cross-namespace check only
	nodeIPs       []string // active node internal IPs
	queryErr      error

	refsQueried  bool
	otherQueried bool
	nodesQueried bool

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
		// CIDInUseByOtherNamespace (bugboard #156), still used by deployments and
		// namespace delete.
		m.otherQueried = true
		if m.otherQueryErr != nil {
			return m.otherQueryErr
		}
		*out = []map[string]interface{}{{"count": float64(m.otherPinCount)}}
	case strings.Contains(query, "ipfs_cid_refs"):
		m.refsQueried = true
		if m.refQueryErr != nil {
			return m.refQueryErr
		}
		*out = []map[string]interface{}{{"count": float64(m.refCount)}}
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
		global.refCount = m.refCount
		global.refQueryErr = m.refQueryErr
	}
	return newHandlersWithDBs(client, db, global)
}

// globalMock is the cluster-registry mock a handler set built by newHandlersWithDB
// reads references and topology from.
func globalMock(h *Handlers) *mockStorageDB { return h.globalDB.(*mockStorageDB) }

func newHandlersWithDBs(client IPFSClient, db, globalDB rqlite.Client) *Handlers {
	return New(client, newTestLogger(), Config{IPFSReplicationFactor: 3, IPFSAPIURL: "http://localhost:5001", ClusterSecret: testClusterSecret}, db, globalDB)
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
			h := newHandlersWithDB(&mockIPFSClient{}, &mockStorageDB{refCount: tc.pin})
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
	db := &mockStorageDB{refCount: 0}
	h := newHandlersWithDB(&mockIPFSClient{}, db)
	if got := h.maybeImmediateEvict(context.Background(), "QmCID", false); got != "skipped" {
		t.Errorf("evicted = %q, want skipped", got)
	}
	if g := globalMock(h); g.refsQueried || g.nodesQueried {
		t.Error("no DB work should happen when immediate is not requested")
	}
}

func TestMaybeImmediateEvict_sharedCIDNotEvicted(t *testing.T) {
	// Another namespace still pins the CID → must NOT fan out an eviction.
	db := &mockStorageDB{refCount: 2}
	h := newHandlersWithDB(&mockIPFSClient{}, db)
	if got := h.maybeImmediateEvict(context.Background(), "QmShared", true); got != "shared" {
		t.Errorf("evicted = %q, want shared", got)
	}
	if !globalMock(h).refsQueried {
		t.Error("expected the cluster-wide reference check to run")
	}
	if globalMock(h).nodesQueried {
		t.Error("shared CID must short-circuit BEFORE fan-out (dns_nodes must not be queried)")
	}
}

func TestMaybeImmediateEvict_zeroPins_noNodes_partial(t *testing.T) {
	// A genuinely empty cluster topology cannot reclaim anything → partial.
	// This is the honest failure signal; the bug it used to hide is covered by
	// TestActiveNodeInternalIPs_readsGlobalNotNamespaceDB below.
	db := &mockStorageDB{refCount: 0}
	global := &mockStorageDB{nodeIPs: nil}
	db.namespaceScoped = true
	h := newHandlersWithDBs(&mockIPFSClient{}, db, global)
	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "partial" {
		t.Errorf("evicted = %q, want partial", got)
	}
	if !global.refsQueried {
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
		&mockStorageDB{namespaceScoped: true, refCount: 0},
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
		&mockStorageDB{namespaceScoped: true, refCount: 0},
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

// evictReq builds the request a peer gateway's fan-out sends for cid: the CID
// in the query string, where the MAC covers it. secret is what the sender
// signs with ("" sends the pre-MAC marker header alone).
func evictReq(t *testing.T, cid, remoteAddr, secret string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, evictPath+"?"+url.Values{"cid": {cid}}.Encode(),
		strings.NewReader(`{"cid":"`+cid+`"}`))
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Orama-Internal-Auth", storageInternalAuthMarker)
	if secret != "" {
		key, err := auth.CoordinationKey(secret)
		if err != nil {
			t.Fatal(err)
		}
		if err := auth.SignCoordination(key, req, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return httptest.NewRecorder(), req
}

func TestEvictHandler_forbiddenWithoutWireGuard(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	rec, req := evictReq(t, "QmX", "203.0.113.9:5000", testClusterSecret) // signed, but a public IP
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// Bug: the marker header plus a WireGuard source address was the whole
// credential, and the marker is a constant in this repository. Any local
// process on a node could evict any namespace's blobs from a peer. e2e:
// TestInternal_evictNeedsMoreThanTheOverlay.
//
// Mutation check: accept the marker again and this fails.
func TestEvictHandler_markerAndWireGuardAloneAreRefused(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmVictim", "10.0.0.7:5000", "") // WG source + marker, no MAC
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if mock.evictCalls != 0 {
		t.Fatal("an unauthenticated request evicted a blob")
	}
}

func TestEvictHandler_wrongSecretRefused(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmVictim", "10.0.0.7:5000", "another-cluster-entirely-secret")
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden || mock.evictCalls != 0 {
		t.Errorf("status = %d, evictions = %d; want 403 and none", rec.Code, mock.evictCalls)
	}
}

// A MAC captured for one CID cannot be replayed onto another: the CID is in the
// query string the MAC covers.
func TestEvictHandler_macDoesNotMoveToAnotherCID(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmMine", "10.0.0.7:5000", testClusterSecret)
	req.URL.RawQuery = url.Values{"cid": {"QmTheirs"}}.Encode()
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden || mock.evictCalls != 0 {
		t.Errorf("status = %d, evictions = %d; want 403 and none", rec.Code, mock.evictCalls)
	}
}

// The body is not authenticated, so it must not choose what is evicted.
func TestEvictHandler_bodyCIDIsIgnored(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmSigned", "10.0.0.7:5000", testClusterSecret)
	req.Body = io.NopCloser(strings.NewReader(`{"cid":"QmInjected"}`))
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(mock.evictedCIDs) != 1 || mock.evictedCIDs[0] != "QmSigned" {
		t.Errorf("evicted %v, want only the signed CID", mock.evictedCIDs)
	}
}

func TestEvictHandler_staleStampRefused(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmX", "10.0.0.7:5000", "")
	key, _ := auth.CoordinationKey(testClusterSecret)
	if err := auth.SignCoordination(key, req, time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden || mock.evictCalls != 0 {
		t.Errorf("status = %d, evictions = %d; want 403 and none (replay window)", rec.Code, mock.evictCalls)
	}
}

// A gateway with no cluster secret cannot authenticate anything, so it refuses
// everything rather than accepting what it cannot check.
func TestEvictHandler_noClusterSecretRefusesAll(t *testing.T) {
	mock := &mockIPFSClient{}
	h := New(mock, newTestLogger(), Config{}, nil, nil)
	rec, req := evictReq(t, "QmX", "10.0.0.7:5000", testClusterSecret)
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusForbidden || mock.evictCalls != 0 {
		t.Errorf("status = %d, evictions = %d; want 403 and none", rec.Code, mock.evictCalls)
	}
}

func TestEvictHandler_wrongMethod(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	req := httptest.NewRequest(http.MethodGet, evictPath, nil)
	req.RemoteAddr = "10.0.0.7:5000"
	rec := httptest.NewRecorder()
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestEvictHandler_missingCID(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	rec, req := evictReq(t, "", "10.0.0.7:5000", testClusterSecret)
	h.EvictHandler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEvictHandler_success(t *testing.T) {
	mock := &mockIPFSClient{evictRemoved: 4}
	h := newTestHandlers(mock)
	rec, req := evictReq(t, "QmGone", "10.0.0.7:5000", testClusterSecret)
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

// The fan-out and the handler agree: what evictBlobEverywhere sends is accepted
// by EvictHandler (verifier sees the peer's address as WireGuard), and a fan-out
// whose secret differs from the receiver's is refused.
func TestEvictFanout_signedRequestIsAcceptedByTheHandler(t *testing.T) {
	for _, tc := range []struct {
		name       string
		receiver   string
		wantStatus string
		wantEvict  int
	}{
		{"same cluster", testClusterSecret, "true", 1},
		{"other cluster", "a-different-cluster-secret", "partial", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recv := &mockIPFSClient{}
			receiver := New(recv, newTestLogger(), Config{ClusterSecret: tc.receiver}, nil, nil)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.RemoteAddr = "10.0.0.7:5000" // the overlay address a peer arrives from
				receiver.EvictHandler(w, r)
			}))
			defer srv.Close()

			h := newHandlersWithDBs(&mockIPFSClient{},
				&mockStorageDB{namespaceScoped: true},
				&mockStorageDB{nodeIPs: []string{"127.0.0.1"}})
			h.evictPort = portOf(t, srv.URL)

			if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != tc.wantStatus {
				t.Errorf("evicted = %q, want %q", got, tc.wantStatus)
			}
			if recv.evictCalls != tc.wantEvict {
				t.Errorf("receiver evicted %d times, want %d", recv.evictCalls, tc.wantEvict)
			}
		})
	}
}

// A sender with no cluster secret must not fire unsigned calls.
func TestEvictFanout_noClusterSecret_sendsNothing(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	h := New(&mockIPFSClient{}, newTestLogger(), Config{}, &mockStorageDB{namespaceScoped: true},
		&mockStorageDB{nodeIPs: []string{"127.0.0.1"}})
	h.evictPort = portOf(t, srv.URL)
	if got := h.maybeImmediateEvict(context.Background(), "QmGone", true); got != "partial" {
		t.Errorf("evicted = %q, want partial", got)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Error("an unsigned evict call was sent")
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
	db := &mockStorageDB{refCount: 1} // another namespace still references it
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
	db := &mockStorageDB{refCount: 0}
	h := newHandlersWithDB(mock, db)
	rec, req := unpinReq("ns-A")
	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if mock.unpinCalls != 1 {
		t.Errorf("last pinner must remove the cluster pin exactly once; unpinCalls=%d", mock.unpinCalls)
	}
	if !globalMock(h).refsQueried {
		t.Error("expected the cluster-wide reference check to run")
	}
}

// If the cross-namespace check errors, fail safe: do NOT remove the shared
// cluster pin, but still return 200 (this namespace is logically unpinned).
func TestUnpinHandler_refcountError_failsSafeLeavingPin(t *testing.T) {
	mock := &mockIPFSClient{}
	db := &mockStorageDB{refQueryErr: errStorageTest}
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
