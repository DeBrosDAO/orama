package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockDatabaseClient implements DatabaseClient with configurable query results.
// queryFn, when set, takes precedence and lets a test inspect the bound args
// (e.g. to return a row only for the HMAC-hashed key, not the raw key).
type mockDatabaseClient struct {
	queryResult *QueryResult
	queryErr    error
	queryFn     func(query string, args ...interface{}) (*QueryResult, error)
}

func (m *mockDatabaseClient) Query(_ context.Context, query string, args ...interface{}) (*QueryResult, error) {
	if m.queryFn != nil {
		return m.queryFn(query, args...)
	}
	return m.queryResult, m.queryErr
}

// mockNetworkClient implements NetworkClient and returns a mockDatabaseClient.
type mockNetworkClient struct {
	db *mockDatabaseClient
}

func (m *mockNetworkClient) Database() DatabaseClient {
	return m.db
}

// mockClusterProvisioner implements ClusterProvisioner as a no-op.
type mockClusterProvisioner struct{}

func (m *mockClusterProvisioner) CheckNamespaceCluster(_ context.Context, _ string) (string, string, bool, error) {
	return "", "", false, nil
}

func (m *mockClusterProvisioner) ProvisionNamespaceCluster(_ context.Context, _ int, _, _ string) (string, string, error) {
	return "", "", nil
}

func (m *mockClusterProvisioner) GetClusterStatusByID(_ context.Context, _ string) (interface{}, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testLogger returns a silent *logging.ColoredLogger suitable for tests.
func testLogger() *logging.ColoredLogger {
	nop := zap.NewNop()
	return &logging.ColoredLogger{Logger: nop}
}

// noopInternalAuth is a no-op internal auth context function.
func noopInternalAuth(ctx context.Context) context.Context { return ctx }

// decodeBody is a test helper that decodes a JSON response body into a map.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewHandlers(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)
	if h == nil {
		t.Fatal("NewHandlers returned nil")
	}
}

func TestSetClusterProvisioner(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)
	// Should not panic.
	h.SetClusterProvisioner(&mockClusterProvisioner{})
}

// --- ChallengeHandler tests -----------------------------------------------

func TestChallengeHandler_MissingWallet(t *testing.T) {
	// authService is nil, but the handler checks it first and returns 503.
	// To reach the wallet validation we need a non-nil authService.
	// Since authsvc.Service is a concrete struct, we create a zero-value one
	// (it will never be reached for this test path).
	// However, the handler checks `h.authService == nil` before everything else.
	// So we must supply a non-nil *authsvc.Service.  We can create one with
	// an empty signing key (NewService returns error for empty PEM only if
	// the PEM is non-empty but unparseable).  An empty PEM is fine.
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}

	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(ChallengeRequest{Wallet: ""})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ChallengeHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "wallet is required" {
		t.Fatalf("expected error 'wallet is required', got %v", m["error"])
	}
}

func TestChallengeHandler_InvalidMethod(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/challenge", nil)
	rec := httptest.NewRecorder()

	h.ChallengeHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "method not allowed" {
		t.Fatalf("expected error 'method not allowed', got %v", m["error"])
	}
}

func TestChallengeHandler_NilAuthService(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(ChallengeRequest{Wallet: "0xABC"})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ChallengeHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

// --- WhoamiHandler tests --------------------------------------------------

func TestWhoamiHandler_NoAuth(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/whoami", nil)
	rec := httptest.NewRecorder()

	h.WhoamiHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	m := decodeBody(t, rec)
	// When no auth context is set, "authenticated" should be false.
	if auth, ok := m["authenticated"].(bool); !ok || auth {
		t.Fatalf("expected authenticated=false, got %v", m["authenticated"])
	}
	if method, ok := m["method"].(string); !ok || method != "api_key" {
		t.Fatalf("expected method='api_key', got %v", m["method"])
	}
	if ns, ok := m["namespace"].(string); !ok || ns != "default" {
		t.Fatalf("expected namespace='default', got %v", m["namespace"])
	}
}

func TestWhoamiHandler_WithAPIKey(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/whoami", nil)
	ctx := req.Context()
	ctx = context.WithValue(ctx, CtxKeyAPIKey, "ak_test123:default")
	ctx = context.WithValue(ctx, CtxKeyNamespaceOverride, "default")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.WhoamiHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	m := decodeBody(t, rec)
	if auth, ok := m["authenticated"].(bool); !ok || !auth {
		t.Fatalf("expected authenticated=true, got %v", m["authenticated"])
	}
	if method, ok := m["method"].(string); !ok || method != "api_key" {
		t.Fatalf("expected method='api_key', got %v", m["method"])
	}
	if key, ok := m["api_key"].(string); !ok || key != "ak_test123:default" {
		t.Fatalf("expected api_key='ak_test123:default', got %v", m["api_key"])
	}
	if ns, ok := m["namespace"].(string); !ok || ns != "default" {
		t.Fatalf("expected namespace='default', got %v", m["namespace"])
	}
}

func TestWhoamiHandler_WithJWT(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	claims := &authsvc.JWTClaims{
		Iss:       "orama-gateway",
		Sub:       "0xWALLET",
		Aud:       "gateway",
		Iat:       1000,
		Nbf:       1000,
		Exp:       9999,
		Namespace: "myns",
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/whoami", nil)
	ctx := context.WithValue(req.Context(), CtxKeyJWT, claims)
	ctx = context.WithValue(ctx, CtxKeyNamespaceOverride, "myns")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.WhoamiHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	m := decodeBody(t, rec)
	if auth, ok := m["authenticated"].(bool); !ok || !auth {
		t.Fatalf("expected authenticated=true, got %v", m["authenticated"])
	}
	if method, ok := m["method"].(string); !ok || method != "jwt" {
		t.Fatalf("expected method='jwt', got %v", m["method"])
	}
	if sub, ok := m["subject"].(string); !ok || sub != "0xWALLET" {
		t.Fatalf("expected subject='0xWALLET', got %v", m["subject"])
	}
	if ns, ok := m["namespace"].(string); !ok || ns != "myns" {
		t.Fatalf("expected namespace='myns', got %v", m["namespace"])
	}
}

// --- LogoutHandler tests --------------------------------------------------

func TestLogoutHandler_MissingRefreshToken(t *testing.T) {
	// The LogoutHandler does NOT validate refresh_token as required the same
	// way RefreshHandler does. Looking at the source, it checks:
	//   if req.All && no JWT subject -> 401
	//   then passes req.RefreshToken to authService.RevokeToken
	// With All=false and empty RefreshToken, RevokeToken returns "nothing to revoke".
	// But before that, authService == nil returns 503.
	//
	// To test the validation path, we need authService != nil, and All=false
	// with empty RefreshToken. The handler will call authService.RevokeToken
	// which returns an error because we have a real service but no DB.
	// However, the key point is that the handler itself doesn't short-circuit
	// on empty token -- that's left to RevokeToken. So we must accept whatever
	// error code the handler returns via the authService error path.
	//
	// Since we can't easily mock authService (it's a concrete struct),
	// we test with nil authService to verify the 503 early return.
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(LogoutRequest{RefreshToken: ""})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.LogoutHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

func TestLogoutHandler_InvalidMethod(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/logout", nil)
	rec := httptest.NewRecorder()

	h.LogoutHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestLogoutHandler_AllTrueNoJWT(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(LogoutRequest{All: true})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.LogoutHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "jwt required for all=true" {
		t.Fatalf("expected error 'jwt required for all=true', got %v", m["error"])
	}
}

// --- RefreshHandler tests -------------------------------------------------

func TestRefreshHandler_MissingRefreshToken(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(RefreshRequest{})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.RefreshHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "refresh_token is required" {
		t.Fatalf("expected error 'refresh_token is required', got %v", m["error"])
	}
}

func TestRefreshHandler_InvalidMethod(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/refresh", nil)
	rec := httptest.NewRecorder()

	h.RefreshHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestRefreshHandler_NilAuthService(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(RefreshRequest{RefreshToken: "some-token"})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.RefreshHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

// Bugboard #125: a non-bad-token failure (here ErrRotationNotConfigured from a
// service with no rqlite client) must surface as a RETRYABLE 503 with a
// Retry-After header — NOT a 401 that would force a locked device into an
// impossible SIWE re-auth mid-call-ring.
func TestRefreshHandler_TransientError_returns503Retryable(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(RefreshRequest{RefreshToken: "some-valid-looking-token"})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.RefreshHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("transient refresh failure must be 503, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("503 refresh response should carry a Retry-After header")
	}
}

// --- APIKeyToJWTHandler tests ---------------------------------------------

func TestAPIKeyToJWTHandler_MissingKey(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "missing API key" {
		t.Fatalf("expected error 'missing API key', got %v", m["error"])
	}
}

func TestAPIKeyToJWTHandler_InvalidMethod(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/token", nil)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestAPIKeyToJWTHandler_NilAuthService(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", "ak_test:default")
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

// jwtCapableService builds a Service that can both hash API keys (HMAC secret
// set, so HashAPIKey produces a value distinct from the raw key) and mint JWTs
// (EdDSA signing key set).
func jwtCapableService(t *testing.T, hmacSecret string) *authsvc.Service {
	t.Helper()
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	if hmacSecret != "" {
		svc.SetAPIKeyHMACSecret(hmacSecret)
	}
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen failed: %v", err)
	}
	svc.SetEdDSAKey(edPriv)
	return svc
}

func nsLookupRow(ns string) *QueryResult {
	return &QueryResult{Count: 1, Rows: []interface{}{[]interface{}{ns}}}
}

// TestAPIKeyToJWTHandler_HashedKeyLookup proves the fix: keys are stored
// HMAC-hashed, so the handler must resolve the namespace by the HASHED key.
// The mock returns a row ONLY for the hashed key — the pre-fix raw-key-only
// lookup would have returned 401 here.
func TestAPIKeyToJWTHandler_HashedKeyLookup(t *testing.T) {
	const rawKey = "ak_live_abc123"
	svc := jwtCapableService(t, "hmac-secret-xyz")
	hashed := svc.HashAPIKey(rawKey)
	if hashed == rawKey {
		t.Fatal("precondition: HashAPIKey must differ from the raw key")
	}

	db := &mockDatabaseClient{queryFn: func(_ string, args ...interface{}) (*QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == hashed {
				return nsLookupRow("vrf708"), nil
			}
		}
		return &QueryResult{Count: 0}, nil
	}}
	h := NewHandlers(testLogger(), svc, &mockNetworkClient{db: db}, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["namespace"] != "vrf708" {
		t.Errorf("expected namespace vrf708, got %v", m["namespace"])
	}
	tok, _ := m["access_token"].(string)
	if strings.Count(tok, ".") != 2 {
		t.Errorf("expected a JWT (2 dots) in access_token, got %q", tok)
	}
}

// TestAPIKeyToJWTHandler_RawKeyFallback covers the rolling-upgrade fallback:
// a legacy row stored under the RAW (unhashed) key still resolves.
func TestAPIKeyToJWTHandler_RawKeyFallback(t *testing.T) {
	const rawKey = "ak_legacy_unhashed"
	svc := jwtCapableService(t, "hmac-secret-xyz")
	hashed := svc.HashAPIKey(rawKey)

	db := &mockDatabaseClient{queryFn: func(_ string, args ...interface{}) (*QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == rawKey && k != hashed {
				return nsLookupRow("vrf708"), nil
			}
		}
		return &QueryResult{Count: 0}, nil
	}}
	h := NewHandlers(testLogger(), svc, &mockNetworkClient{db: db}, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 via raw-key fallback, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestAPIKeyToJWTHandler_UnknownKey verifies an unknown key still 401s after
// both candidate lookups miss.
func TestAPIKeyToJWTHandler_UnknownKey(t *testing.T) {
	svc := jwtCapableService(t, "hmac-secret-xyz")
	db := &mockDatabaseClient{queryFn: func(_ string, _ ...interface{}) (*QueryResult, error) {
		return &QueryResult{Count: 0}, nil
	}}
	h := NewHandlers(testLogger(), svc, &mockNetworkClient{db: db}, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", "ak_does_not_exist")
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown key, got %d", rec.Code)
	}
}

// TestAPIKeyToJWTHandler_PrefersAPIKeyDBOverNetClient proves that once
// SetAPIKeyDB has been wired (as gateway.go does via apiKeyDB(), which
// resolves to the global/core API-key registry), the fallback lookup uses it
// instead of netClient, avoiding a redundant query against the core-bound
// adapter. The netClient mock fails the test if queried at all.
func TestAPIKeyToJWTHandler_PrefersAPIKeyDBOverNetClient(t *testing.T) {
	const rawKey = "ak_live_nsdb"
	svc := jwtCapableService(t, "hmac-secret-xyz")
	hashed := svc.HashAPIKey(rawKey)

	coreDB := &mockDatabaseClient{queryFn: func(_ string, _ ...interface{}) (*QueryResult, error) {
		t.Error("handler must query apiKeyDB, not the core-bound netClient, once SetAPIKeyDB is wired")
		return &QueryResult{Count: 0}, nil
	}}
	nsDB := &mockDatabaseClient{queryFn: func(_ string, args ...interface{}) (*QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == hashed {
				return nsLookupRow("vrf708"), nil
			}
		}
		return &QueryResult{Count: 0}, nil
	}}

	h := NewHandlers(testLogger(), svc, &mockNetworkClient{db: coreDB}, "default", noopInternalAuth)
	h.SetAPIKeyDB(nsDB)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", rawKey)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["namespace"] != "vrf708" {
		t.Errorf("expected namespace vrf708, got %v", m["namespace"])
	}
}

// TestAPIKeyToJWTHandler_TrustsInternalAuthContext is the regression for the
// two-gateway bug (bugboard #147/#148): on a NAMESPACE gateway the api_keys
// table is EMPTY (keys live in the main cluster RQLite only). The main gateway
// validates the key first and forwards the resolved namespace + scopes via the
// trusted X-Internal-Auth-* headers, which the namespace gateway's auth
// middleware places on the request context. The exchange handler must mint the
// JWT from that context, NOT re-query the local DB — the pre-fix self-query
// returned "invalid API key" for EVERY real key on a namespace gateway.
//
// We prove it by wiring a DB whose query FAILS the test if it is ever called.
func TestAPIKeyToJWTHandler_TrustsInternalAuthContext(t *testing.T) {
	svc := jwtCapableService(t, "hmac-secret-xyz")
	db := &mockDatabaseClient{queryFn: func(_ string, _ ...interface{}) (*QueryResult, error) {
		t.Error("handler must NOT query the DB when the namespace is pre-validated on the request context")
		return &QueryResult{Count: 0}, nil
	}}
	h := NewHandlers(testLogger(), svc, &mockNetworkClient{db: db}, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/token", nil)
	req.Header.Set("X-API-Key", "ak_runtime:anchat-test")
	ctx := context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "anchat-test")
	ctx = context.WithValue(ctx, ctxkeys.Scopes, authsvc.ParseScopes("invoke,proxy,push,storage,webrtc"))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.APIKeyToJWTHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from pre-validated context, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["namespace"] != "anchat-test" {
		t.Errorf("expected namespace anchat-test, got %v", m["namespace"])
	}
	// The minted JWT must carry the key's EXACT scopes so the scope gate enforces
	// them on the exchanged token exactly as on the raw key (no escalation).
	tok, _ := m["access_token"].(string)
	claims, err := svc.ParseAndVerifyJWT(tok)
	if err != nil {
		t.Fatalf("minted token failed verification: %v", err)
	}
	if got := claims.Custom["scopes"]; got != "invoke,proxy,push,storage,webrtc" {
		t.Errorf("expected canonical scopes on the JWT, got %q", got)
	}
}

func TestApiKeyLookupCandidates(t *testing.T) {
	// Distinct hash → hashed first, raw fallback.
	got := apiKeyLookupCandidates("raw", "hashed")
	if len(got) != 2 || got[0] != "hashed" || got[1] != "raw" {
		t.Errorf("distinct: expected [hashed raw], got %v", got)
	}
	// No HMAC secret (hashed == raw) → single candidate, no duplicate query.
	got = apiKeyLookupCandidates("raw", "raw")
	if len(got) != 1 || got[0] != "raw" {
		t.Errorf("equal: expected [raw], got %v", got)
	}
}

// --- RegisterHandler tests ------------------------------------------------

func TestRegisterHandler_MissingFields(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	tests := []struct {
		name string
		req  RegisterRequest
	}{
		{"missing wallet", RegisterRequest{Nonce: "n", Signature: "s"}},
		{"missing nonce", RegisterRequest{Wallet: "0x123", Signature: "s"}},
		{"missing signature", RegisterRequest{Wallet: "0x123", Nonce: "n"}},
		{"all empty", RegisterRequest{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.req)
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.RegisterHandler(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
			}

			m := decodeBody(t, rec)
			if errMsg, ok := m["error"].(string); !ok || errMsg != "wallet, nonce and signature are required" {
				t.Fatalf("expected error 'wallet, nonce and signature are required', got %v", m["error"])
			}
		})
	}
}

func TestRegisterHandler_InvalidMethod(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/register", nil)
	rec := httptest.NewRecorder()

	h.RegisterHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestRegisterHandler_NilAuthService(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	body, _ := json.Marshal(RegisterRequest{Wallet: "0x123", Nonce: "n", Signature: "s"})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.RegisterHandler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
}

// --- markNonceUsed (tested indirectly via nil safety) ----------------------

func TestMarkNonceUsed_NilNetClient(t *testing.T) {
	// markNonceUsed is unexported but returns early when h.netClient == nil.
	// We verify it does not panic by constructing a Handlers with nil netClient
	// and invoking it through the struct directly (same-package test).
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)
	// This should not panic.
	h.markNonceUsed(context.Background(), 1, "0xwallet", "nonce123")
}

// --- resolveNamespace (tested indirectly via nil safety) --------------------

func TestResolveNamespace_NilAuthService(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)
	_, err := h.resolveNamespace(context.Background(), "default")
	if err == nil {
		t.Fatal("expected error when authService is nil, got nil")
	}
}

// --- extractAPIKey tests ---------------------------------------------------

func TestExtractAPIKey_XAPIKeyHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "ak_test123:ns")

	got := extractAPIKey(req)
	if got != "ak_test123:ns" {
		t.Fatalf("expected 'ak_test123:ns', got '%s'", got)
	}
}

func TestExtractAPIKey_BearerNonJWT(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ak_mykey")

	got := extractAPIKey(req)
	if got != "ak_mykey" {
		t.Fatalf("expected 'ak_mykey', got '%s'", got)
	}
}

func TestExtractAPIKey_BearerJWTSkipped(t *testing.T) {
	// A JWT-looking token (two dots) should be skipped by extractAPIKey.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer header.payload.signature")

	got := extractAPIKey(req)
	if got != "" {
		t.Fatalf("expected empty string for JWT bearer, got '%s'", got)
	}
}

func TestExtractAPIKey_ApiKeyScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "ApiKey ak_scheme_key")

	got := extractAPIKey(req)
	if got != "ak_scheme_key" {
		t.Fatalf("expected 'ak_scheme_key', got '%s'", got)
	}
}

func TestExtractAPIKey_QueryParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?api_key=ak_query", nil)

	got := extractAPIKey(req)
	if got != "ak_query" {
		t.Fatalf("expected 'ak_query', got '%s'", got)
	}
}

func TestExtractAPIKey_TokenQueryParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?token=ak_tokenval", nil)

	got := extractAPIKey(req)
	if got != "ak_tokenval" {
		t.Fatalf("expected 'ak_tokenval', got '%s'", got)
	}
}

func TestExtractAPIKey_NoKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := extractAPIKey(req)
	if got != "" {
		t.Fatalf("expected empty string, got '%s'", got)
	}
}

func TestExtractAPIKey_AuthorizationNoSchemeNonJWT(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "ak_raw_token")

	got := extractAPIKey(req)
	if got != "ak_raw_token" {
		t.Fatalf("expected 'ak_raw_token', got '%s'", got)
	}
}

func TestExtractAPIKey_AuthorizationNoSchemeJWTSkipped(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "a.b.c")

	got := extractAPIKey(req)
	if got != "" {
		t.Fatalf("expected empty string for JWT-like auth, got '%s'", got)
	}
}

// --- ChallengeHandler invalid JSON ----------------------------------------

func TestChallengeHandler_InvalidJSON(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ChallengeHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	m := decodeBody(t, rec)
	if errMsg, ok := m["error"].(string); !ok || errMsg != "invalid json body" {
		t.Fatalf("expected error 'invalid json body', got %v", m["error"])
	}
}

// --- WhoamiHandler with namespace override --------------------------------

func TestWhoamiHandler_NamespaceOverride(t *testing.T) {
	h := NewHandlers(testLogger(), nil, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/whoami", nil)
	ctx := context.WithValue(req.Context(), CtxKeyNamespaceOverride, "custom-ns")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.WhoamiHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	m := decodeBody(t, rec)
	if ns, ok := m["namespace"].(string); !ok || ns != "custom-ns" {
		t.Fatalf("expected namespace='custom-ns', got %v", m["namespace"])
	}
}

// --- LogoutHandler invalid JSON -------------------------------------------

func TestLogoutHandler_InvalidJSON(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", bytes.NewReader([]byte("bad json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.LogoutHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

// --- RefreshHandler invalid JSON ------------------------------------------

func TestRefreshHandler_InvalidJSON(t *testing.T) {
	svc, err := authsvc.NewService(testLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to create auth service: %v", err)
	}
	h := NewHandlers(testLogger(), svc, nil, "default", noopInternalAuth)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewReader([]byte("bad json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.RefreshHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}
