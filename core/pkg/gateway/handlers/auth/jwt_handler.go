package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
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

	ctx := r.Context()

	// Resolve the caller's namespace and effective scopes.
	//
	// The auth middleware has ALREADY validated this API key against the MAIN
	// cluster RQLite — either directly (on the main gateway) or, when this
	// handler runs on a NAMESPACE gateway, via the trusted X-Internal-Auth-*
	// headers the main gateway sets after validating and before proxying. It
	// stores the resolved namespace + scope set on the request context.
	//
	// We MUST trust that context here rather than re-query the key. Namespace
	// gateways have an ISOLATED RQLite whose api_keys table is EMPTY (API keys
	// live in the main cluster RQLite only). This endpoint was the one handler
	// that bypassed the internal-auth context and self-queried the local DB, so
	// `POST /v1/auth/token` returned "invalid API key" for EVERY real key on a
	// namespace gateway — even though the same key authenticated fine on every
	// other path (bugboard #147/#148 two-gateway regression).
	ns := ""
	if v, ok := ctx.Value(ctxkeys.NamespaceOverride).(string); ok {
		ns = strings.TrimSpace(v)
	}
	scopesCanonical := ""
	if s, ok := ctx.Value(ctxkeys.Scopes).(authsvc.ScopeSet); ok {
		scopesCanonical = s.Canonical()
	}

	// Fallback: no pre-validated identity on the context (e.g. the handler was
	// reached on the main gateway without the middleware resolving a namespace,
	// or in a direct unit-test invocation). Resolve against the DB. API keys are
	// stored HMAC-hashed (Service.HashAPIKey), so try the hashed key first and
	// fall back to the raw key for legacy unhashed rows during a rolling upgrade.
	// Revoked keys (revoked_at IS NOT NULL) resolve to invalid. The minted JWT
	// carries the SAME scope set as the key so a narrow runtime key cannot
	// exchange for a JWT and escalate to admin (bugboard #148).
	if ns == "" {
		db := h.netClient.Database()
		internalCtx := h.internalAuthFn(ctx)
		const q = "SELECT namespaces.name, api_keys.scopes FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? AND api_keys.revoked_at IS NULL LIMIT 1"
		rawScopes := ""
		for _, candidate := range apiKeyLookupCandidates(key, h.authService.HashAPIKey(key)) {
			res, err := db.Query(internalCtx, q, candidate)
			if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 {
				continue
			}
			row, ok := res.Rows[0].([]interface{})
			if !ok || len(row) == 0 {
				continue
			}
			if s, ok := row[0].(string); ok && s != "" {
				ns = s
				if len(row) > 1 {
					if sc, ok := row[1].(string); ok {
						rawScopes = sc
					}
				}
				break
			}
		}
		// Embed the key's effective scopes (grandfather NULL→admin).
		scopesCanonical = authsvc.ScopesFromStored(rawScopes).Canonical()
	}

	if ns == "" {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	// Embed the key's effective scopes so the gateway scope gate enforces them on
	// the exchanged JWT exactly as on the raw key (bugboard #148).
	custom := map[string]string{"scopes": scopesCanonical}
	token, expUnix, err := h.authService.GenerateJWT(ns, key, 15*time.Minute, custom)
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

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	// Feature #68 / RFC 9700 §4.12: refresh-token rotation.
	// Every successful refresh mints a NEW refresh token and revokes the
	// supplied one atomically. The response carries the rotated value;
	// the SDK persists it (bug #239 fix) and uses it on the next refresh.
	token, newRefreshToken, subject, expUnix, err := h.authService.RefreshToken(r.Context(), req.RefreshToken, req.Namespace)
	if err != nil {
		// Bugboard #125: a TRANSIENT rotation failure (rqlite leader briefly
		// unavailable during a rolling restart) must surface as a retryable
		// 503 — NOT a 401 — so the client retries within the call-ring window
		// instead of tearing the session down to a full SIWE re-auth, which is
		// impossible on a locked device answering a VoIP-woken call.
		if errors.Is(err, authsvc.ErrRefreshTransient) || errors.Is(err, authsvc.ErrRotationNotConfigured) {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "refresh temporarily unavailable, retry")
			return
		}
		// Genuine bad/expired/replayed token. The service emits a WARN log on
		// replay (ErrRefreshTokenReplay) so the operator can investigate. We
		// surface a generic 401 regardless — leaking "your token was already
		// used" would help an attacker confirm a stolen token was rotated.
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  token,
		"token_type":    "Bearer",
		"expires_in":    int(expUnix - time.Now().Unix()),
		"refresh_token": newRefreshToken,
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

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
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

// apiKeyLookupCandidates returns the api_keys.key values to try, hashed first
// (new keys are stored HMAC-hashed) then the raw key as a rolling-upgrade
// fallback for legacy unhashed rows. The raw fallback is skipped when hashing
// is a no-op (hashedKey == rawKey) so we never issue a duplicate query.
func apiKeyLookupCandidates(rawKey, hashedKey string) []string {
	if hashedKey == rawKey {
		return []string{rawKey}
	}
	return []string{hashedKey, rawKey}
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
