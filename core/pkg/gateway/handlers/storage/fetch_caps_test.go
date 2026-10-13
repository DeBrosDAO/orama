package storage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/capability"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// testGate is the capability.Issuer minus its revocation list: the real
// authority, and a set of revoked ids and devices.
type testGate struct {
	authority *capability.Authority
	mu        sync.Mutex
	revoked   map[string]bool
	devices   map[string]bool
	checkErr  error
}

func newTestGate(t *testing.T) *testGate {
	t.Helper()
	a, err := capability.NewAuthority("fetch-cap-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return &testGate{authority: a, revoked: map[string]bool{}, devices: map[string]bool{}}
}

func (g *testGate) MintFetchCaps(_ context.Context, ns, cid, device string, count int, ttl time.Duration) ([]serverless.FetchCap, error) {
	var caps []serverless.FetchCap
	for range count {
		token, c, err := g.authority.MintFetch(ns, cid, device, ttl, time.Now())
		if err != nil {
			return nil, err
		}
		key, err := g.authority.RevokeKey(ns, c.ID)
		if err != nil {
			return nil, err
		}
		caps = append(caps, serverless.FetchCap{ID: c.ID, Token: token, RevokeKey: key, ExpiresAt: c.ExpiresAt})
	}
	return caps, nil
}

func (g *testGate) RevokeFetchCap(_ context.Context, ns, id, revokeKey string) error {
	ok, err := g.authority.VerifyRevokeKey(ns, id, revokeKey)
	if err != nil {
		return err
	}
	if !ok {
		return capability.ErrFetchRevokeKeyInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked[id] = true
	return nil
}

func (g *testGate) CheckFetch(token, ns, cid string) (*capability.FetchClaims, error) {
	if g.checkErr != nil {
		return nil, g.checkErr
	}
	claims, err := g.authority.VerifyFetch(token, ns, cid, time.Now())
	if err != nil {
		return nil, err
	}
	rc, err := g.authority.FetchRevocationClaims(claims)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.revoked[claims.ID] || g.devices[rc.Did] {
		return nil, capability.ErrFetchRevoked
	}
	return claims, nil
}

// countingDB counts the registry reads a request makes.
type countingDB struct {
	ownershipDB
	mu      sync.Mutex
	queries int
}

func (d *countingDB) Query(ctx context.Context, dest any, query string, args ...any) error {
	d.mu.Lock()
	d.queries++
	d.mu.Unlock()
	return d.ownershipDB.Query(ctx, dest, query, args...)
}

const fetchTestNS = "test-ns"

func fetchHandlers(t *testing.T, mock *mockIPFSClient, db *countingDB) (*Handlers, *testGate) {
	t.Helper()
	gate := newTestGate(t)
	h := New(mock, newTestLogger(), Config{ServedNamespace: fetchTestNS}, db, nil)
	h.SetFetchCaps(gate)
	return h, gate
}

func mintTokens(t *testing.T, gate *testGate, cid, device string, n int) []serverless.FetchCap {
	t.Helper()
	caps, err := gate.MintFetchCaps(context.Background(), fetchTestNS, cid, device, n, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return caps
}

func relayedGET(cid, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, RelayedPathPrefix+cid, nil)
	if token != "" {
		req.Header.Set(FetchCapHeader, token)
	}
	return req
}

func serveRelayed(h *Handlers, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.RelayedDownloadHandler(rec, req)
	return rec
}

func wantFetchCapRefusal(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["code"] != code || body["error"] == "" || body["hint"] == "" {
		t.Errorf("body = %v, want {error, code %s, hint}", body, code)
	}
}

func TestRelayedDownload_servesWhatGetServes(t *testing.T) {
	mock := &mockIPFSClient{getReader: sizedBody("file contents"), heldLocally: true}
	h, gate := fetchHandlers(t, mock, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 1)

	rec := serveRelayed(h, relayedGET(testCID, caps[0].Token))
	if rec.Code != http.StatusOK || rec.Body.String() != "file contents" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/octet-stream" ||
		rec.Header().Get("Content-Disposition") != "attachment; filename="+testCID ||
		rec.Header().Get("Content-Length") != "13" {
		t.Errorf("headers = %v", rec.Header())
	}
}

func TestRelayedDownload_refusalsAreOneCodeAndTouchNothing(t *testing.T) {
	mock := &mockIPFSClient{getReader: sizedBody("x"), heldLocally: true}
	db := &countingDB{ownershipDB: ownershipDB{owned: true}}
	h, gate := fetchHandlers(t, mock, db)
	good := mintTokens(t, gate, testCID, "dev-1", 1)[0].Token
	otherCID := "bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy"
	forOther := mintTokens(t, gate, otherCID, "dev-1", 1)[0].Token
	foreign, _ := capability.NewAuthority("another-cluster")
	foreignToken, _, _ := foreign.MintFetch(fetchTestNS, testCID, "dev-1", 2*time.Hour, time.Now())
	wsToken, _, _ := gate.authority.Mint(fetchTestNS, "fn", testCID, "dev-1", 2*time.Hour, time.Now())
	elsewhere, _, _ := gate.authority.MintFetch("other-ns", testCID, "dev-1", 2*time.Hour, time.Now())
	expired, _, _ := gate.authority.MintFetch(fetchTestNS, testCID, "dev-1", capability.FetchMinTTL, time.Now().Add(-2*time.Hour))

	for name, token := range map[string]string{
		"forged":                 good[:len(good)-4] + "AAAA",
		"garbage":                "not-a-token",
		"another CID":            forOther,
		"another cluster":        foreignToken,
		"a WebSocket capability": wsToken,
		"another namespace":      elsewhere,
		"expired":                expired,
	} {
		rec := serveRelayed(h, relayedGET(testCID, token))
		t.Run(name, func(t *testing.T) { wantFetchCapRefusal(t, rec, http.StatusForbidden, CodeFetchCapInvalid) })
	}
	if db.queries != 0 || mock.pinsetCalls != 0 {
		t.Errorf("a refused token reached the registry (%d queries) or IPFS (%d calls)", db.queries, mock.pinsetCalls)
	}
}

func TestRelayedDownload_missingHeaderRevokedAndNotAlone(t *testing.T) {
	mock := &mockIPFSClient{getReader: sizedBody("x"), heldLocally: true}
	db := &countingDB{ownershipDB: ownershipDB{owned: true}}
	h, gate := fetchHandlers(t, mock, db)
	caps := mintTokens(t, gate, testCID, "dev-1", 3)

	wantFetchCapRefusal(t, serveRelayed(h, relayedGET(testCID, "")), http.StatusUnauthorized, CodeFetchCapMissing)

	_ = gate.RevokeFetchCap(context.Background(), fetchTestNS, caps[0].ID, caps[0].RevokeKey)
	wantFetchCapRefusal(t, serveRelayed(h, relayedGET(testCID, caps[0].Token)), http.StatusForbidden, CodeFetchCapRevoked)

	gate.devices["dev-1"] = true
	wantFetchCapRefusal(t, serveRelayed(h, relayedGET(testCID, caps[1].Token)), http.StatusForbidden, CodeFetchCapRevoked)

	gate.devices["dev-1"] = false
	withAuth := relayedGET(testCID, caps[2].Token)
	withAuth.Header.Set("Authorization", "Bearer abc")
	wantFetchCapRefusal(t, serveRelayed(h, withAuth), http.StatusBadRequest, CodeFetchCapNotAlone)
	withKey := relayedGET(testCID, caps[2].Token)
	withKey.Header.Set("X-API-Key", "k")
	wantFetchCapRefusal(t, serveRelayed(h, withKey), http.StatusBadRequest, CodeFetchCapNotAlone)
	withSession := relayedGET(testCID, caps[2].Token)
	withSession = withSession.WithContext(context.WithValue(withSession.Context(), ctxkeys.JWT, &gwauth.JWTClaims{Sub: "0xabc"}))
	wantFetchCapRefusal(t, serveRelayed(h, withSession), http.StatusBadRequest, CodeFetchCapNotAlone)

	if rec := serveRelayed(h, relayedGET(testCID, caps[2].Token)); rec.Code != http.StatusOK {
		t.Errorf("a good capability beside revoked ones: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRelayedDownload_gatewayStatesWithoutKeyOrNamespace(t *testing.T) {
	mock := &mockIPFSClient{heldLocally: true}
	h, gate := fetchHandlers(t, mock, &countingDB{ownershipDB: ownershipDB{owned: true}})
	token := mintTokens(t, gate, testCID, "dev-1", 1)[0].Token

	noGate := New(mock, newTestLogger(), Config{ServedNamespace: fetchTestNS}, nil, nil)
	wantFetchCapRefusal(t, serveRelayed(noGate, relayedGET(testCID, token)), http.StatusServiceUnavailable, CodeFetchCapUnavailable)

	index := New(mock, newTestLogger(), Config{}, nil, nil)
	index.SetFetchCaps(gate)
	wantFetchCapRefusal(t, serveRelayed(index, relayedGET(testCID, token)), http.StatusForbidden, CodeFetchCapInvalid)

	gate.checkErr = errors.New("revocation list unreadable")
	wantFetchCapRefusal(t, serveRelayed(h, relayedGET(testCID, token)), http.StatusServiceUnavailable, CodeFetchCapUnavailable)
}

func TestRelayedDownload_aNonCanonicalCIDIsAValidationError(t *testing.T) {
	h, gate := fetchHandlers(t, &mockIPFSClient{}, &countingDB{})
	token := mintTokens(t, gate, testCID, "dev-1", 1)[0].Token
	for _, cid := range []string{"", "not-a-cid", testCID + "%00"} {
		if rec := serveRelayed(h, relayedGET(cid, token)); rec.Code != http.StatusBadRequest {
			t.Errorf("cid %q: status %d", cid, rec.Code)
		}
	}
}

func TestRelayedDownload_aCapabilityOfAnUnownedCIDOpensNothing(t *testing.T) {
	mock := &mockIPFSClient{heldLocally: true}
	h, gate := fetchHandlers(t, mock, &countingDB{ownershipDB: ownershipDB{owned: false}})
	token := mintTokens(t, gate, testCID, "dev-1", 1)[0].Token
	if rec := serveRelayed(h, relayedGET(testCID, token)); rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
}

func TestRelayedDownload_boundsDownloadsPerCapability(t *testing.T) {
	c := newFetchCounter()
	for range maxConcurrentPerFetchCap {
		if !c.acquire("id") {
			t.Fatal("refused below the bound")
		}
	}
	if c.acquire("id") {
		t.Error("accepted past the bound")
	}
	if !c.acquire("other") {
		t.Error("one capability's downloads counted against another")
	}
	c.release("id")
	if !c.acquire("id") {
		t.Error("a released slot was not reusable")
	}
}

func mintRequest(body string, device string) *http.Request {
	req := withNamespace(httptest.NewRequest(http.MethodPost, FetchCapsPath, strings.NewReader(body)), fetchTestNS)
	if device != "" {
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, &gwauth.JWTClaims{Sub: "0xabc", Did: device}))
	}
	return req
}

func TestFetchCapsMint_returnsDistinctVerifiableTokens(t *testing.T) {
	h, gate := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	rec := httptest.NewRecorder()
	h.FetchCapsHandler(rec, mintRequest(`{"cid":"`+testCID+`","count":3,"ttl_seconds":7200}`, "dev-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	caps, _ := body["caps"].([]any)
	if body["namespace"] != fetchTestNS || body["cid"] != testCID || len(caps) != 3 {
		t.Fatalf("body = %v", body)
	}
	ids := map[any]bool{}
	for _, c := range caps {
		m := c.(map[string]any)
		ids[m["id"]] = true
		if _, err := gate.CheckFetch(m["token"].(string), fetchTestNS, testCID); err != nil {
			t.Errorf("a minted token does not check: %v", err)
		}
		if exp, ok := m["expires_at"].(float64); !ok || int64(exp) < time.Now().Add(time.Hour).Unix() {
			t.Errorf("expires_at = %v, want Unix seconds ~2h ahead", m["expires_at"])
		}
	}
	if len(ids) != 3 {
		t.Errorf("ids not distinct: %v", ids)
	}
}

func TestFetchCapsMint_refusals(t *testing.T) {
	h, _ := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	valid := `"cid":"` + testCID + `"`
	for name, tc := range map[string]struct {
		body, device string
		status       int
		code         string
	}{
		"no device":     {`{` + valid + `,"count":1,"ttl_seconds":3600}`, "", 403, CodeFetchCapDeviceRequired},
		"count zero":    {`{` + valid + `,"count":0,"ttl_seconds":3600}`, "d", 400, ""},
		"count 65":      {`{` + valid + `,"count":65,"ttl_seconds":3600}`, "d", 400, ""},
		"ttl short":     {`{` + valid + `,"count":1,"ttl_seconds":3599}`, "d", 400, ""},
		"ttl long":      {`{` + valid + `,"count":1,"ttl_seconds":604801}`, "d", 400, ""},
		"bad cid":       {`{"cid":"nope","count":1,"ttl_seconds":3600}`, "d", 400, ""},
		"unknown field": {`{` + valid + `,"count":1,"ttl_seconds":3600,"x":1}`, "d", 400, ""},
		"not json":      {`cid`, "d", 400, ""},
		"empty body":    {``, "d", 400, ""},
	} {
		rec := httptest.NewRecorder()
		h.FetchCapsHandler(rec, mintRequest(tc.body, tc.device))
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, tc.status, rec.Body.String())
			continue
		}
		if tc.code != "" {
			wantFetchCapRefusal(t, rec, tc.status, tc.code)
		}
	}
}

func TestFetchCapsMint_unownedCIDAndNoGate(t *testing.T) {
	h, _ := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: false}})
	rec := httptest.NewRecorder()
	h.FetchCapsHandler(rec, mintRequest(`{"cid":"`+testCID+`","count":1,"ttl_seconds":3600}`, "d"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("unowned: status %d", rec.Code)
	}
	bare := New(&mockIPFSClient{}, newTestLogger(), Config{}, nil, nil)
	rec = httptest.NewRecorder()
	bare.FetchCapsHandler(rec, mintRequest(`{}`, "d"))
	wantFetchCapRefusal(t, rec, http.StatusServiceUnavailable, CodeFetchCapUnavailable)
}

func revokeRequest(id, revokeKey string) *http.Request {
	req := withNamespace(httptest.NewRequest(http.MethodDelete, FetchCapsPath+"/"+id, nil), fetchTestNS)
	if revokeKey != "" {
		req.Header.Set(RevokeKeyHeader, revokeKey)
	}
	return req
}

func TestFetchCapsMint_returnsARevokeKeyPerCapability(t *testing.T) {
	h, _ := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	rec := httptest.NewRecorder()
	h.FetchCapsHandler(rec, mintRequest(`{"cid":"`+testCID+`","count":2,"ttl_seconds":3600}`, "d"))
	keys := map[any]bool{}
	for _, c := range decodeBody(t, rec)["caps"].([]any) {
		key, _ := c.(map[string]any)["revoke_key"].(string)
		if len(key) != 64 {
			t.Errorf("revoke_key = %q, want 64 hex characters", key)
		}
		keys[key] = true
	}
	if len(keys) != 2 {
		t.Errorf("revoke keys are not distinct: %v", keys)
	}
}

func TestFetchCapsRevoke(t *testing.T) {
	h, gate := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 1)

	rec := httptest.NewRecorder()
	h.FetchCapsHandler(rec, revokeRequest(caps[0].ID, caps[0].RevokeKey))
	if rec.Code != http.StatusOK || decodeBody(t, rec)["revoked"] != caps[0].ID || !gate.revoked[caps[0].ID] {
		t.Errorf("revoke: %d %s", rec.Code, rec.Body.String())
	}

	for _, id := range []string{"short", strings.Repeat("g", 32)} {
		rec := httptest.NewRecorder()
		h.FetchCapsHandler(rec, revokeRequest(id, "k"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id %q: status %d", id, rec.Code)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := httptest.NewRecorder()
		h.FetchCapsHandler(rec, withNamespace(httptest.NewRequest(method, FetchCapsPath, nil), fetchTestNS))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", method, rec.Code)
		}
	}
}

// Revoking by id takes the revoke key the mint returned: an id alone, which any
// caller can invent, writes nothing.
func TestFetchCapsRevoke_requiresTheRevokeKeyOfThatID(t *testing.T) {
	h, gate := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 2)
	invented := strings.Repeat("ab", 16)

	for name, req := range map[string]*http.Request{
		"no key":                revokeRequest(caps[0].ID, ""),
		"another capability's":  revokeRequest(caps[0].ID, caps[1].RevokeKey),
		"garbage":               revokeRequest(caps[0].ID, "not-a-key"),
		"an id nobody was told": revokeRequest(invented, caps[0].RevokeKey),
		"the id as its key":     revokeRequest(caps[0].ID, caps[0].ID),
	} {
		rec := httptest.NewRecorder()
		h.FetchCapsHandler(rec, req)
		t.Run(name, func(t *testing.T) { wantFetchCapRefusal(t, rec, http.StatusForbidden, CodeFetchCapRevokeKeyInvalid) })
	}
	if len(gate.revoked) != 0 {
		t.Errorf("a refused revoke wrote %v", gate.revoked)
	}
}

func TestFetchCapsRevoke_aKeyOfAnotherNamespaceDoesNotWork(t *testing.T) {
	h, gate := fetchHandlers(t, &mockIPFSClient{}, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 1)
	other, err := gate.authority.RevokeKey("other-ns", caps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.FetchCapsHandler(rec, revokeRequest(caps[0].ID, other))
	wantFetchCapRefusal(t, rec, http.StatusForbidden, CodeFetchCapRevokeKeyInvalid)
}
