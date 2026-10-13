package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// expiredToken is a token this gateway signed whose lifetime ran out well past
// the clock-skew allowance.
func expiredToken(t *testing.T, g *Gateway) string {
	t.Helper()
	token, _, err := g.authService.GenerateJWT("acme", "0xwallet", -10*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}
	return token
}

// An expired access token was answered AUTH_MISSING, "no credential was
// presented": the verifier's error fell through to the API-key check, which
// found nothing. The SDK and CLI could not tell a token to refresh from a
// credential never sent.
func TestValidateAuthForNamespaceProxy_anExpiredTokenIsRefusedAsExpired(t *testing.T) {
	g := revocableGateway(t)
	token := expiredToken(t, g)

	t.Run("bearer", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		ns, claims, _, errMsg := g.validateAuthForNamespaceProxy(r)
		if errMsg != tokenExpiredMessage || ns != "" || claims != nil {
			t.Errorf("got (%q, %v, %q), want a refusal saying the token expired", ns, claims, errMsg)
		}
	})
	t.Run("websocket jwt query", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws?jwt="+token, nil)
		r.Header.Set("Connection", "upgrade")
		r.Header.Set("Upgrade", "websocket")
		_, _, _, errMsg := g.validateAuthForNamespaceProxy(r)
		if errMsg != tokenExpiredMessage {
			t.Errorf("errMsg = %q, want the expired-token refusal", errMsg)
		}
	})
}

func TestProxyToNamespaceGateway_anExpiredTokenAnswersAuthExpired(t *testing.T) {
	g := revocableGateway(t)
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
	r.Header.Set("Authorization", "Bearer "+expiredToken(t, g))
	rec := httptest.NewRecorder()

	g.proxyToNamespaceGateway(rec, r, "acme", g.namespaceProxyAuthFor(r))

	assertAuthCode(t, rec, CodeAuthExpired)
}

func TestAuthMiddleware_anExpiredTokenAnswersAuthExpired(t *testing.T) {
	g := revocableGateway(t)
	token := expiredToken(t, g)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an expired token reached the handler")
	})

	t.Run("bearer", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/cache/get", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		g.authMiddleware(next).ServeHTTP(rec, r)
		assertAuthCode(t, rec, CodeAuthExpired)
	})
	t.Run("websocket jwt query", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws?jwt="+token, nil)
		r.Header.Set("Connection", "upgrade")
		r.Header.Set("Upgrade", "websocket")
		rec := httptest.NewRecorder()
		g.authMiddleware(next).ServeHTTP(rec, r)
		assertAuthCode(t, rec, CodeAuthExpired)
	})
}
