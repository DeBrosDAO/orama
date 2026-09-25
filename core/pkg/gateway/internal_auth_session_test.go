package gateway

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// A token the main gateway verified, as the hop forwards it.
func hopClaims() *auth.JWTClaims {
	now := time.Now()
	return &auth.JWTClaims{
		Sub: "0xwallet",
		Iat: now.Add(-time.Minute).Unix(),
		Exp: now.Add(14 * time.Minute).Unix(),
		Jti: "0123456789abcdef0123456789abcdef",
	}
}

// signedJWTHop is a request as the main gateway sends it for a JWT caller.
func signedJWTHop(t *testing.T, key []byte, claims *auth.JWTClaims) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/functions/rpc/ws", nil)
	r.Header.Set(HeaderInternalAuthValidated, "true")
	r.Header.Set(HeaderInternalAuthNamespace, "anchat")
	setInternalAuthJWTHeaders(r.Header, claims)
	if err := signInternalAuthHeaders(key, r.Header, r.Method, r.URL.Path, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

// The bug: behind the hop the namespace gateway rebuilt claims with no exp and
// no jti, so a persistent socket read exp=0 as "never expires" and no
// revocation could name it.
func TestClaimsFromInternalAuthHeaders_carriesTheTokensTimesAndID(t *testing.T) {
	want := hopClaims()
	h := http.Header{}
	setInternalAuthJWTHeaders(h, want)

	got, err := claimsFromInternalAuthHeaders(h, "anchat", time.Now())
	if err != nil || got == nil {
		t.Fatalf("no claims recovered: %v", err)
	}
	if got.Exp != want.Exp || got.Iat != want.Iat || got.Jti != want.Jti {
		t.Errorf("recovered exp=%d iat=%d jti=%q, want exp=%d iat=%d jti=%q",
			got.Exp, got.Iat, got.Jti, want.Exp, want.Iat, want.Jti)
	}
}

// The production path end to end: signed on one side, verified and turned into
// the request's claims on the other.
func TestMiddlewareChain_hopCarriesTheTokensExpiryToTheHandler(t *testing.T) {
	logger, _ := logging.NewColoredLogger(logging.ComponentGateway, false)
	g := &Gateway{logger: logger, internalAuthKey: testHopKey(t), cfg: &Config{ClientNamespace: "anchat"}}
	want := hopClaims()

	var got *auth.JWTClaims
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	})
	g.internalAuthMiddleware(g.authMiddleware(next)).ServeHTTP(
		httptest.NewRecorder(), signedJWTHop(t, g.internalAuthKey, want))

	if got == nil {
		t.Fatal("the handler saw no claims for a genuine JWT hop")
	}
	if got.Exp != want.Exp {
		t.Errorf("exp behind the hop = %d, want %d — a socket opened here would never expire", got.Exp, want.Exp)
	}
	if got.Jti != want.Jti {
		t.Errorf("jti behind the hop = %q, want %q — a revoked session could not reach it", got.Jti, want.Jti)
	}
}

func TestVerifyInternalAuthHeaders_v2CoversTheTokensTimesAndID(t *testing.T) {
	key := testHopKey(t)
	for _, edit := range []struct{ header, value string }{
		{HeaderInternalAuthJWTExp, strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)},
		{HeaderInternalAuthJWTIat, strconv.FormatInt(time.Now().Unix()+3600, 10)},
		{HeaderInternalAuthJWTJti, "another-token"},
	} {
		r := signedJWTHop(t, key, hopClaims())
		r.Header.Set(edit.header, edit.value)
		if verifyInternalAuthHeaders(key, r, time.Now()) {
			t.Errorf("%s was changed after signing and the MAC still verified", edit.header)
		}
	}
}

// A request carrying a v2 MAC is judged by it alone. Falling back to v1 would
// let whoever broke the v2 MAC keep the fields v1 does not cover.
func TestVerifyInternalAuthHeaders_aFailedV2IsNotRetriedAsV1(t *testing.T) {
	key := testHopKey(t)
	r := signedJWTHop(t, key, hopClaims())
	r.Header.Set(HeaderInternalAuthMACv2, strconv.FormatInt(time.Now().Unix(), 10)+".00")

	if verifyInternalAuthHeaders(key, r, time.Now()) {
		t.Error("a request with a bad v2 MAC was accepted on its v1 MAC")
	}
}

// A main gateway that predates v2 stamps only the v1 MAC. The namespace
// gateway must keep accepting it through a rolling upgrade, and must not read
// the fields v1 does not cover.
func TestInternalAuthMiddleware_aV1HopIsAcceptedWithoutWhatV1DoesNotCover(t *testing.T) {
	g := testGatewayWithHopKey(t)
	r := signedJWTHop(t, g.internalAuthKey, hopClaims())
	r.Header.Del(HeaderInternalAuthMACv2) // what an old gateway sends
	r.Header.Set(HeaderInternalAuthJWTExp, "9999999999")

	var seen http.Header
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() })
	g.internalAuthMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)

	if seen.Get(HeaderInternalAuthValidated) != "true" || seen.Get(HeaderInternalAuthJWTSub) != "0xwallet" {
		t.Fatal("a v1 hop from a not-yet-upgraded main gateway was refused; the rollout would lock callers out")
	}
	for _, name := range internalAuthV2OnlyHeaders {
		if v := seen.Get(name); v != "" {
			t.Errorf("%s, which no MAC covered, reached the handler as %q", name, v)
		}
	}
	if seen.Get(HeaderInternalAuthMACv2) != "" || seen.Get(HeaderInternalAuthMAC) != "" {
		t.Error("a MAC was forwarded past the hop it authenticates")
	}
}

// A v1 hop cannot say when its token expires. It gets the most time any token
// has left rather than none, so a socket opened through it still ends.
func TestClaimsFromInternalAuthHeaders_aV1HopGetsTheLongestExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xwallet")

	got, err := claimsFromInternalAuthHeaders(h, "anchat", now)
	if err != nil || got == nil {
		t.Fatalf("a v1 hop's subject was dropped: %v", err)
	}
	if want := now.Add(auth.MaxTokenLifetime).Unix(); got.Exp != want {
		t.Errorf("exp = %d, want %d", got.Exp, want)
	}
	if got.Jti != "" {
		t.Errorf("jti = %q, want empty", got.Jti)
	}
}

// The main gateway applied the revocation list when it verified the token, so
// a v1 hop's issue time need only reach back one list staleness. Reaching back
// further would have an older "log out everywhere" close the fresh session
// the user signed in with afterwards — and keep closing it, since signing in
// again would not help.
func TestClaimsFromInternalAuthHeaders_aV1HopIsOnlyCaughtByARecentRevocation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xwallet")

	got, err := claimsFromInternalAuthHeaders(h, "anchat", now)
	if err != nil || got == nil {
		t.Fatalf("a v1 hop's subject was dropped: %v", err)
	}
	// RevocationList.Denies refuses a token when iat <= issued_before.
	loggedOutEverywhere := now.Add(-30 * time.Minute).Unix()
	if got.Iat <= loggedOutEverywhere {
		t.Errorf("iat %d is caught by a revocation of the subject 30 minutes ago", got.Iat)
	}
	justRevoked := now.Add(-auth.RevocationRefreshInterval / 2).Unix()
	if got.Iat > justRevoked {
		t.Errorf("iat %d escapes a revocation the main gateway's list may not have held yet", got.Iat)
	}
}

func TestClaimsFromInternalAuthHeaders_aZeroExpiryGetsTheLongestExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := http.Header{}
	h.Set(HeaderInternalAuthJWTSub, "0xwallet")
	h.Set(HeaderInternalAuthJWTExp, "0")
	h.Set(HeaderInternalAuthJWTIat, "1799999000")

	got, err := claimsFromInternalAuthHeaders(h, "anchat", now)
	if err != nil || got == nil {
		t.Fatalf("claims dropped: %v", err)
	}
	if want := now.Add(auth.MaxTokenLifetime).Unix(); got.Exp != want {
		t.Errorf("exp = %d, want %d: a socket would never expire", got.Exp, want)
	}
}

func TestClaimsFromInternalAuthHeaders_anUnreadableTimeRefusesTheIdentity(t *testing.T) {
	for _, tc := range []struct{ name, exp, iat string }{
		{"garbage exp", "soon", "1"},
		{"exp without iat", "1800000000", ""},
		{"garbage iat", "1800000000", "then"},
	} {
		h := http.Header{}
		h.Set(HeaderInternalAuthJWTSub, "0xwallet")
		h.Set(HeaderInternalAuthJWTExp, tc.exp)
		h.Set(HeaderInternalAuthJWTIat, tc.iat)
		got, err := claimsFromInternalAuthHeaders(h, "anchat", time.Now())
		if got != nil || err == nil {
			t.Errorf("%s: got claims %+v and error %v, want an error and no claims", tc.name, got, err)
		}
	}
}

// A namespace gateway that predates v2 verifies the v1 MAC exactly as it always
// has. If the v1 payload drifted by a byte, every hop from an upgraded main
// gateway to one of those would be stripped and every signed-in caller would
// arrive anonymous for the length of the rollout.
func TestInternalAuthPayload_v1IsTheFormatOlderGatewaysVerify(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderInternalAuthNamespace, "anchat")
	h.Set(HeaderInternalAuthJWTSub, "0xwallet")
	h.Set(HeaderInternalAuthJWTCustom, "eyJhIjoiYiJ9")
	h.Set(HeaderInternalAuthScopes, "admin")
	// Present, and not part of v1.
	h.Set(HeaderInternalAuthJWTExp, "1800000900")
	h.Set(HeaderInternalAuthJWTJti, "j")

	got := internalAuthPayload(internalAuthPayloadV1, "get", "/v1/functions/rpc/ws", h, 1_800_000_000)
	want := "orama-internal-auth-v1\nGET\n/v1/functions/rpc/ws\nanchat\n0xwallet\neyJhIjoiYiJ9\nadmin\n1800000000"
	if got != want {
		t.Errorf("v1 payload changed:\n got %q\nwant %q", got, want)
	}
}

func TestStripInboundInternalAuthHeaders_removesTheTokensTimesAndID(t *testing.T) {
	h := http.Header{}
	setInternalAuthJWTHeaders(h, hopClaims())

	stripInboundInternalAuthHeaders(h)
	for _, name := range internalAuthV2OnlyHeaders {
		if v := h.Get(name); v != "" {
			t.Errorf("%s survived the strip as %q", name, v)
		}
	}
}

// Every token lifetime the cluster mints must fit inside the window a v1 hop
// is given, or a socket from a longer-lived token would be closed early and a
// revocation older than MaxTokenLifetime could miss one issued before it.
func TestMaxTokenLifetime_boundsEveryMintedLifetime(t *testing.T) {
	for name, lifetime := range map[string]time.Duration{
		"session":  auth.AccessTokenLifetime,
		"workload": auth.WorkloadTokenLifetime,
	} {
		if lifetime > auth.MaxTokenLifetime {
			t.Errorf("a %s token lives %s, beyond MaxTokenLifetime %s", name, lifetime, auth.MaxTokenLifetime)
		}
	}
}
