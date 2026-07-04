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
		{"/v1/auth/token", ""},   // not public, but any key may exchange
		{"/v1/auth/whoami", ""},  // any valid credential
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
