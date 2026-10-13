package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// revocableGateway is a gateway whose auth service can sign tokens and record
// their revocation, the way the main gateway in front of a namespace does.
func revocableGateway(t *testing.T) *Gateway {
	t.Helper()
	g, _, _ := keyRegistry(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	g.authService.SetEdDSAKey(priv, "")
	g.logger = newRQLiteTestLogger()
	g.cfg = &Config{}
	return g
}

func revokedToken(t *testing.T, g *Gateway) string {
	t.Helper()
	token, _, err := g.authService.GenerateJWT("acme", "0xwallet", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	claims, err := g.authService.ParseAndVerifyJWT(token)
	if err != nil {
		t.Fatalf("verify a fresh token: %v", err)
	}
	if err := g.authService.RevokeSession(context.Background(), claims); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	return token
}

// A logged-out session's token reached the namespace host and was answered
// AUTH_MISSING, "no credential was presented": the proxy's verifier dropped the
// revocation error and fell through to the API-key check, which found nothing
// (stagenet e2e TestCacheAuth_revokedSessionStops).
func TestValidateAuthForNamespaceProxy_aRevokedTokenIsRefusedAsRevoked(t *testing.T) {
	g := revocableGateway(t)
	token := revokedToken(t, g)

	t.Run("bearer", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		ns, claims, _, errMsg := g.validateAuthForNamespaceProxy(r)
		if errMsg != sessionRevokedMessage || ns != "" || claims != nil {
			t.Errorf("got (%q, %v, %q), want a refusal saying the session was revoked", ns, claims, errMsg)
		}
	})
	t.Run("websocket jwt query", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws?jwt="+token, nil)
		r.Header.Set("Connection", "upgrade")
		r.Header.Set("Upgrade", "websocket")
		_, _, _, errMsg := g.validateAuthForNamespaceProxy(r)
		if errMsg != sessionRevokedMessage {
			t.Errorf("errMsg = %q, want the revoked-session refusal", errMsg)
		}
	})
}

func TestProxyToNamespaceGateway_aRevokedTokenAnswersAuthRevoked(t *testing.T) {
	g := revocableGateway(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
	r.Header.Set("Authorization", "Bearer "+revokedToken(t, g))
	rec := httptest.NewRecorder()

	g.proxyToNamespaceGateway(rec, r, "acme", g.namespaceProxyAuthFor(r))

	assertAuthCode(t, rec, CodeAuthRevoked)
}

func TestProxyToNamespaceGateway_noCredentialStaysAuthMissing(t *testing.T) {
	g := revocableGateway(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
	rec := httptest.NewRecorder()

	g.proxyToNamespaceGateway(rec, r, "acme", g.namespaceProxyAuthFor(r))

	assertAuthCode(t, rec, CodeAuthMissing)
}

func TestProxyToNamespaceGateway_anUnknownTokenStaysInvalid(t *testing.T) {
	g := revocableGateway(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
	r.Header.Set("X-API-Key", "ak_unknown")
	rec := httptest.NewRecorder()

	g.proxyToNamespaceGateway(rec, r, "acme", g.namespaceProxyAuthFor(r))

	assertAuthCode(t, rec, CodeAuthInvalidKey)
}

func assertAuthCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusUnauthorized || body.Code != want {
		t.Errorf("status %d code %q, want 401 %s: %s", rec.Code, body.Code, want, rec.Body.String())
	}
}

// failingRegistry is a cluster registry that cannot answer.
type failingRegistry struct{ rqlite.Client }

func (failingRegistry) Query(context.Context, any, string, ...any) error {
	return errors.New("rqlite: leader not found")
}

func proxyWithRegistry(t *testing.T, registry rqlite.Client) *httptest.ResponseRecorder {
	t.Helper()
	g := &Gateway{logger: newRQLiteTestLogger(), cfg: &Config{}, registry: registry}
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
	rec := httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, r, "acme", namespaceProxyAuth{namespace: "acme"})
	return rec
}

// A registry that could not answer is not a namespace that does not exist: it
// was reported "Namespace gateway not found" (404, final) for a live tenant.
func TestProxyToNamespaceGateway_aFailedLookupIsARetryable503(t *testing.T) {
	rec := proxyWithRegistry(t, failingRegistry{})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Retryable bool `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Error.Retryable {
		t.Errorf("the refusal is not marked retryable: %s", rec.Body.String())
	}
}

func TestProxyToNamespaceGateway_aNamespaceWithNoLiveGatewayIs404(t *testing.T) {
	rec := proxyWithRegistry(t, targetsDB(t))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
