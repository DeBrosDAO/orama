package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// quotaMockDB is a partial rqlite.Client (bugboard #141 tests): it embeds the
// interface so it satisfies every method, but only Query/Exec are implemented —
// Query returns crafted budget/usage rows keyed on the SQL, Exec is a no-op.
type quotaMockDB struct {
	rqlite.Client
	budget []map[string]interface{}
	usage  []map[string]interface{}
}

func (m *quotaMockDB) Query(_ context.Context, dest any, query string, _ ...any) error {
	out, ok := dest.(*[]map[string]interface{})
	if !ok {
		return nil
	}
	switch {
	case strings.Contains(query, "max_storage_bytes"):
		*out = m.budget
	case strings.Contains(query, "SUM(size_bytes)"):
		*out = m.usage
	}
	return nil
}

func (m *quotaMockDB) Exec(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockIPFSClient implements the IPFSClient interface for testing.
type mockIPFSClient struct {
	addResp    *ipfs.AddResponse
	addErr     error
	pinResp    *ipfs.PinResponse
	pinErr     error
	pinStatus  *ipfs.PinStatus
	pinStatErr error
	getReader  io.ReadCloser
	getErr     error
	// GetStored (bugboard #414): heldLocally serves without the pinset;
	// otherwise notInPinset / pinsetErr decide, and pinsetCalls counts.
	heldLocally bool
	notInPinset bool
	pinsetErr   error
	pinsetCalls int
	// getDeadline records the deadline Get was called with.
	getDeadline time.Time
	unpinErr    error
	unpinCalls  int
	// evict tracking (bugboard #153)
	evictRemoved int
	evictErr     error
	evictCalls   int
	evictedCIDs  []string
}

func (m *mockIPFSClient) Add(_ context.Context, _ io.Reader, _ string) (*ipfs.AddResponse, error) {
	return m.addResp, m.addErr
}

func (m *mockIPFSClient) Pin(_ context.Context, _ string, _ string, _ int) (*ipfs.PinResponse, error) {
	return m.pinResp, m.pinErr
}

func (m *mockIPFSClient) PinStatus(_ context.Context, _ string) (*ipfs.PinStatus, error) {
	return m.pinStatus, m.pinStatErr
}

// GetStored models ipfs.Client.GetStored: a local hit is served without the
// pinset; on a miss the pinset decides between NOT_FOUND and a network fetch.
func (m *mockIPFSClient) GetStored(ctx context.Context, cid, url string) (io.ReadCloser, error) {
	if m.heldLocally {
		return m.Get(ctx, cid, url)
	}
	m.pinsetCalls++
	switch {
	case m.pinsetErr != nil:
		return nil, fmt.Errorf("%w: %v", ipfs.ErrPinsetUnavailable, m.pinsetErr)
	case m.notInPinset:
		return nil, fmt.Errorf("%w: %s", ipfs.ErrNotInPinset, cid)
	}
	return m.Get(ctx, cid, url)
}

func (m *mockIPFSClient) Get(ctx context.Context, _ string, _ string) (io.ReadCloser, error) {
	m.getDeadline, _ = ctx.Deadline()
	return m.getReader, m.getErr
}

func (m *mockIPFSClient) Unpin(_ context.Context, _ string) error {
	m.unpinCalls++
	return m.unpinErr
}

func (m *mockIPFSClient) EvictLocal(_ context.Context, cid string) (int, error) {
	m.evictCalls++
	m.evictedCIDs = append(m.evictedCIDs, cid)
	return m.evictRemoved, m.evictErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestLogger() *logging.ColoredLogger {
	logger, _ := logging.NewColoredLogger(logging.ComponentStorage, false)
	return logger
}

func newTestHandlers(client IPFSClient) *Handlers {
	return New(client, newTestLogger(), Config{
		IPFSReplicationFactor: 3,
		IPFSAPIURL:            "http://localhost:5001",
	}, nil, nil) // db=nil -> ownership checks bypassed
}

// withNamespace returns a request with the namespace context key set.
func withNamespace(r *http.Request, ns string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, ns)
	return r.WithContext(ctx)
}

// decodeBody decodes a JSON response body into a map.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return body
}

// ---------------------------------------------------------------------------
// Tests: getNamespaceFromContext
// ---------------------------------------------------------------------------

func TestGetNamespaceFromContext_Present(t *testing.T) {
	h := newTestHandlers(nil)
	ctx := context.WithValue(context.Background(), ctxkeys.NamespaceOverride, "my-ns")

	got := h.getNamespaceFromContext(ctx)
	if got != "my-ns" {
		t.Errorf("expected 'my-ns', got %q", got)
	}
}

func TestGetNamespaceFromContext_Missing(t *testing.T) {
	h := newTestHandlers(nil)

	got := h.getNamespaceFromContext(context.Background())
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestGetNamespaceFromContext_WrongType(t *testing.T) {
	h := newTestHandlers(nil)
	ctx := context.WithValue(context.Background(), ctxkeys.NamespaceOverride, 12345)

	got := h.getNamespaceFromContext(ctx)
	if got != "" {
		t.Errorf("expected empty string for wrong type, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Tests: UploadHandler
// ---------------------------------------------------------------------------

func TestUploadHandler_NilIPFS(t *testing.T) {
	h := newTestHandlers(nil) // nil IPFS client
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestUploadHandler_InvalidMethod(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/upload", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUploadHandler_MissingNamespace(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	// No namespace in context
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", strings.NewReader(`{"data":"dGVzdA=="}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUploadHandler_InvalidJSON(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestUploadHandler_MissingData(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", strings.NewReader(`{"name":"test.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "data field required") {
		t.Errorf("expected 'data field required' error, got %q", errMsg)
	}
}

func TestUploadHandler_InvalidBase64(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", strings.NewReader(`{"data":"!!!invalid!!!"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "base64") {
		t.Errorf("expected base64 decode error, got %q", errMsg)
	}
}

func TestUploadHandler_PUTNotAllowed(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPut, "/v1/storage/upload", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUploadHandler_Success(t *testing.T) {
	mock := &mockIPFSClient{
		addResp: &ipfs.AddResponse{
			Cid:  "QmTestCID1234567890123456789012345678901234",
			Name: "test.txt",
			Size: 4,
		},
		pinResp: &ipfs.PinResponse{
			Cid:  "QmTestCID1234567890123456789012345678901234",
			Name: "test.txt",
		},
	}
	h := newTestHandlers(mock)

	// "dGVzdA==" is base64("test")
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", strings.NewReader(`{"data":"dGVzdA==","name":"test.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	body := decodeBody(t, rec)
	if body["cid"] != "QmTestCID1234567890123456789012345678901234" {
		t.Errorf("unexpected cid: %v", body["cid"])
	}
}

// ---------------------------------------------------------------------------
// Tests: DownloadHandler
// ---------------------------------------------------------------------------

func TestDownloadHandler_NilIPFS(t *testing.T) {
	h := newTestHandlers(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/QmSomeCID", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.DownloadHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestDownloadHandler_InvalidMethod(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/get/QmSomeCID", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.DownloadHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestDownloadHandler_MissingCID(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.DownloadHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	detail, _ := body["error"].(map[string]any)
	if msg, _ := detail["message"].(string); !strings.Contains(msg, "cid required") {
		t.Errorf("expected 'cid required' error, got %v", body)
	}
}

func TestDownloadHandler_MissingNamespace(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	// No namespace in context
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/QmSomeCID", nil)
	rec := httptest.NewRecorder()

	h.DownloadHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestDownloadHandler_Success(t *testing.T) {
	mock := &mockIPFSClient{
		getReader: io.NopCloser(strings.NewReader("file contents")),
	}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+testCID, nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.DownloadHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("expected application/octet-stream, got %q", ct)
	}
	if rec.Body.String() != "file contents" {
		t.Errorf("expected 'file contents', got %q", rec.Body.String())
	}
}

// testCID is a real CIDv0 (the one bugboard #414 was reported against).
const testCID = "QmfYzxZHqYpmy29rVWqs6f4igzYngACaxSxPWdf7FspuDV"

// download runs the handler for testCID and decodes the RPC error, if any.
func download(t *testing.T, mock *mockIPFSClient) (int, map[string]any) {
	t.Helper()
	h := newTestHandlers(mock)
	req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+testCID, nil), "test-ns")
	rec := httptest.NewRecorder()
	h.DownloadHandler(rec, req)
	if rec.Code == http.StatusOK {
		return rec.Code, nil
	}
	body := decodeBody(t, rec)
	detail, _ := body["error"].(map[string]any)
	return rec.Code, detail
}

// bugboard #414: a client must be able to tell content that is gone from
// content that is slow, and a gone object must not cost the full proxy budget.
func TestDownloadHandler_notInPinsetIsAFinalNotFound(t *testing.T) {
	mock := &mockIPFSClient{notInPinset: true, getErr: errors.New("Get must not be called")}
	status, detail := download(t, mock)
	if status != http.StatusNotFound || detail["code"] != "NOT_FOUND" || detail["retryable"] != false {
		t.Fatalf("status = %d, error = %v; want 404 NOT_FOUND, not retryable", status, detail)
	}
}

func TestDownloadHandler_slowFetchIsARetryableTimeout(t *testing.T) {
	mock := &mockIPFSClient{getErr: fmt.Errorf("cat: %w", context.DeadlineExceeded)}
	status, detail := download(t, mock)
	if status != http.StatusGatewayTimeout || detail["code"] != "TIMEOUT" || detail["retryable"] != true {
		t.Fatalf("status = %d, error = %v; want 504 TIMEOUT, retryable", status, detail)
	}
	if msg, _ := detail["message"].(string); strings.Contains(msg, "function.yaml") {
		t.Errorf("a storage timeout points at function.yaml: %q", msg)
	}
}

func TestDownloadHandler_clusterUnreachableIsRetryable(t *testing.T) {
	mock := &mockIPFSClient{pinsetErr: errors.New("dial tcp 127.0.0.1:9094: connect: connection refused")}
	status, detail := download(t, mock)
	if status != http.StatusServiceUnavailable || detail["code"] != "SERVICE_UNAVAILABLE" || detail["retryable"] != true {
		t.Fatalf("status = %d, error = %v; want 503 SERVICE_UNAVAILABLE, retryable", status, detail)
	}
}

func TestDownloadHandler_fetchIsBoundedBelowTheProxyBudget(t *testing.T) {
	mock := &mockIPFSClient{getReader: io.NopCloser(strings.NewReader("x"))}
	start := time.Now()
	if status, _ := download(t, mock); status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if mock.getDeadline.IsZero() || mock.getDeadline.After(start.Add(storageFetchTimeout+time.Second)) {
		t.Errorf("Get deadline = %v, want one within %s", mock.getDeadline, storageFetchTimeout)
	}
}

func TestDownloadHandler_heldLocallyIsServedWithoutThePinset(t *testing.T) {
	// A node that holds the content serves it even when the cluster peer is
	// down, and whether or not it was ever pinned.
	mock := &mockIPFSClient{heldLocally: true, pinsetErr: errors.New("cluster down"),
		getReader: io.NopCloser(strings.NewReader("local"))}
	if status, _ := download(t, mock); status != http.StatusOK || mock.pinsetCalls != 0 {
		t.Fatalf("status = %d, pinset asked %d times; want 200 with no pinset lookup", status, mock.pinsetCalls)
	}
}

func TestDownloadHandler_aFailedReadDoesNotLeakInternals(t *testing.T) {
	mock := &mockIPFSClient{getErr: errors.New("get X from http://127.0.0.1:4501 failed with status 500: kubo internals")}
	status, detail := download(t, mock)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if msg, _ := detail["message"].(string); strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "kubo internals") {
		t.Errorf("the response carries the internal error: %q", msg)
	}
}

// ownershipDB answers the ownership and upload-recency queries.
type ownershipDB struct {
	rqlite.Client
	owned, recent bool
}

func (d *ownershipDB) Query(_ context.Context, dest any, query string, args ...any) error {
	out := dest.(*[]map[string]interface{})
	hit := d.owned
	if strings.Contains(query, "pin_requested_at, uploaded_at) >=") {
		if len(args) != 3 || args[2] != "-120 seconds" {
			return fmt.Errorf("pin-request window queried with %v, want the 2-minute window", args)
		}
		hit = d.recent
	}
	n := int64(0)
	if hit {
		n = 1
	}
	*out = []map[string]interface{}{{"count": n}}
	return nil
}

// Authorization comes before anything asks IPFS about the CID, so the
// responses cannot reveal whether the cluster holds a CID the caller does not
// own.
func TestDownloadHandler_unownedIsRefusedBeforeThePinsetIsAsked(t *testing.T) {
	mock := &mockIPFSClient{}
	h := New(mock, newTestLogger(), Config{}, &ownershipDB{owned: false}, nil)
	req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+testCID, nil), "test-ns")
	rec := httptest.NewRecorder()
	h.DownloadHandler(rec, req)
	if rec.Code != http.StatusForbidden || mock.pinsetCalls != 0 {
		t.Fatalf("status = %d, pinset asked %d times; want 403 and no pinset lookup", rec.Code, mock.pinsetCalls)
	}
}

// A CID missing from this node's pinset moments after its upload is a pin
// still propagating: NOT_FOUND, but retryable.
func TestDownloadHandler_aFreshUploadNotYetVisibleIsRetryable(t *testing.T) {
	for _, tc := range []struct {
		recent        bool
		wantRetryable bool
	}{{true, true}, {false, false}} {
		mock := &mockIPFSClient{notInPinset: true}
		h := New(mock, newTestLogger(), Config{}, &ownershipDB{owned: true, recent: tc.recent}, nil)
		req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+testCID, nil), "test-ns")
		rec := httptest.NewRecorder()
		h.DownloadHandler(rec, req)
		body := decodeBody(t, rec)
		detail, _ := body["error"].(map[string]any)
		if rec.Code != http.StatusNotFound || detail["retryable"] != tc.wantRetryable {
			t.Errorf("recent=%v: status = %d, error = %v; want 404 retryable=%v", tc.recent, rec.Code, detail, tc.wantRetryable)
		}
	}
}

func TestDownloadHandler_invalidCIDIsRefused(t *testing.T) {
	// Garbage, and a valid CID in a non-canonical spelling (upper-case
	// base32): only the form an upload records is passed on to IPFS.
	for _, cid := range []string{"not-a-cid", "BAFKREIHDWDCEFGH4DQKJV67UZCMW7OJEE6XEDZDETOJUZJEVTENXQUVYKU"} {
		h := newTestHandlers(&mockIPFSClient{})
		req := withNamespace(httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+cid, nil), "test-ns")
		rec := httptest.NewRecorder()
		h.DownloadHandler(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", cid, rec.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: StatusHandler
// ---------------------------------------------------------------------------

func TestStatusHandler_NilIPFS(t *testing.T) {
	h := newTestHandlers(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/QmSomeCID", nil)
	rec := httptest.NewRecorder()

	h.StatusHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestStatusHandler_InvalidMethod(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/status/QmSomeCID", nil)
	rec := httptest.NewRecorder()

	h.StatusHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestStatusHandler_MissingCID(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/", nil)
	rec := httptest.NewRecorder()

	h.StatusHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "cid required") {
		t.Errorf("expected 'cid required' error, got %q", errMsg)
	}
}

func TestStatusHandler_Success(t *testing.T) {
	mock := &mockIPFSClient{
		pinStatus: &ipfs.PinStatus{
			Cid:    "QmTestCID",
			Name:   "test.txt",
			Status: "pinned",
			Peers:  []string{"peer1", "peer2"},
		},
	}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/status/QmTestCID", nil)
	rec := httptest.NewRecorder()

	h.StatusHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["cid"] != "QmTestCID" {
		t.Errorf("expected cid='QmTestCID', got %v", body["cid"])
	}
	if body["status"] != "pinned" {
		t.Errorf("expected status='pinned', got %v", body["status"])
	}
}

// ---------------------------------------------------------------------------
// Tests: PinHandler
// ---------------------------------------------------------------------------

func TestPinHandler_NilIPFS(t *testing.T) {
	h := newTestHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"cid":"QmTest"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestPinHandler_InvalidMethod(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/pin", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestPinHandler_InvalidJSON(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader("bad json"))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestPinHandler_MissingCID(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "cid required") {
		t.Errorf("expected 'cid required' error, got %q", errMsg)
	}
}

func TestPinHandler_MissingNamespace(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	// No namespace in context
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"cid":"QmTest"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestPinHandler_Success(t *testing.T) {
	mock := &mockIPFSClient{
		pinResp: &ipfs.PinResponse{
			Cid:  "QmTestCID",
			Name: "test.txt",
		},
	}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/pin", strings.NewReader(`{"cid":"QmTestCID","name":"test.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.PinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["cid"] != "QmTestCID" {
		t.Errorf("expected cid='QmTestCID', got %v", body["cid"])
	}
}

// ---------------------------------------------------------------------------
// Tests: UnpinHandler
// ---------------------------------------------------------------------------

func TestUnpinHandler_NilIPFS(t *testing.T) {
	h := newTestHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/QmTest", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestUnpinHandler_InvalidMethod(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodGet, "/v1/storage/unpin/QmTest", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUnpinHandler_MissingCID(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := decodeBody(t, rec)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "cid required") {
		t.Errorf("expected 'cid required' error, got %q", errMsg)
	}
}

func TestUnpinHandler_MissingNamespace(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	// No namespace in context
	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/QmTest", nil)
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestUnpinHandler_POSTNotAllowed(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodPost, "/v1/storage/unpin/QmTest", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestUnpinHandler_Success(t *testing.T) {
	mock := &mockIPFSClient{}
	h := newTestHandlers(mock)

	req := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/QmTestCID", nil)
	req = withNamespace(req, "test-ns")
	rec := httptest.NewRecorder()

	h.UnpinHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["status"] != "ok" {
		t.Errorf("expected status='ok', got %v", body["status"])
	}
	if body["cid"] != "QmTestCID" {
		t.Errorf("expected cid='QmTestCID', got %v", body["cid"])
	}
}

// ---------------------------------------------------------------------------
// Tests: base64Decode helper
// ---------------------------------------------------------------------------

func TestBase64Decode_Valid(t *testing.T) {
	// "dGVzdA==" is base64("test")
	data, err := base64Decode("dGVzdA==")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "test" {
		t.Errorf("expected 'test', got %q", string(data))
	}
}

func TestBase64Decode_Invalid(t *testing.T) {
	_, err := base64Decode("!!!not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64, got nil")
	}
}

func TestBase64Decode_Empty(t *testing.T) {
	data, err := base64Decode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty slice, got %d bytes", len(data))
	}
}

// ---------------------------------------------------------------------------
// Tests: recordCIDOwnership / checkCIDOwnership / updatePinStatus with nil DB
// ---------------------------------------------------------------------------

func TestRecordCIDOwnership_NilDB(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	err := h.recordCIDOwnership(context.Background(), "cid", "ns", "name", "uploader", 100)
	if err != nil {
		t.Errorf("expected nil error with nil db, got %v", err)
	}
}

func TestCheckCIDOwnership_NilDB(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	hasAccess, err := h.checkCIDOwnership(context.Background(), "cid", "ns")
	if err != nil {
		t.Errorf("expected nil error with nil db, got %v", err)
	}
	if !hasAccess {
		t.Error("expected true (allow access) when db is nil")
	}
}

func TestUpdatePinStatus_NilDB(t *testing.T) {
	h := newTestHandlers(&mockIPFSClient{})
	err := h.updatePinStatus(context.Background(), "cid", "ns", true)
	if err != nil {
		t.Errorf("expected nil error with nil db, got %v", err)
	}
}

func TestIsAlreadyUnpinned(t *testing.T) {
	// Nil error = the CID was unpinned = success.
	if !isAlreadyUnpinned(nil) {
		t.Error("nil error should be treated as already-unpinned success")
	}
	// IPFS-Cluster "already gone" shapes → idempotent success (bugboard #140).
	for _, msg := range []string{
		"unpin failed with status 404: cid not part of the pinset",
		"CID is NOT PINNED on this cluster",
		"the pin is not part of the pinset for peer abc",
	} {
		if !isAlreadyUnpinned(errString(msg)) {
			t.Errorf("expected idempotent success for %q", msg)
		}
	}
	// Real failures AND ambiguous 404 / "not found" shapes that are NOT the
	// definitive pinset phrase must NOT be swallowed (would orphan a blob).
	for _, msg := range []string{
		"pin request failed: connection refused",
		"unpin failed with status 500: internal error",
		"namespace not found",
		"route not found",
		"unpin failed with status 404: gateway not found",
	} {
		if isAlreadyUnpinned(errString(msg)) {
			t.Errorf("ambiguous/real failure %q must not be treated as success", msg)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestStorageQuotaExceeded(t *testing.T) {
	mk := func(budget, usage int64, hasBudgetRow bool) *Handlers {
		db := &quotaMockDB{usage: []map[string]interface{}{{"used": usage}}}
		if hasBudgetRow {
			db.budget = []map[string]interface{}{{"max_storage_bytes": budget}}
		}
		return New(&mockIPFSClient{}, newTestLogger(), Config{IPFSReplicationFactor: 3}, db, db)
	}
	ctx := context.Background()

	// No budget row → unlimited (opt-in enforcement).
	if ex, _, _, _ := mk(0, 74_000_000, false).storageQuotaExceeded(ctx, "ns", 1_000_000); ex {
		t.Error("no budget row must be unlimited")
	}
	// Within budget: (74MB + 1MB) × 3 = 225MB < 300MB.
	if ex, _, _, _ := mk(300_000_000, 74_000_000, true).storageQuotaExceeded(ctx, "ns", 1_000_000); ex {
		t.Error("within budget must not be exceeded")
	}
	// Over budget: (74MB + 1MB) × 3 = 225MB > 100MB.
	if ex, budget, projected, _ := mk(100_000_000, 74_000_000, true).storageQuotaExceeded(ctx, "ns", 1_000_000); !ex {
		t.Errorf("over budget must be exceeded (budget=%d projected=%d)", budget, projected)
	}
	// Zero / non-positive budget → unlimited.
	if ex, _, _, _ := mk(0, 74_000_000, true).storageQuotaExceeded(ctx, "ns", 1_000_000); ex {
		t.Error("zero budget must be unlimited")
	}
	// nil db (test mode) → never enforced.
	if ex, _, _, _ := newTestHandlers(&mockIPFSClient{}).storageQuotaExceeded(ctx, "ns", 1<<40); ex {
		t.Error("nil db must skip enforcement")
	}
}

func TestUploadHandler_QuotaExceeded(t *testing.T) {
	// Budget 10 bytes; a 5-byte upload → (0+5)×RF3 = 15 > 10 → rejected before Add.
	db := &quotaMockDB{
		budget: []map[string]interface{}{{"max_storage_bytes": int64(10)}},
		usage:  []map[string]interface{}{{"used": int64(0)}},
	}
	h := New(&mockIPFSClient{addResp: &ipfs.AddResponse{Cid: "QmShouldNotBeAdded", Size: 5}},
		newTestLogger(), Config{IPFSReplicationFactor: 3}, db, db)

	// "aGVsbG8=" is base64("hello") = 5 bytes.
	req := httptest.NewRequest(http.MethodPost, "/v1/storage/upload",
		strings.NewReader(`{"data":"aGVsbG8=","name":"x.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withNamespace(req, "ns")
	rec := httptest.NewRecorder()

	h.UploadHandler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	errObj, _ := body["error"].(map[string]interface{})
	if errObj == nil || errObj["code"] != "STORAGE_QUOTA_EXCEEDED" {
		t.Errorf("expected STORAGE_QUOTA_EXCEEDED envelope, got %v", body)
	}
}
