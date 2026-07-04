package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// newAuthServiceForTest builds a real auth.Service backed by a temporary
// EdDSA key, suitable for end-to-end auth-middleware tests. Mirrors the
// shape of pkg/gateway/auth/service_test.go::createDualKeyService but lives
// in package gateway so we don't need to export internals.
func newAuthServiceForTest(t *testing.T) *auth.Service {
	t.Helper()
	logger, _ := logging.NewColoredLogger(logging.ComponentGeneral, false)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	rsaPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})
	s, err := auth.NewService(logger, nil, string(rsaPEM), "default")
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	s.SetEdDSAKey(edPriv)
	return s
}

// Bug #240: WebSocket clients on browsers and React Native can't reliably
// set custom headers on the upgrade request. The auth middleware now
// accepts a JWT via `?jwt=` query parameter — but only for WebSocket
// upgrade requests. These tests lock that contract in.

func TestAuthMiddleware_WSJWTQuery_validToken(t *testing.T) {
	svc := newAuthServiceForTest(t)
	token, _, err := svc.GenerateJWT("anchat-test", "0xWALLET_SUBJECT", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	g := &Gateway{authService: svc}

	var gotClaims *auth.JWTClaims
	var gotNamespace string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(ctxKeyJWT); v != nil {
			gotClaims, _ = v.(*auth.JWTClaims)
		}
		if v := r.Context().Value(CtxKeyNamespaceOverride); v != nil {
			gotNamespace, _ = v.(string)
		}
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc-router/ws?jwt="+token, nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if gotClaims == nil {
		t.Fatal("ctxKeyJWT not set on the next handler's context")
	}
	if gotClaims.Sub != "0xWALLET_SUBJECT" {
		t.Errorf("claims.Sub = %q, want %q", gotClaims.Sub, "0xWALLET_SUBJECT")
	}
	if gotNamespace != "anchat-test" {
		t.Errorf("namespace override = %q, want %q", gotNamespace, "anchat-test")
	}
}

func TestAuthMiddleware_WSJWTQuery_invalidTokenFallsThrough(t *testing.T) {
	// Invalid JWT in ?jwt= must NOT set ctxKeyJWT and must NOT short-circuit
	// to success — middleware should fall through to API-key path.
	svc := newAuthServiceForTest(t)
	g := &Gateway{authService: svc}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	// Three-segment string that ParseAndVerifyJWT will reject (bad signature).
	bogus := "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ4In0.bogussignature"
	r := httptest.NewRequest(http.MethodGet, "/v1/functions/private-fn/ws?jwt="+bogus, nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	// No valid creds anywhere → middleware should 401, not call next.
	if called {
		t.Error("next handler was called despite invalid JWT — middleware short-circuited incorrectly")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_WSJWTQuery_ignoredOnNonWSRequest(t *testing.T) {
	// Putting a JWT in ?jwt= on a regular HTTP request must NOT authenticate.
	// We deliberately scope query-string JWT to WS upgrades to avoid the
	// privacy issues of JWTs leaking via referrer headers, browser history,
	// and access logs.
	svc := newAuthServiceForTest(t)
	token, _, err := svc.GenerateJWT("ns", "sub", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	g := &Gateway{authService: svc}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	// Regular GET (no Upgrade header).
	r := httptest.NewRequest(http.MethodGet, "/v1/some-private-endpoint?jwt="+token, nil)
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if called {
		t.Error("non-WS request with ?jwt= was authenticated — must be WS-only")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_WSJWTQuery_headerWinsOverQuery(t *testing.T) {
	// Both Authorization: Bearer <header-jwt> AND ?jwt=<query-jwt> present.
	// Header path runs FIRST and wins. Verifies the query fallback is a
	// fallback, not an override.
	svc := newAuthServiceForTest(t)
	headerJWT, _, _ := svc.GenerateJWT("ns-header", "sub-header", 15*time.Minute, nil)
	queryJWT, _, _ := svc.GenerateJWT("ns-query", "sub-query", 15*time.Minute, nil)

	g := &Gateway{authService: svc}

	var got *auth.JWTClaims
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if v := r.Context().Value(ctxKeyJWT); v != nil {
			got, _ = v.(*auth.JWTClaims)
		}
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/fn/ws?jwt="+queryJWT, nil)
	r.Header.Set("Authorization", "Bearer "+headerJWT)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if got == nil {
		t.Fatal("ctxKeyJWT not set")
	}
	if got.Sub != "sub-header" {
		t.Errorf("Sub = %q, want %q (header should win over query)", got.Sub, "sub-header")
	}
}

func TestAuthMiddleware_WSJWTQuery_emptyJWTParamFallsThrough(t *testing.T) {
	// `?jwt=` with empty value should not affect anything — fall through to
	// API key / default path.
	svc := newAuthServiceForTest(t)
	g := &Gateway{authService: svc}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/fn/ws?jwt=", nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if called {
		t.Error("empty ?jwt= unexpectedly authenticated the request")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_WSJWTQuery_malformedJWTFallsThrough(t *testing.T) {
	// `?jwt=not-a-jwt` — single segment, no dots. Must NOT call
	// ParseAndVerifyJWT (the dot-count gate skips it) AND must NOT
	// authenticate.
	svc := newAuthServiceForTest(t)
	g := &Gateway{authService: svc}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/fn/ws?jwt=not-a-jwt", nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if called {
		t.Error("non-JWT-shaped ?jwt= value was treated as authenticated")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// validateAuthForNamespaceProxy — same WS-JWT-query path, in the main
// gateway's pre-validation flow.

func TestValidateAuthForNamespaceProxy_WSJWTQuery(t *testing.T) {
	svc := newAuthServiceForTest(t)
	token, _, err := svc.GenerateJWT("anchat-test", "0xWALLET", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	g := &Gateway{authService: svc}

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc-router/ws?jwt="+token, nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")

	ns, claims, _, errMsg := g.validateAuthForNamespaceProxy(r)
	if errMsg != "" {
		t.Fatalf("unexpected errMsg: %q", errMsg)
	}
	if ns != "anchat-test" {
		t.Errorf("namespace = %q, want %q", ns, "anchat-test")
	}
	if claims == nil {
		t.Fatal("claims nil; expected JWT claims set")
	}
	if claims.Sub != "0xWALLET" {
		t.Errorf("Sub = %q, want %q", claims.Sub, "0xWALLET")
	}
}

func TestValidateAuthForNamespaceProxy_WSJWTQuery_ignoredOnNonWS(t *testing.T) {
	svc := newAuthServiceForTest(t)
	token, _, err := svc.GenerateJWT("anchat-test", "0xWALLET", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	g := &Gateway{authService: svc}

	r := httptest.NewRequest(http.MethodGet, "/v1/invoke/rpc-router?jwt="+token, nil)
	// No Upgrade headers — this is a regular HTTP request.

	ns, claims, _, errMsg := g.validateAuthForNamespaceProxy(r)
	if ns != "" || claims != nil {
		t.Errorf("non-WS request was authenticated via ?jwt= — expected (\"\", nil), got (%q, %#v)", ns, claims)
	}
	if errMsg != "" {
		t.Errorf("unexpected errMsg on no-auth no-WS path: %q", errMsg)
	}
}

// TestAuthMiddleware_WSJWTQuery_strippedAfterVerify guards the hardening
// recommendation from the security audit: the `?jwt=` value MUST be
// stripped from r.URL.RawQuery after a successful verify so the token
// doesn't leak into proxy hops or downstream logs.
func TestAuthMiddleware_WSJWTQuery_strippedAfterVerify(t *testing.T) {
	svc := newAuthServiceForTest(t)
	token, _, err := svc.GenerateJWT("anchat-test", "0xWALLET", 15*time.Minute, nil)
	if err != nil {
		t.Fatalf("GenerateJWT: %v", err)
	}

	g := &Gateway{authService: svc}

	var seenQueryHasJWT bool
	var seenRawQuery string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenRawQuery = r.URL.RawQuery
		seenQueryHasJWT = r.URL.Query().Has("jwt")
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/fn/ws?jwt="+token+"&other=keepme", nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if seenQueryHasJWT {
		t.Errorf("`jwt` param survived into downstream handler: RawQuery=%q", seenRawQuery)
	}
	// Other query params must survive — strip is surgical.
	if !strings.Contains(seenRawQuery, "other=keepme") {
		t.Errorf("unrelated query param dropped: RawQuery=%q", seenRawQuery)
	}
}

// TestAuthMiddleware_WSJWTQuery_oversizedTokenRejected ensures the cheap
// length gate at the start of the branch refuses absurdly long tokens
// before reaching the cryptographic verifier (cheap DoS defense).
func TestAuthMiddleware_WSJWTQuery_oversizedTokenRejected(t *testing.T) {
	svc := newAuthServiceForTest(t)
	g := &Gateway{authService: svc}

	called := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	// 8 KB of dot-padded garbage — exceeds maxQueryJWTLength (4 KB).
	huge := strings.Repeat("a", 4000) + "." + strings.Repeat("b", 4000) + ".sig"
	if len(huge) <= maxQueryJWTLength {
		t.Fatalf("test setup wrong: token len=%d should exceed cap %d", len(huge), maxQueryJWTLength)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/functions/fn/ws?jwt="+huge, nil)
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	g.authMiddleware(next).ServeHTTP(w, r)

	if called {
		t.Error("oversized ?jwt= was accepted — length cap not enforced")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// TestStripJWTQueryParam_idempotent — the helper is called from two paths
// and should be safe to call on requests without a `jwt` param.
func TestStripJWTQueryParam_idempotent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Strip-path: jwt present → re-encoded (url.Values.Encode sorts).
		{"foo=bar&jwt=secret&baz=qux", "baz=qux&foo=bar"},
		{"jwt=secret", ""},
		{"jwt=secret&jwt=other", ""}, // both copies removed
		// No-op path: no jwt present → query left untouched (preserves
		// original ordering and any encoding quirks).
		{"foo=bar&baz=qux", "foo=bar&baz=qux"},
		{"", ""},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/?"+tc.in, nil)
		stripJWTQueryParam(r)
		if r.URL.RawQuery != tc.want {
			t.Errorf("strip(%q) = %q, want %q", tc.in, r.URL.RawQuery, tc.want)
		}
	}
}

// Just to keep go vet happy when wiring custom test contexts.
var _ = context.Background
