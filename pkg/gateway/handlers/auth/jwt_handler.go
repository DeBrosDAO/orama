package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// APIKeyToJWTHandler issues a short-lived JWT from a valid API key.
// This allows API key holders to obtain JWT tokens for use with the gateway.
//
// POST /v1/auth/token
// Requires: Authorization header with API key (Bearer, ApiKey, or X-API-Key header)
// Response: { "access_token", "token_type", "expires_in", "namespace" }
func (h *Handlers) APIKeyToJWTHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	key := extractAPIKey(r)
	if strings.TrimSpace(key) == "" {
		writeError(w, http.StatusUnauthorized, "missing API key")
		return
	}

	// Validate and get namespace
	db := h.netClient.Database()
	ctx := r.Context()
	internalCtx := h.internalAuthFn(ctx)
	q := "SELECT namespaces.name FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? LIMIT 1"
	res, err := db.Query(internalCtx, q, key)
	if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	// Extract namespace from first row
	row, ok := res.Rows[0].([]interface{})
	if !ok || len(row) == 0 {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	var ns string
	if s, ok := row[0].(string); ok {
		ns = s
	} else {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	token, expUnix, err := h.authService.GenerateJWT(ns, key, 15*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(expUnix - time.Now().Unix()),
		"namespace":    ns,
	})
}

// RefreshHandler refreshes an access token using a refresh token.
//
// POST /v1/auth/refresh
// Request body: RefreshRequest
// Response: { "access_token", "token_type", "expires_in", "refresh_token", "subject", "namespace" }
func (h *Handlers) RefreshHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	token, subject, expUnix, err := h.authService.RefreshToken(r.Context(), req.RefreshToken, req.Namespace)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    int(expUnix - time.Now().Unix()),
		"refresh_token": req.RefreshToken,
		"subject":       subject,
		"namespace":     req.Namespace,
	})
}

// LogoutHandler revokes refresh tokens.
// If a refresh_token is provided, it will be revoked.
// If all=true is provided (and the request is authenticated via JWT),
// all tokens for the JWT subject within the namespace are revoked.
//
// POST /v1/auth/logout
// Request body: LogoutRequest
// Response: { "status": "ok" }
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req LogoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	ctx := r.Context()
	var subject string
	if req.All {
		if v := ctx.Value(CtxKeyJWT); v != nil {
			if claims, ok := v.(*authsvc.JWTClaims); ok && claims != nil {
				subject = strings.TrimSpace(claims.Sub)
			}
		}
		if subject == "" {
			writeError(w, http.StatusUnauthorized, "jwt required for all=true")
			return
		}
	}

	if err := h.authService.RevokeToken(ctx, req.Namespace, req.RefreshToken, req.All, subject); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// extractAPIKey extracts API key from Authorization, X-API-Key header, or query parameters
func extractAPIKey(r *http.Request) string {
	// Prefer X-API-Key header (most explicit)
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}

	// Check Authorization header for ApiKey scheme or non-JWT Bearer tokens
	auth := r.Header.Get("Authorization")
	if auth != "" {
		lower := strings.ToLower(auth)
		if strings.HasPrefix(lower, "bearer ") {
			tok := strings.TrimSpace(auth[len("Bearer "):])
			// Skip Bearer tokens that look like JWTs (have 2 dots)
			if strings.Count(tok, ".") != 2 {
				return tok
			}
		} else if strings.HasPrefix(lower, "apikey ") {
			return strings.TrimSpace(auth[len("ApiKey "):])
		} else if !strings.Contains(auth, " ") {
			// If header has no scheme, treat the whole value as token
			tok := strings.TrimSpace(auth)
			if strings.Count(tok, ".") != 2 {
				return tok
			}
		}
	}

	// Fallback to query parameter
	if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}
	return ""
}
