package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

func signalRequestAs(sub string) *http.Request {
	r := httptest.NewRequest("GET", "/v1/webrtc/signal", nil)
	r.RemoteAddr = "203.0.113.7:4444"
	if sub == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: sub}))
}

const (
	walletA = "0x1111111111111111111111111111111111111111"
	walletB = "0x2222222222222222222222222222222222222222"
)

func TestWebRTCJoinAllowed_limitsPerIdentity(t *testing.T) {
	g := &Gateway{webrtcJoinRateLimiter: NewRateLimiter(60, 3)}
	for i := 0; i < 3; i++ {
		if !g.webrtcJoinAllowed(signalRequestAs(walletA)) {
			t.Fatalf("join %d refused inside the burst", i)
		}
	}
	if g.webrtcJoinAllowed(signalRequestAs(walletA)) {
		t.Fatal("a fourth join inside the burst window was allowed")
	}
	if !g.webrtcJoinAllowed(signalRequestAs(walletB)) {
		t.Fatal("another identity was limited by the first one's joins")
	}
}

// The subject is the identity, not the address: the same wallet from a second
// address shares its bucket.
func TestWebRTCJoinAllowed_identityNotAddress(t *testing.T) {
	g := &Gateway{webrtcJoinRateLimiter: NewRateLimiter(60, 1)}
	r1 := signalRequestAs(walletA)
	r2 := signalRequestAs(walletA)
	r2.RemoteAddr = "198.51.100.9:1"
	if !g.webrtcJoinAllowed(r1) || g.webrtcJoinAllowed(r2) {
		t.Fatal("the same identity from a second address did not share its bucket")
	}
}

func TestWebRTCJoinAllowed_noSubjectFallsBackToAddress(t *testing.T) {
	g := &Gateway{webrtcJoinRateLimiter: NewRateLimiter(60, 1)}
	if !g.webrtcJoinAllowed(signalRequestAs("")) || g.webrtcJoinAllowed(signalRequestAs("")) {
		t.Fatal("an unauthenticated address was not limited")
	}
}

func TestWebRTCJoinAllowed_noLimiterAllows(t *testing.T) {
	if !(&Gateway{}).webrtcJoinAllowed(signalRequestAs(walletA)) {
		t.Fatal("a gateway with no limiter refused a join")
	}
}
