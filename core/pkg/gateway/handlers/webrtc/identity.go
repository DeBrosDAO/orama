package webrtc

import (
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// caller is who the gateway authenticated for a request.
type caller struct {
	// UserID is the token's subject: a wallet, or a deployed app's workload.
	UserID string
	// DeviceID is the device the session is bound to, "" when bound to none.
	DeviceID string
}

// callerOf reads the identity the auth middleware validated. It never reads a
// request header or the join frame: a client controls both. ok is false when no
// token was validated, which the route's policy should have made impossible.
func callerOf(r *http.Request) (caller, bool) {
	claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims)
	if !ok || claims == nil {
		return caller{}, false
	}
	sub := strings.TrimSpace(claims.Sub)
	if sub == "" {
		return caller{}, false
	}
	return caller{UserID: sub, DeviceID: strings.TrimSpace(claims.Did)}, true
}
