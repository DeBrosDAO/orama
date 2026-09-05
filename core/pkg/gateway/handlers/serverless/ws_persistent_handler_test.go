package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// TestBuildPersistentInvocationContext_PropagatesJWTSubject is the regression
// guard for Layer 7 of the WS bug chain (Feature #73 on bugboard).
//
// Symptom: AnChat's persistent rpc-router function called function_invoke into
// a sub-function. Inside the sub-function, oh.JwtSubjectUserID() returned ""
// and the sub-function bailed with AUTH_REQUIRED — even though the WS upgrade
// itself was JWT-authenticated and the calling user was identified.
//
// Root cause: handlePersistentWebSocket built the per-connection
// InvocationContext WITHOUT calling getJWTSubjectFromRequest, so
// CallerJWTSubject was always "". HostFunctions.FunctionInvoke correctly
// propagated cur.CallerJWTSubject — but cur.CallerJWTSubject was empty to
// begin with. The stateless WS handler (ws_handler.go) had always done this
// correctly; the persistent handler diverged silently.
//
// If a future refactor drops the field again, this test fails loud — the
// AnChat sync flow would break end-to-end one more time.
func TestBuildPersistentInvocationContext_PropagatesJWTSubject(t *testing.T) {
	h := newTestHandlers(nil)

	// Simulate a JWT-authenticated request: middleware would have stashed
	// the *auth.JWTClaims on the request context under ctxkeys.JWT.
	claims := &auth.JWTClaims{
		Sub:    "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB",
		Custom: map[string]string{"role": "admin"},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, claims))

	fn := &serverless.Function{
		ID:        "fn-id",
		Name:      "rpc-router",
		Namespace: "anchat",
	}
	clientID := "ws-client-uuid"

	got := h.buildPersistentInvocationContext(req, fn, clientID)

	if got == nil {
		t.Fatal("buildPersistentInvocationContext returned nil")
	}

	// Layer 7 invariant: CallerJWTSubject must be populated. Without this
	// field, every function_invoke from inside a persistent WS instance
	// loses the caller identity — see comment on the helper for the full
	// story.
	if got.CallerJWTSubject != "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB" {
		t.Errorf("CallerJWTSubject = %q; want %q (Layer 7 regression — see Feature #73)",
			got.CallerJWTSubject, "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB")
	}

	// Other identity fields the persistent invCtx is responsible for. These
	// exercise a smaller surface than the full handler but cover the same
	// wiring contract.
	if got.CallerWallet == "" {
		t.Error("CallerWallet should be populated from JWT (got empty)")
	}
	if got.WSClientID != clientID {
		t.Errorf("WSClientID = %q; want %q", got.WSClientID, clientID)
	}
	if got.FunctionID != fn.ID {
		t.Errorf("FunctionID = %q; want %q", got.FunctionID, fn.ID)
	}
	if got.FunctionName != fn.Name {
		t.Errorf("FunctionName = %q; want %q", got.FunctionName, fn.Name)
	}
	if got.Namespace != fn.Namespace {
		t.Errorf("Namespace = %q; want %q", got.Namespace, fn.Namespace)
	}
	if got.TriggerType != serverless.TriggerTypeWebSocket {
		t.Errorf("TriggerType = %q; want %q", got.TriggerType, serverless.TriggerTypeWebSocket)
	}
	if got.CallerClaims["role"] != "admin" {
		t.Errorf("CallerClaims[role] = %q; want %q", got.CallerClaims["role"], "admin")
	}
}

// TestBuildPersistentInvocationContext_NoJWT covers the non-authenticated
// path — namespace-key auth or unauthenticated. CallerJWTSubject must be ""
// (NOT crash, NOT panic). Everything else is whatever the helpers return for
// a bare request.
func TestBuildPersistentInvocationContext_NoJWT(t *testing.T) {
	h := newTestHandlers(nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	fn := &serverless.Function{
		ID:        "fn-id",
		Name:      "f",
		Namespace: "ns",
	}

	got := h.buildPersistentInvocationContext(req, fn, "client-id")

	if got == nil {
		t.Fatal("buildPersistentInvocationContext returned nil")
	}
	if got.CallerJWTSubject != "" {
		t.Errorf("CallerJWTSubject should be empty without JWT, got %q", got.CallerJWTSubject)
	}
	if got.WSClientID != "client-id" {
		t.Errorf("WSClientID = %q; want %q", got.WSClientID, "client-id")
	}
	if got.TriggerType != serverless.TriggerTypeWebSocket {
		t.Errorf("TriggerType = %q; want %q", got.TriggerType, serverless.TriggerTypeWebSocket)
	}
}

// TestBuildPersistentInvocationContext_MatchesStatelessHandler is a structural
// guard: the persistent and stateless WS paths must populate the same
// auth-identity fields. The two paths diverged silently for ~6 months; this
// test makes any future divergence loud.
//
// We compare the field set (not values — values come from the same request
// helpers and are exercised in the cases above).
func TestBuildPersistentInvocationContext_MatchesStatelessHandler(t *testing.T) {
	h := newTestHandlers(nil)

	claims := &auth.JWTClaims{Sub: "test-subject"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, claims))

	fn := &serverless.Function{ID: "id", Name: "n", Namespace: "ns"}
	got := h.buildPersistentInvocationContext(req, fn, "cid")

	// Compare against the helpers the stateless path uses on every frame
	// (ws_handler.go:140-145). If any of these returns a value but doesn't
	// land in the persistent invCtx, that's the same class of bug as
	// Layer 7.
	if got.CallerWallet != h.getWalletFromRequest(req) {
		t.Errorf("CallerWallet drift: persistent=%q, helper=%q",
			got.CallerWallet, h.getWalletFromRequest(req))
	}
	if got.CallerJWTSubject != h.getJWTSubjectFromRequest(req) {
		t.Errorf("CallerJWTSubject drift: persistent=%q, helper=%q",
			got.CallerJWTSubject, h.getJWTSubjectFromRequest(req))
	}
	// Claims comparison: deep-equal isn't worth the ceremony for nil-vs-nil;
	// just check both branches produce the same nilness.
	statelessClaims := h.getCallerClaimsFromRequest(req)
	if (got.CallerClaims == nil) != (statelessClaims == nil) {
		t.Errorf("CallerClaims nilness drift: persistent=%v, helper=%v",
			got.CallerClaims, statelessClaims)
	}
}
