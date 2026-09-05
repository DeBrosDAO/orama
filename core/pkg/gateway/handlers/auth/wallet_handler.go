package auth

import (
	"net/http"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// WhoamiHandler returns the authenticated user's identity and method.
// This endpoint shows whether the request is authenticated via JWT or API key,
// and provides details about the authenticated principal.
//
// GET /v1/auth/whoami
// Response: { "authenticated", "method", "subject", "namespace", ... }
func (h *Handlers) WhoamiHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Determine namespace (may be overridden by auth layer)
	ns := h.defaultNS
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}

	// Prefer JWT if present
	if v := ctx.Value(CtxKeyJWT); v != nil {
		if claims, ok := v.(*authsvc.JWTClaims); ok && claims != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"method":        "jwt",
				"subject":       claims.Sub,
				"issuer":        claims.Iss,
				"audience":      claims.Aud,
				"issued_at":     claims.Iat,
				"not_before":    claims.Nbf,
				"expires_at":    claims.Exp,
				"namespace":     ns,
			})
			return
		}
	}

	// Fallback: API key identity
	var key string
	if v := ctx.Value(CtxKeyAPIKey); v != nil {
		if s, ok := v.(string); ok {
			key = s
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": key != "",
		"method":        "api_key",
		"api_key":       key,
		"namespace":     ns,
	})
}
