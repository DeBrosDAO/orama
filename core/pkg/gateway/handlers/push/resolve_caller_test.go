package push

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// Bugboard #548 — a push device must be keyed on the stable identity (rootId)
// when the app provides one, not the wallet credential that authenticated the
// session. resolveCallerUserID prefers the `root_id` custom claim and falls
// back to the JWT subject so single-credential apps keep working.

func reqWithClaims(t *testing.T, claims *authsvc.JWTClaims) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := r.Context()
	if claims != nil {
		ctx = context.WithValue(ctx, ctxkeys.JWT, claims)
	}
	return r.WithContext(ctx)
}

func TestResolveCallerUserID_prefersRootIDClaim(t *testing.T) {
	r := reqWithClaims(t, &authsvc.JWTClaims{
		Sub:    "0xWALLET",
		Custom: map[string]string{rootIDClaim: "root-uuid-123"},
	})
	if got := resolveCallerUserID(r); got != "root-uuid-123" {
		t.Errorf("want rootId from claim, got %q", got)
	}
}

func TestResolveCallerUserID_fallsBackToSubject(t *testing.T) {
	// No custom claim → wallet subject (back-compat for single-credential apps).
	r := reqWithClaims(t, &authsvc.JWTClaims{Sub: "0xWALLET"})
	if got := resolveCallerUserID(r); got != "0xWALLET" {
		t.Errorf("want wallet subject fallback, got %q", got)
	}
}

func TestResolveCallerUserID_emptyRootIDFallsBack(t *testing.T) {
	// An empty root_id must not collapse identity to "" — fall back to subject.
	r := reqWithClaims(t, &authsvc.JWTClaims{
		Sub:    "0xWALLET",
		Custom: map[string]string{rootIDClaim: ""},
	})
	if got := resolveCallerUserID(r); got != "0xWALLET" {
		t.Errorf("want wallet fallback on empty root_id, got %q", got)
	}
}

func TestResolveCallerUserID_noJWTReturnsEmpty(t *testing.T) {
	// API-key-only request (no JWT in context) → empty.
	r := reqWithClaims(t, nil)
	if got := resolveCallerUserID(r); got != "" {
		t.Errorf("want empty for API-key-only request, got %q", got)
	}
}
