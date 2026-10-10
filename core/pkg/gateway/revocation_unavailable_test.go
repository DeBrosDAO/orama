package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// unreadableRevocations is a gateway whose registry cannot be read, so the
// revocation list was never loaded and whether a credential was revoked is
// unknown. It also returns a valid token for the gateway and a stored key.
func unreadableRevocations(t *testing.T) (g *Gateway, token, key string) {
	t.Helper()
	g, _, db := keyRegistry(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	g.authService.SetEdDSAKey(priv, "")
	g.logger = newRQLiteTestLogger()
	g.cfg = &Config{}

	token, _, err = g.authService.GenerateJWT("acme", "0xwallet", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	if key, err = gwauth.NewKey(gwauth.KeyTypeService); err != nil {
		t.Fatalf("mint key: %v", err)
	}
	storeKey(t, g, db, key, "admin", time.Now().Add(24*time.Hour))
	if err := db.Close(); err != nil {
		t.Fatalf("close the registry: %v", err)
	}
	return g, token, key
}

func assertUnavailable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want a retryable 503: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a retryable refusal carries no Retry-After")
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Error("a 503 must not challenge for credentials: the credential may be fine")
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != CodeAuthUnavailable {
		t.Errorf("code %q (%v), want %s", body.Code, err, CodeAuthUnavailable)
	}
}

// Before the fix an unknown revocation state was "not revoked" and the request
// was served.
func TestAuthMiddleware_aTokenWhoseRevocationCannotBeCheckedIsRefusedRetryably(t *testing.T) {
	g, token, _ := unreadableRevocations(t)
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	r := httptest.NewRequest(http.MethodGet, "/v1/db/query", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	g.authMiddleware(next).ServeHTTP(rec, r)

	assertUnavailable(t, rec)
	if reached {
		t.Error("the handler ran on a token whose revocation state is unknown")
	}
}

func TestAuthMiddleware_anAPIKeyWhoseRevocationCannotBeCheckedIsRefusedRetryably(t *testing.T) {
	g, _, key := unreadableRevocations(t)
	reached := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	r := httptest.NewRequest(http.MethodGet, "/v1/db/query", nil)
	r.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	g.authMiddleware(next).ServeHTTP(rec, r)

	assertUnavailable(t, rec)
	if reached {
		t.Error("the handler ran on a key whose revocation state is unknown")
	}
}

func TestProxyToNamespaceGateway_aCredentialWhoseRevocationCannotBeCheckedAnswers503(t *testing.T) {
	g, token, key := unreadableRevocations(t)
	for name, set := range map[string]func(*http.Request){
		"bearer":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
		"api key": func(r *http.Request) { r.Header.Set("X-API-Key", key) },
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
			set(r)
			if _, _, _, errMsg := g.validateAuthForNamespaceProxy(r); errMsg != authUnavailableMessage {
				t.Fatalf("errMsg = %q, want the unavailable refusal", errMsg)
			}
			rec := httptest.NewRecorder()
			g.proxyToNamespaceGateway(rec, r, "acme", g.namespaceProxyAuthFor(r))
			assertUnavailable(t, rec)
		})
	}
}
