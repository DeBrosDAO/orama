package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
)

func TestRouteScope(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// public / invoke — no scope
		{"/health", ""},
		{"/v1/invoke/ns/fn", ""},
		{"/v1/node/enroll", ""},
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
		{"/v1/network/connect", auth.ScopeAdmin},
		{"/v1/network/disconnect", auth.ScopeAdmin},
		{"/v1/network/status", ""},
		{"/v1/network/peers", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := policyOf(http.MethodPost, tt.path).Scope; got != tt.want {
				t.Errorf("%q requires scope %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// The token a route asks for beyond the grant. It used to be derived from the
// grant name, so every route sharing a grant shared the requirement whether or
// not that was intended; it is declared per route now, and this is the set that
// must still ask for a logged-in user.
func TestRouteToken(t *testing.T) {
	wallet := []string{
		"/v1/storage/upload", "/v1/storage/pin", "/v1/storage/get/Qm1", "/v1/storage/status/Qm1",
		"/v1/webrtc/signal", "/v1/webrtc/rooms", "/v1/webrtc/turn/credentials",
		"/v1/proxy/anon", "/v1/proxy/tunnel",
	}
	for _, path := range wallet {
		if got := policyOf(http.MethodPost, path).Token; got != routepolicy.WalletToken {
			t.Errorf("%q asks for token %v, want a logged-in user — an extracted runtime "+
				"key would otherwise reach it on its own", path, got)
		}
	}

	anyCredential := []string{
		"/v1/pubsub/publish", "/v1/push/devices", "/v1/cache/get",
		"/v1/functions/fn/ws", "/v1/deployments/list", "/v1/audit",
	}
	for _, path := range anyCredential {
		if got := policyOf(http.MethodPost, path).Token; got != routepolicy.AnyCredential {
			t.Errorf("%q asks for token %v, want none beyond the grant", path, got)
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
		t.Error("an api-key-exchanged JWT in the legacy key format counted as a wallet JWT")
	}
	if hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "orama_rk_2fJ8xQ_9Zc"})) {
		t.Error("an api-key-exchanged JWT counted as a wallet JWT — this is the check that stops " +
			"an extracted runtime key acting as a logged-in user")
	}
	// A subject that is neither an address nor a key shape is treated as a key,
	// not as a user. It used to be the other way round — anything without the
	// `ak_` prefix was a wallet — which is fail-open: the one thing that must
	// never happen is a key being read as a logged-in user.
	if hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "did:ethr:not-an-address"})) {
		t.Error("a subject nothing recognises counted as a wallet JWT")
	}
	// A Solana address is a wallet, in whatever case it was normalised to.
	if !hasWalletJWT(reqWithJWT(&auth.JWTClaims{Sub: "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"})) {
		t.Error("a Solana wallet JWT was not recognised")
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

	// Wallet JWT + an owner grant → admin.
	rOwner := reqWithJWT(&auth.JWTClaims{Sub: "0xOWNER"})
	rOwner = markGrant(rOwner, &auth.Grant{Role: auth.RoleOwner})
	if s := g.callerScopes(rOwner); !s.IsAdmin() {
		t.Error("the namespace owner's wallet should be admin")
	}

	// The whole point of the roles: a member who is not an owner or an admin
	// does not get the control plane. This used to be a boolean, so everyone
	// the authorization gate let through was an admin.
	rRuntime := reqWithJWT(&auth.JWTClaims{Sub: "0xTEAMMATE"})
	rRuntime = markGrant(rRuntime, &auth.Grant{Role: auth.RoleRuntime})
	if s := g.callerScopes(rRuntime); s.IsAdmin() {
		t.Error("a runtime member reached the control plane")
	} else if !s.Has(auth.ScopeStorage) {
		t.Error("a runtime member should hold the data plane")
	}

	rReader := reqWithJWT(&auth.JWTClaims{Sub: "0xREADER"})
	rReader = markGrant(rReader, &auth.Grant{Role: auth.RoleReader})
	if s := g.callerScopes(rReader); s.IsAdmin() || s.Has(auth.ScopeStorage) {
		t.Error("a reader holds no grant at all")
	}

	// A grant with a selector holds the scope that selector narrows and nothing
	// else. The scope gate lets it reach storage; AuthorizeResource in the
	// handler is what decides which object.
	rScoped := reqWithJWT(&auth.JWTClaims{Sub: "0xSCOPED"})
	rScoped = markGrant(rScoped, &auth.Grant{Role: auth.RoleRuntime, Resource: "storage:avatars/*"})
	scoped := g.callerScopes(rScoped)
	if !scoped.Has(auth.ScopeStorage) {
		t.Error("a grant narrowed to storage:avatars/* cannot reach storage at all")
	}
	if scoped.Has(auth.ScopePubsub) || scoped.IsAdmin() {
		t.Errorf("it holds %q; a storage selector says nothing about anything else", scoped.Canonical())
	}

	// A selector in a domain nothing narrows still authorises nothing, or the
	// narrower-looking grant is the wide one.
	rUnenforced := reqWithJWT(&auth.JWTClaims{Sub: "0xUNENFORCED"})
	rUnenforced = markGrant(rUnenforced, &auth.Grant{Role: auth.RoleAdmin, Resource: "db:table=posts:read"})
	if s := g.callerScopes(rUnenforced); len(s) != 0 {
		t.Errorf("a grant narrowed to a domain no data path applies holds %q", s.Canonical())
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

// Unpinning is the one storage operation a userless job may reach: it is
// ownership-checked in its handler and can only drop the namespace's own pins.
// Every other storage operation keeps the strict requirement, and so does every
// other method on the unpin route.
func TestStorageUnpinToken(t *testing.T) {
	if got := policyOf(http.MethodDelete, "/v1/storage/unpin/Qm123").Token; got != routepolicy.AnyToken {
		t.Errorf("DELETE unpin asks for token %v, want any exchanged token (#151)", got)
	}
	if got := policyOf(http.MethodPost, "/v1/storage/unpin/Qm123").Token; got != routepolicy.WalletToken {
		t.Errorf("POST to the unpin route asks for token %v; only DELETE is the reclaim", got)
	}
	for _, path := range []string{"/v1/storage/upload", "/v1/storage/get/Qm1", "/v1/storage/pin", "/v1/storage/status/Qm1"} {
		if got := policyOf(http.MethodDelete, path).Token; got != routepolicy.WalletToken {
			t.Errorf("%q asks for token %v, want a logged-in user", path, got)
		}
	}
	// And the grant is unchanged: the relaxation is about the token, not the
	// scope. A key with no storage grant reaches none of it.
	if got := policyOf(http.MethodDelete, "/v1/storage/unpin/Qm123").Scope; got != auth.ScopeStorage {
		t.Errorf("DELETE unpin requires scope %q, want %q", got, auth.ScopeStorage)
	}
}

// The exact relaxation scopeMiddleware applies for bugboard #151: unpin with
// any exchanged token is allowed, a bare API key is not, and it never leaks to
// another storage operation.
func TestUnpinException_decision(t *testing.T) {
	g := &Gateway{}
	storage := auth.ParseScopes("invoke,storage,push,webrtc,proxy")

	exchanged := reqDelJWT("/v1/storage/unpin/Qm1", &auth.JWTClaims{Sub: "ak_x:ns"})
	if !g.hasRequiredToken(exchanged, policyOf(http.MethodDelete, exchanged.URL.Path), storage) {
		t.Error("unpin with an exchanged storage-scoped token must be allowed (#151)")
	}

	bare := httptest.NewRequest(http.MethodDelete, "/v1/storage/unpin/Qm1", nil)
	if g.hasRequiredToken(bare, policyOf(http.MethodDelete, bare.URL.Path), storage) {
		t.Error("unpin with a bare API key (no token) must NOT be allowed")
	}

	upload := reqDelJWT("/v1/storage/upload", &auth.JWTClaims{Sub: "ak_x:ns"})
	if g.hasRequiredToken(upload, policyOf(http.MethodDelete, upload.URL.Path), storage) {
		t.Error("upload must keep the strict logged-in-user requirement")
	}

	// An admin credential is exempt everywhere: the requirement exists to make
	// a leaked data-plane key inert, and an admin key is not one.
	if !g.hasRequiredToken(bare, policyOf(http.MethodPost, "/v1/storage/upload"), auth.ParseScopes("admin")) {
		t.Error("an admin credential must not be asked for a user token")
	}
}

// Minting a cluster invite hands the holder every secret the cluster has, and
// the JWT signing key is derived from one of them. These paths had no entry at
// all, so they fell through to "any valid credential is enough" — and a key out
// of a public app bundle is a valid credential.
func TestRouteScope_operatorEndpointsNeedAdmin(t *testing.T) {
	for _, path := range []string{
		"/v1/operator/invite",
		"/v1/operator/nodes",
		"/v1/operator/node/register",
	} {
		if got := policyOf(http.MethodPost, path).Scope; got != auth.ScopeAdmin {
			t.Errorf("%s requires scope %q, want %q — an invoke-only key must not "+
				"reach it", path, got, auth.ScopeAdmin)
		}
	}
}
