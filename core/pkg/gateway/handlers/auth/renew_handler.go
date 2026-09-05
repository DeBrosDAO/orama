package auth

import (
	"net/http"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A workload renewing its own token.
//
// The credential a deployment starts with is staged by systemd from a file only
// the gateway can write, which is what lets an unprivileged gateway hand a
// token to a process running as somebody else. systemd stages it once, at
// start, so without renewal the token would either be long-lived — the thing
// this replaces — or the deployment would have to be restarted every hour.
//
// The deployment renews with the token it is holding. Nothing else is needed on
// the node, nothing long-lived is stored, and a deployment whose grants have
// been taken away renews into a token that reaches nothing.

// RenewHandler serves POST /v1/auth/renew.
func (h *Handlers) RenewHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	claims := jwtClaimsFromContext(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized,
			"renewing needs the token being renewed: send it as Authorization: Bearer <token>")
		return
	}
	if !authsvc.IsWorkloadSubject(claims.Sub) {
		// A user's session is renewed by the refresh token, which rotates and
		// can be revoked. Letting any token mint its own successor would make
		// a stolen access token good for ever.
		writeError(w, http.StatusForbidden,
			"only a workload token renews itself; a user session is renewed at /v1/auth/refresh")
		return
	}

	token, expiresAt, err := h.authService.RenewWorkloadToken(r.Context(), claims)
	if err != nil {
		writeError(w, http.StatusForbidden, "this token cannot be renewed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(time.Until(expiresAt).Seconds()),
		"namespace":    claims.Namespace,
		"subject":      claims.Sub,
	})
}

// jwtClaimsFromContext returns the verified claims the auth middleware put on
// the request, or nil when it was not token-authenticated.
func jwtClaimsFromContext(r *http.Request) *authsvc.JWTClaims {
	claims, _ := r.Context().Value(CtxKeyJWT).(*authsvc.JWTClaims)
	return claims
}
