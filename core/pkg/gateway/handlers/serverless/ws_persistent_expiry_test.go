package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// TestGetJWTClaimsFromRequest verifies the gateway reads the authorizing
// token's claims off the request context at upgrade. They are what the socket
// is registered with and held to for its lifetime (#868); if this silently
// returned nothing for a JWT-authenticated request, the socket would never be
// swept.
func TestGetJWTClaimsFromRequest(t *testing.T) {
	h := newTestHandlers(nil)

	t.Run("JWT on the context is returned", func(t *testing.T) {
		claims := &auth.JWTClaims{Sub: "alice", Exp: 1_700_000_123, Jti: "j1"}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, claims))

		got := h.getJWTClaimsFromRequest(req)
		if got == nil || got.Exp != 1_700_000_123 || got.Jti != "j1" {
			t.Errorf("getJWTClaimsFromRequest = %+v; want the claims on the context", got)
		}
	})

	t.Run("no JWT on context returns nil (API-key / unauthenticated)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := h.getJWTClaimsFromRequest(req); got != nil {
			t.Errorf("getJWTClaimsFromRequest = %+v; want nil", got)
		}
	})

	t.Run("nil claims under key returns nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		var nilClaims *auth.JWTClaims
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, nilClaims))
		if got := h.getJWTClaimsFromRequest(req); got != nil {
			t.Errorf("getJWTClaimsFromRequest = %+v; want nil", got)
		}
	})
}
