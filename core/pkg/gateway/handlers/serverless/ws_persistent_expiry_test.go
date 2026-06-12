package serverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// TestWSJWTExpired is the core security regression guard for bugboard #868: a
// persistent WS authenticates ONCE at upgrade, and the read loop must stop
// serving application frames once the authorizing JWT is past exp+grace.
//
// If wsJWTExpired starts returning false for a clearly-expired token (or true
// for a still-valid one), an expired token regains full RPC access — including
// turn.credentials minting — for the socket's lifetime.
func TestWSJWTExpired(t *testing.T) {
	// Fixed reference instant so the table is deterministic (the read loop
	// uses time.Now() in production; the pure function takes `now` for tests).
	now := time.Unix(1_700_000_000, 0)
	grace := 120 * time.Second

	cases := []struct {
		name    string
		expUnix int64
		now     time.Time
		want    bool
	}{
		{
			name:    "no expiry to enforce (API-key auth, exp=0) never expires",
			expUnix: 0,
			now:     now,
			want:    false,
		},
		{
			name:    "negative exp treated as no-expiry (defensive)",
			expUnix: -5,
			now:     now,
			want:    false,
		},
		{
			name:    "token valid, well before exp",
			expUnix: now.Add(10 * time.Minute).Unix(),
			now:     now,
			want:    false,
		},
		{
			name:    "token just past exp but inside grace window — still allowed",
			expUnix: now.Add(-30 * time.Second).Unix(),
			now:     now,
			want:    false,
		},
		{
			name:    "token exactly at exp+grace boundary — not yet expired (After is strict)",
			expUnix: now.Add(-grace).Unix(),
			now:     now,
			want:    false,
		},
		{
			name:    "token past exp+grace — expired, must reject",
			expUnix: now.Add(-(grace + time.Second)).Unix(),
			now:     now,
			want:    true,
		},
		{
			name:    "token long expired — expired",
			expUnix: now.Add(-24 * time.Hour).Unix(),
			now:     now,
			want:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wsJWTExpired(tc.expUnix, tc.now, grace)
			if got != tc.want {
				t.Errorf("wsJWTExpired(exp=%d, now=%d, grace=%s) = %v; want %v",
					tc.expUnix, tc.now.Unix(), grace, got, tc.want)
			}
		})
	}
}

// TestGetJWTExpiryFromRequest verifies the gateway reads the authorizing JWT's
// exp off the request context at upgrade. This is the value the read loop
// enforces for the socket's lifetime (#868); if it silently returns 0 for a
// JWT-authenticated request, expiry enforcement is disabled and the bug
// re-opens.
func TestGetJWTExpiryFromRequest(t *testing.T) {
	h := newTestHandlers(nil)

	t.Run("JWT with exp returns exp", func(t *testing.T) {
		claims := &auth.JWTClaims{Sub: "alice", Exp: 1_700_000_123}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, claims))

		if got := h.getJWTExpiryFromRequest(req); got != 1_700_000_123 {
			t.Errorf("getJWTExpiryFromRequest = %d; want 1700000123", got)
		}
	})

	t.Run("no JWT on context returns 0 (API-key / unauthenticated)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := h.getJWTExpiryFromRequest(req); got != 0 {
			t.Errorf("getJWTExpiryFromRequest = %d; want 0", got)
		}
	})

	t.Run("nil claims under key returns 0", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		var nilClaims *auth.JWTClaims
		req = req.WithContext(context.WithValue(req.Context(), ctxkeys.JWT, nilClaims))
		if got := h.getJWTExpiryFromRequest(req); got != 0 {
			t.Errorf("getJWTExpiryFromRequest = %d; want 0", got)
		}
	})
}

// TestWSAuthState_refreshExtendsExpiry documents the auth.refresh contract that
// the read loop relies on (#868 + #321): a successful auth.refresh moves the
// enforced expiry forward to the new token's exp, so a socket that refreshes
// before its grace window closes keeps serving RPCs uninterrupted.
//
// We assert the state-transition directly (the full handler needs a live WS
// conn for the ack write; that path is exercised by integration tests). The
// invariant: after refresh, a `now` that WOULD have expired the old token no
// longer expires the socket.
func TestWSAuthState_refreshExtendsExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	grace := 120 * time.Second

	oldExp := now.Add(-(grace + time.Minute)).Unix() // already past grace → expired
	state := &wsAuthState{expUnix: oldExp}

	if !wsJWTExpired(state.expUnix, now, grace) {
		t.Fatalf("precondition: old token should be expired at now")
	}

	// Simulate what handleAuthRefresh does on success: adopt the new token's
	// exp.
	newExp := now.Add(15 * time.Minute).Unix()
	state.expUnix = newExp

	if wsJWTExpired(state.expUnix, now, grace) {
		t.Errorf("after refresh the socket must NOT be expired (exp=%d, now=%d)",
			state.expUnix, now.Unix())
	}
}
