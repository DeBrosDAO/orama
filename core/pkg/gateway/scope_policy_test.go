package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

func TestRequiredScope(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// public / invoke — no scope
		{"/health", ""},
		{"/v1/invoke/ns/fn", ""},
		{"/v1/functions/fn/invoke", ""},
		{"/v1/auth/token", ""},  // not public, but any key may exchange
		{"/v1/auth/whoami", ""}, // any valid credential
		// invoke transport
		{"/v1/functions/fn/ws", auth.ScopeInvoke},
		// functions control-plane
		{"/v1/functions", auth.ScopeAdmin},
		{"/v1/functions/fn", auth.ScopeAdmin},
		{"/v1/functions/secrets", auth.ScopeAdmin},
		// storage (data-plane)
		{"/v1/storage/upload", auth.ScopeStorage},
		{"/v1/storage/get/Qm123", auth.ScopeStorage},
		{"/v1/storage/unpin/Qm123", auth.ScopeStorage},
		// push — mixed prefix
		{"/v1/push/devices", auth.ScopePush},
		{"/v1/push/devices/42", auth.ScopePush},
		{"/v1/push/config", auth.ScopeAdmin},
		{"/v1/push/send", auth.ScopeAdmin},
		{"/v1/namespace/push-credentials", auth.ScopeAdmin},
		{"/v1/namespace/push-credentials/apns", auth.ScopeAdmin},
		// webrtc (data-plane) vs namespace webrtc mgmt (admin) vs status (read)
		{"/v1/webrtc/signal", auth.ScopeWebRTC},
		{"/v1/webrtc/turn/credentials", auth.ScopeWebRTC},
		{"/v1/namespace/webrtc/enable", auth.ScopeAdmin},
		{"/v1/namespace/webrtc/status", ""},
		// proxy / pubsub / cache
		{"/v1/proxy/anon", auth.ScopeProxy},
		{"/v1/pubsub/publish", auth.ScopePubsub},
		{"/v1/cache/get", auth.ScopeCache},
		// control-plane
		{"/v1/rqlite/query", auth.ScopeAdmin},
		{"/v1/deployments/list", auth.ScopeAdmin},
		{"/v1/db/sqlite/query", auth.ScopeAdmin},
		{"/v1/serverless/ws/connections", auth.ScopeAdmin},
		{"/v1/namespace/rate-limit", auth.ScopeAdmin},
		{"/v1/namespace/keys", auth.ScopeAdmin},
		{"/v1/namespace/keys/5", auth.ScopeAdmin},
		{"/v1/node/status", auth.ScopeAdmin},
		{"/v1/node/command", auth.ScopeAdmin},
		{"/v1/node/logs", auth.ScopeAdmin},
		{"/v1/node/leave", auth.ScopeAdmin},
		{"/v1/node/enroll", ""},
		{"/v1/network/connect", auth.ScopeAdmin},
		{"/v1/network/disconnect", auth.ScopeAdmin},
		{"/v1/network/status", ""},
		{"/v1/network/peers", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := requiredScope(http.MethodPost, tt.path); got != tt.want {
				t.Errorf("requiredScope(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestRequiresUserJWT(t *testing.T) {
	yes := []string{auth.ScopeStorage, auth.ScopeWebRTC, auth.ScopeProxy}
	no := []string{auth.ScopeInvoke, auth.ScopePush, auth.ScopeAdmin, auth.ScopePubsub, auth.ScopeCache, ""}
	for _, g := range yes {
		if !requiresUserJWT(g) {
			t.Errorf("requiresUserJWT(%q) = false, want true", g)
		}
	}
	for _, g := range no {
		if requiresUserJWT(g) {
			t.Errorf("requiresUserJWT(%q) = true, want false", g)
		}
	}
}

func reqWithJWT(claims *auth.JWTClaims) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/storage/upload", nil)
	return r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, claims))
}

func TestHasWalletJWT(t *testing.T) {
	if !hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "0xWALLET"})) {
		t.Error("wallet JWT should be recognized")
	}
	if hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "ak_abc:ns"})) {
		t.Error("api-key-exchanged JWT (ak_ prefix) must NOT count as a wallet JWT")
	}
	// A colon-bearing but non-ak_ subject (e.g. a future DID/CAIP wallet) IS a
	// user — only the ak_ prefix marks an exchanged key.
	if !hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "did:ethr:0xabc"})) {
		t.Error("non-ak_ subject must count as a wallet JWT")
	}
	if hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: ""})) {
		t.Error("empty subject must not count")
	}
	if hasWalletJWT(httptest.NewRequest(http.MethodPost, "/x", nil)) {
		t.Error("no JWT must not count")
	}
}

func TestCallerScopes(t *testing.T) {
	g := &Gateway{}

	// API-key identity: scopes come from ctx.
	rKey := httptest.NewRequest(http.MethodPost, "/x", nil)
	rKey = rKey.WithContext(context.WithValue(rKey.Context(), ctxKeyScopes, auth.ScopeSet{auth.ScopeInvoke: {}, auth.ScopeStorage: {}}))
	if s := g.callerScopes(rKey); !s.Has(auth.ScopeStorage) || s.Has(auth.ScopeAdmin) {
		t.Errorf("api-key scopes wrong: %v", s)
	}

	// Exchanged RUNTIME key JWT must NOT escalate to admin.
	rRun := reqWithJWT(&auth.JWTClaims{Sub: "ak_x:ns", Custom: map[string]string{"scopes": "invoke,storage"}})
	if s := g.callerScopes(rRun); s.Has(auth.ScopeAdmin) {
		t.Error("exchanged runtime-key JWT escalated to admin — escalation hole open")
	}

	// Exchanged ADMIN key JWT keeps admin.
	rAdm := reqWithJWT(&auth.JWTClaims{Sub: "ak_x:ns", Custom: map[string]string{"scopes": "admin"}})
	if s := g.callerScopes(rAdm); !s.IsAdmin() {
		t.Error("exchanged admin-key JWT should be admin")
	}

	// Wallet JWT, no owner confirmation → data-plane, never admin.
	rWallet := reqWithJWT(&auth.JWTClaims{Sub: "0xWALLET"})
	s := g.callerScopes(rWallet)
	if s.IsAdmin() {
		t.Error("plain wallet JWT must not be admin")
	}
	if !s.Has(auth.ScopeStorage) {
		t.Error("wallet JWT should hold data-plane storage grant")
	}

	// CRITICAL regression (claims-provider injection): a WALLET JWT that carries
	// an injected custom["scopes"]="admin" must NOT be trusted — a non-ak_
	// subject's scopes claim is ignored, so it stays data-plane.
	rInjected := reqWithJWT(&auth.JWTClaims{Sub: "0xWALLET", Custom: map[string]string{"scopes": "admin"}})
	if g.callerScopes(rInjected).IsAdmin() {
		t.Error("wallet JWT with injected scopes:admin escalated to admin — claims-provider injection hole open")
	}

	// Wallet JWT + confirmed owner → admin.
	rOwner := reqWithJWT(&auth.JWTClaims{Sub: "0xOWNER"})
	rOwner = rOwner.WithContext(context.WithValue(rOwner.Context(), ctxKeyOwnerConfirmed, true))
	if s := g.callerScopes(rOwner); !s.IsAdmin() {
		t.Error("confirmed owner wallet should be admin")
	}
}

// reqDelJWT builds a DELETE request with optional JWT claims in context.
func reqDelJWT(path string, claims *auth.JWTClaims) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, path, nil)
	if claims != nil {
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, claims))
	}
	return r
}

func TestHasAnyJWT(t *testing.T) {
	if !hasAnyJWT(reqWithJWT(&auth.JWTClaims{Sub: "0xWALLET"})) {
		t.Error("wallet JWT should count as a JWT")
	}
	// Unlike hasWalletJWT, an exchanged api-key JWT DOES count here (#151).
	if !hasAnyJWT(reqWithJWT(&auth.JWTClaims{Sub: "ak_abc:ns"})) {
		t.Error("exchanged api-key JWT should count for hasAnyJWT")
	}
	if hasAnyJWT(reqWithJWT(&auth.JWTClaims{Sub: ""})) {
		t.Error("empty-subject JWT must not count")
	}
	// Bare api key (no JWT in context) must NOT count — keeps layer-1's
	// "an extracted key is inert without a JWT" property.
	if hasAnyJWT(httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/Qm1", nil)) {
		t.Error("no JWT (bare api key) must not count")
	}
}

func TestIsStorageUnpinPath(t *testing.T) {
	if !isStorageUnpinPath(http.MethodDelete, "/v1/storage/unpin/Qm123") {
		t.Error("DELETE /v1/storage/unpin/:cid should match")
	}
	if isStorageUnpinPath(http.MethodPost, "/v1/storage/unpin/Qm123") {
		t.Error("only DELETE should match the unpin exception")
	}
	// Every OTHER storage op keeps the strict wallet-JWT requirement.
	for _, p := range []string{"/v1/storage/upload", "/v1/storage/get/Qm1", "/v1/storage/pin", "/v1/storage/status/Qm1"} {
		if isStorageUnpinPath(http.MethodDelete, p) {
			t.Errorf("%q must NOT be treated as the unpin exception", p)
		}
	}
}

// TestUnpinException_decision locks in the exact layer-1 relaxation used by
// scopeMiddleware for bugboard #151: unpin + any scoped JWT is allowed; a bare
// api key (no JWT) is not; and the exception never leaks to other storage ops.
func TestUnpinException_decision(t *testing.T) {
	// unpin + exchanged storage-scoped JWT → allowed.
	exchanged := reqDelJWT("/v1/storage/unpin/Qm1", &auth.JWTClaims{Sub: "ak_x:ns", Custom: map[string]string{"scopes": "invoke,storage,push,webrtc,proxy"}})
	if !(isStorageUnpinPath(exchanged.Method, exchanged.URL.Path) && hasAnyJWT(exchanged)) {
		t.Error("unpin with an exchanged storage-scoped JWT must be allowed (#151)")
	}
	// unpin + BARE api key (no JWT) → NOT allowed.
	bare := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/Qm1", nil)
	if isStorageUnpinPath(bare.Method, bare.URL.Path) && hasAnyJWT(bare) {
		t.Error("unpin with a bare api key (no JWT) must NOT be allowed")
	}
	// The exception must never apply to upload (a DELETE-shaped probe still
	// fails because the path isn't the unpin path).
	if isStorageUnpinPath(http.MethodDelete, "/v1/storage/upload") {
		t.Error("upload must keep the strict wallet-JWT requirement, not the unpin exception")
	}
}
