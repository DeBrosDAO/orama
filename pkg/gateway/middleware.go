package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// context keys for request-scoped auth metadata (private to package)
type contextKey string

const (
	ctxKeyAPIKey            contextKey = "api_key"
	ctxKeyJWT               contextKey = "jwt_claims"
	ctxKeyNamespaceOverride contextKey = "namespace_override"
)

// withMiddleware adds CORS and logging middleware
func (g *Gateway) withMiddleware(next http.Handler) http.Handler {
	// Order: logging (outermost) -> CORS -> auth -> handler
	// Add authorization layer after auth to enforce namespace ownership
	return g.loggingMiddleware(g.corsMiddleware(g.authMiddleware(g.authorizationMiddleware(next))))
}

// loggingMiddleware logs basic request info and duration
func (g *Gateway) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		srw := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(srw, r)
		dur := time.Since(start)
		g.logger.ComponentInfo(logging.ComponentGeneral, "request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.Int("status", srw.status),
			zap.Int("bytes", srw.bytes),
			zap.String("duration", dur.String()),
		)

		// Persist request log asynchronously (best-effort)
		go g.persistRequestLog(r, srw, dur)
	})
}

// authMiddleware enforces auth when enabled via config.
// Accepts:
//   - Authorization: Bearer <JWT> (RS256 issued by this gateway)
//   - Authorization: Bearer <API key> or ApiKey <API key>
//   - X-API-Key: <API key>
func (g *Gateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow preflight without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		isPublic := isPublicPath(r.URL.Path)

		// 1) Try JWT Bearer first if Authorization looks like one
		if auth := r.Header.Get("Authorization"); auth != "" {
			lower := strings.ToLower(auth)
			if strings.HasPrefix(lower, "bearer ") {
				tok := strings.TrimSpace(auth[len("Bearer "):])
				if strings.Count(tok, ".") == 2 {
					if claims, err := g.authService.ParseAndVerifyJWT(tok); err == nil {
						// Attach JWT claims and namespace to context
						ctx := context.WithValue(r.Context(), ctxKeyJWT, claims)
						if ns := strings.TrimSpace(claims.Namespace); ns != "" {
							ctx = context.WithValue(ctx, ctxKeyNamespaceOverride, ns)
						}
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					// If it looked like a JWT but failed verification, fall through to API key check
				}
			}
		}

		// 2) Fallback to API key (validate against DB)
		key := extractAPIKey(r)
		if key == "" {
			if isPublic {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer realm=\"gateway\", charset=\"UTF-8\"")
			writeError(w, http.StatusUnauthorized, "missing API key")
			return
		}

		// Look up API key in DB and derive namespace
		db := g.client.Database()
		// Use internal auth for DB validation (auth not established yet)
		internalCtx := client.WithInternalAuth(r.Context())
		// Join to namespaces to resolve name in one query
		q := "SELECT namespaces.name FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? LIMIT 1"
		res, err := db.Query(internalCtx, q, key)
		if err != nil || res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			if isPublic {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer error=\"invalid_token\"")
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}
		// Extract namespace name
		var ns string
		if s, ok := res.Rows[0][0].(string); ok {
			ns = strings.TrimSpace(s)
		} else {
			b, _ := json.Marshal(res.Rows[0][0])
			_ = json.Unmarshal(b, &ns)
			ns = strings.TrimSpace(ns)
		}
		if ns == "" {
			if isPublic {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", "Bearer error=\"invalid_token\"")
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		// Attach auth metadata to context for downstream use
		reqCtx := context.WithValue(r.Context(), ctxKeyAPIKey, key)
		reqCtx = context.WithValue(reqCtx, ctxKeyNamespaceOverride, ns)
		next.ServeHTTP(w, r.WithContext(reqCtx))
	})
}

// extractAPIKey extracts API key from Authorization, X-API-Key header, or query parameters
// Note: Bearer tokens that look like JWTs (have 2 dots) are skipped (they're JWTs, handled separately)
// X-API-Key header is preferred when both Authorization and X-API-Key are present
func extractAPIKey(r *http.Request) string {
	// Prefer X-API-Key header (most explicit) - check this first
	if v := strings.TrimSpace(r.Header.Get("X-API-Key")); v != "" {
		return v
	}

	// Check Authorization header for ApiKey scheme or non-JWT Bearer tokens
	auth := r.Header.Get("Authorization")
	if auth != "" {
		lower := strings.ToLower(auth)
		if strings.HasPrefix(lower, "bearer ") {
			tok := strings.TrimSpace(auth[len("Bearer "):])
			// Skip Bearer tokens that look like JWTs (have 2 dots) - they're JWTs
			// But allow Bearer tokens that don't look like JWTs (for backward compatibility)
			if strings.Count(tok, ".") == 2 {
				// This is a JWT, skip it
			} else {
				// This doesn't look like a JWT, treat as API key (backward compatibility)
				return tok
			}
		} else if strings.HasPrefix(lower, "apikey ") {
			return strings.TrimSpace(auth[len("ApiKey "):])
		} else if !strings.Contains(auth, " ") {
			// If header has no scheme, treat the whole value as token (lenient for dev)
			// But skip if it looks like a JWT (has 2 dots)
			tok := strings.TrimSpace(auth)
			if strings.Count(tok, ".") != 2 {
				return tok
			}
		}
	}

	// Fallback to query parameter (for WebSocket support)
	if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
		return v
	}
	// Also check token query parameter (alternative name)
	if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
		return v
	}
	return ""
}

// isPublicPath returns true for routes that should be accessible without API key auth
func isPublicPath(p string) bool {
	// Allow ACME challenges for Let's Encrypt certificate provisioning
	if strings.HasPrefix(p, "/.well-known/acme-challenge/") {
		return true
	}

	// Serverless invocation is public (authorization is handled within the invoker)
	if strings.HasPrefix(p, "/v1/invoke/") || (strings.HasPrefix(p, "/v1/functions/") && strings.HasSuffix(p, "/invoke")) {
		return true
	}

	switch p {
	case "/health", "/v1/health", "/status", "/v1/status", "/v1/auth/jwks", "/.well-known/jwks.json", "/v1/version", "/v1/auth/login", "/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/register", "/v1/auth/refresh", "/v1/auth/logout", "/v1/auth/api-key", "/v1/auth/simple-key", "/v1/network/status", "/v1/network/peers":
		return true
	default:
		return false
	}
}

// authorizationMiddleware enforces that the authenticated actor owns the namespace
// for certain protected paths (e.g., apps CRUD and storage APIs).
func (g *Gateway) authorizationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip for public/OPTIONS paths only
		if r.Method == http.MethodOptions || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Exempt whoami from ownership enforcement so users can inspect their session
		if r.URL.Path == "/v1/auth/whoami" {
			next.ServeHTTP(w, r)
			return
		}

		// Only enforce for specific resource paths
		if !requiresNamespaceOwnership(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Determine namespace from context
		ctx := r.Context()
		ns := ""
		if v := ctx.Value(ctxKeyNamespaceOverride); v != nil {
			if s, ok := v.(string); ok {
				ns = strings.TrimSpace(s)
			}
		}
		if ns == "" && g.cfg != nil {
			ns = strings.TrimSpace(g.cfg.ClientNamespace)
		}
		if ns == "" {
			writeError(w, http.StatusForbidden, "namespace not resolved")
			return
		}

		// Identify actor from context
		ownerType := ""
		ownerID := ""
		apiKeyFallback := ""

		if v := ctx.Value(ctxKeyJWT); v != nil {
			if claims, ok := v.(*auth.JWTClaims); ok && claims != nil && strings.TrimSpace(claims.Sub) != "" {
				// Determine subject type.
				// If subject looks like an API key (e.g., ak_<random>:<namespace>),
				// treat it as an API key owner; otherwise assume a wallet subject.
				subj := strings.TrimSpace(claims.Sub)
				lowerSubj := strings.ToLower(subj)
				if strings.HasPrefix(lowerSubj, "ak_") || strings.Contains(subj, ":") {
					ownerType = "api_key"
					ownerID = subj
				} else {
					ownerType = "wallet"
					ownerID = subj
				}
			}
		}
		if ownerType == "" && ownerID == "" {
			if v := ctx.Value(ctxKeyAPIKey); v != nil {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					ownerType = "api_key"
					ownerID = strings.TrimSpace(s)
				}
			}
		} else if ownerType == "wallet" {
			// If we have a JWT wallet, also capture the API key as fallback
			if v := ctx.Value(ctxKeyAPIKey); v != nil {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					apiKeyFallback = strings.TrimSpace(s)
				}
			}
		}

		if ownerType == "" || ownerID == "" {
			writeError(w, http.StatusForbidden, "missing identity")
			return
		}

		g.logger.ComponentInfo("gateway", "namespace auth check",
			zap.String("namespace", ns),
			zap.String("owner_type", ownerType),
			zap.String("owner_id", ownerID),
		)

		// Check ownership in DB using internal auth context
		db := g.client.Database()
		internalCtx := client.WithInternalAuth(ctx)
		// Ensure namespace exists and get id
		if _, err := db.Query(internalCtx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", ns); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nres, err := db.Query(internalCtx, "SELECT id FROM namespaces WHERE name = ? LIMIT 1", ns)
		if err != nil || nres == nil || nres.Count == 0 || len(nres.Rows) == 0 || len(nres.Rows[0]) == 0 {
			writeError(w, http.StatusForbidden, "namespace not found")
			return
		}
		nsID := nres.Rows[0][0]

		q := "SELECT 1 FROM namespace_ownership WHERE namespace_id = ? AND owner_type = ? AND owner_id = ? LIMIT 1"
		res, err := db.Query(internalCtx, q, nsID, ownerType, ownerID)

		// If primary owner check fails and we have a JWT wallet with API key fallback, try the API key
		if (err != nil || res == nil || res.Count == 0) && ownerType == "wallet" && apiKeyFallback != "" {
			res, err = db.Query(internalCtx, q, nsID, "api_key", apiKeyFallback)
		}

		if err != nil || res == nil || res.Count == 0 {
			writeError(w, http.StatusForbidden, "forbidden: not an owner of namespace")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requiresNamespaceOwnership returns true if the path should be guarded by
// namespace ownership checks.
func requiresNamespaceOwnership(p string) bool {
	if p == "/rqlite" || p == "/v1/rqlite" || strings.HasPrefix(p, "/v1/rqlite/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/pubsub") {
		return true
	}
	if strings.HasPrefix(p, "/v1/rqlite/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/proxy/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/functions") {
		return true
	}
	return false
}

// corsMiddleware applies permissive CORS headers suitable for early development
func (g *Gateway) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(600))
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// persistRequestLog writes request metadata to the database (best-effort)
func (g *Gateway) persistRequestLog(r *http.Request, srw *statusResponseWriter, dur time.Duration) {
	if g.client == nil {
		return
	}
	// Use a short timeout to avoid blocking shutdowns
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	db := g.client.Database()

	// Resolve API key ID if available
	var apiKeyID interface{} = nil
	if v := r.Context().Value(ctxKeyAPIKey); v != nil {
		if key, ok := v.(string); ok && key != "" {
			if res, err := db.Query(ctx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", key); err == nil {
				if res != nil && res.Count > 0 && len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
					switch idv := res.Rows[0][0].(type) {
					case int64:
						apiKeyID = idv
					case float64:
						apiKeyID = int64(idv)
					case int:
						apiKeyID = int64(idv)
					case string:
						// best effort parse
						if n, err := strconv.Atoi(idv); err == nil {
							apiKeyID = int64(n)
						}
					}
				}
			}
		}
	}

	ip := getClientIP(r)

	// Insert the log row
	_, _ = db.Query(ctx,
		"INSERT INTO request_logs (method, path, status_code, bytes_out, duration_ms, ip, api_key_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
		r.Method,
		r.URL.Path,
		srw.status,
		srw.bytes,
		dur.Milliseconds(),
		ip,
		apiKeyID,
	)

	// Update last_used_at for the API key if present
	if apiKeyID != nil {
		_, _ = db.Query(ctx, "UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?", apiKeyID)
	}
}

// getClientIP extracts the client IP from headers or RemoteAddr
func getClientIP(r *http.Request) string {
	// X-Forwarded-For may contain a list of IPs, take the first
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
