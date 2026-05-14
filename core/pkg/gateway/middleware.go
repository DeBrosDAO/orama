package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// isLongRunningProxyPath returns true for paths whose expected work bound
// exceeds the default 30s proxy timeout. New classes go here, alphabetically.
//
//   - /v1/functions/.../invoke and /v1/invoke/...        — up to 300s per fn
//   - /v1/functions/.../ws                                — long-lived WS
//   - /v1/storage/upload, /v1/storage/pin                 — IPFS add can be slow
//
// Adding a path here is preferable to bumping the global timeout: each
// path's bound is documented in one grep-able place.
func isLongRunningProxyPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/v1/storage/upload"),
		strings.HasPrefix(p, "/v1/storage/pin"),
		strings.HasPrefix(p, "/v1/invoke/"),
		strings.HasPrefix(p, "/v1/functions/") && (strings.HasSuffix(p, "/invoke") || strings.HasSuffix(p, "/ws")):
		return true
	}
	return false
}

// isProxyTimeoutErr returns true when an HTTP client error is a timeout —
// either the http.Client overall timeout or context.DeadlineExceeded
// propagating from a parent context.
func isProxyTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	return false
}

// Note: context keys (ctxKeyAPIKey, ctxKeyJWT, CtxKeyNamespaceOverride) are now defined in context.go

// Internal auth headers for trusted inter-gateway communication.
// When the main gateway proxies to a namespace gateway, it validates auth first
// and passes the validated namespace via these headers. The namespace gateway
// trusts these headers when they come from internal IPs (WireGuard 10.0.0.x).
const (
	// HeaderInternalAuthNamespace contains the validated namespace name
	HeaderInternalAuthNamespace = "X-Internal-Auth-Namespace"
	// HeaderInternalAuthValidated indicates the request was pre-authenticated by main gateway
	HeaderInternalAuthValidated = "X-Internal-Auth-Validated"
	// HeaderInternalAuthJWTSub carries the validated JWT `sub` claim across the
	// internal proxy hop. Bug #215: without this, namespace gateways received
	// only the namespace and host functions saw empty `caller_jwt_subject`.
	// Trusted only when X-Internal-Auth-Validated=true AND the source IP is
	// internal (WireGuard mesh / loopback) — same trust model as the
	// namespace header.
	HeaderInternalAuthJWTSub = "X-Internal-Auth-JWT-Sub"
	// HeaderInternalAuthJWTCustom carries the validated JWT custom-claims map
	// as a base64(JSON) blob. Optional; absent means the original token had
	// no custom claims. Same trust gate as HeaderInternalAuthJWTSub.
	HeaderInternalAuthJWTCustom = "X-Internal-Auth-JWT-Custom"
)

// setInternalAuthJWTHeaders writes the validated JWT subject and custom claims
// onto an outbound proxy request so the destination gateway can hydrate
// ctxKeyJWT. Called only after auth has been validated AND we're proxying over
// the WireGuard mesh (the destination still gates trust by source IP).
//
// No-op when claims is nil (caller authenticated with API key, not JWT).
func setInternalAuthJWTHeaders(h http.Header, claims *auth.JWTClaims) {
	if claims == nil {
		return
	}
	if sub := strings.TrimSpace(claims.Sub); sub != "" {
		h.Set(HeaderInternalAuthJWTSub, sub)
	}
	if len(claims.Custom) > 0 {
		buf, err := json.Marshal(claims.Custom)
		if err == nil {
			h.Set(HeaderInternalAuthJWTCustom, base64.StdEncoding.EncodeToString(buf))
		}
	}
}

// stripInboundInternalAuthHeaders deletes the X-Internal-Auth-* headers from
// the supplied header set. MUST be called on every outbound request the main
// gateway proxies to a namespace gateway BEFORE we (conditionally) re-set
// them with the validated values.
//
// SECURITY (bug #215 follow-up — critical): without this, an external client
// could send `X-Internal-Auth-Validated: true` + `X-Internal-Auth-JWT-Sub:
// <victim-wallet>` directly. The header-copy loop at the proxy hop would
// forward those headers verbatim. The namespace gateway, seeing the request
// arrive from the main gateway's WireGuard IP, would trust them and hydrate
// ctxKeyJWT with attacker-controlled subject — full cross-namespace identity
// spoofing on every public path.
//
// Stripping FIRST and conditionally setting AFTER closes that hole regardless
// of which auth path won (JWT, API key, or no creds on a public path).
func stripInboundInternalAuthHeaders(h http.Header) {
	h.Del(HeaderInternalAuthValidated)
	h.Del(HeaderInternalAuthNamespace)
	h.Del(HeaderInternalAuthJWTSub)
	h.Del(HeaderInternalAuthJWTCustom)
}

// maxQueryJWTLength caps the size of a JWT accepted via `?jwt=` query
// param. EdDSA + RS256 JWTs minted by this gateway are well under 2 KB;
// 4 KB is a generous ceiling that still cheaply rejects DoS attempts
// that try to feed multi-MB tokens through the verifier.
const maxQueryJWTLength = 4096

// stripJWTQueryParam removes the `jwt` key from the URL's query string
// (if present), mutating r in place. Called after a successful WS-upgrade
// JWT-via-query verification so the token doesn't propagate to:
//   - the namespace-gateway proxy hop (`r.URL.RawQuery` is forwarded)
//   - downstream handler logs that record `r.URL.RequestURI()`
//   - any inner `r.URL.Query()` lookups in business logic
//
// Idempotent: safe to call on requests without a `jwt` param.
func stripJWTQueryParam(r *http.Request) {
	q := r.URL.Query()
	if !q.Has("jwt") {
		return
	}
	q.Del("jwt")
	r.URL.RawQuery = q.Encode()
}

// claimsFromInternalAuthHeaders rebuilds a *auth.JWTClaims from the trusted
// internal-auth headers. Returns nil if no JWT subject was forwarded (the
// caller used an API key, or the request didn't carry validated JWT data).
//
// SECURITY: this MUST only be called after the caller has confirmed
// HeaderInternalAuthValidated == "true" AND the source IP is internal AND
// the proxy hop has stripped any inbound copies of these headers (see
// stripInboundInternalAuthHeaders). Skipping any of those would let any
// client forge JWT identities.
func claimsFromInternalAuthHeaders(h http.Header, namespace string) *auth.JWTClaims {
	sub := strings.TrimSpace(h.Get(HeaderInternalAuthJWTSub))
	if sub == "" {
		return nil
	}
	claims := &auth.JWTClaims{
		Sub:       sub,
		Namespace: namespace,
	}
	if raw := strings.TrimSpace(h.Get(HeaderInternalAuthJWTCustom)); raw != "" {
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
			var custom map[string]string
			if json.Unmarshal(decoded, &custom) == nil && len(custom) > 0 {
				claims.Custom = custom
			}
		}
	}
	return claims
}

// validateAuthForNamespaceProxy validates the request's auth credentials against the MAIN
// cluster RQLite and returns the namespace the credentials belong to plus the
// JWT claims when the caller authenticated with a Bearer JWT.
// This is used by handleNamespaceGatewayRequest to pre-authenticate before proxying to
// namespace gateways (which have isolated RQLites without API keys).
//
// Returns:
//   - (namespace, claims, "") if auth is valid via JWT — claims may be used to
//     populate internal-auth headers so the namespace gateway can hydrate
//     `caller_jwt_subject` for serverless host functions (bug #215).
//   - (namespace, nil, "") if auth is valid via API key.
//   - ("", nil, errorMessage) if auth is invalid.
//   - ("", nil, "") if no auth credentials provided (for public paths).
func (g *Gateway) validateAuthForNamespaceProxy(r *http.Request) (namespace string, claims *auth.JWTClaims, errMsg string) {
	// 1) Try JWT Bearer first
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		lower := strings.ToLower(authHeader)
		if strings.HasPrefix(lower, "bearer ") {
			tok := strings.TrimSpace(authHeader[len("Bearer "):])
			if strings.Count(tok, ".") == 2 {
				if c, err := g.authService.ParseAndVerifyJWT(tok); err == nil {
					if ns := strings.TrimSpace(c.Namespace); ns != "" {
						return ns, c, ""
					}
				}
				// JWT verification failed - fall through to API key check
			}
		}
	}

	// 1b) WS upgrade fallback: JWT via `?jwt=` query. Same rationale as in
	// authMiddleware — browser / React Native WS clients can't set custom
	// headers reliably. Bug #240. Strip-after-verify is applied here too
	// so the JWT doesn't propagate to the namespace gateway over the proxy
	// hop (where it would otherwise live in the proxied request's RawQuery
	// + the inner gateway's logs).
	if isWebSocketUpgrade(r) {
		tok := strings.TrimSpace(r.URL.Query().Get("jwt"))
		if tok != "" && len(tok) <= maxQueryJWTLength && strings.Count(tok, ".") == 2 {
			if c, err := g.authService.ParseAndVerifyJWT(tok); err == nil {
				if ns := strings.TrimSpace(c.Namespace); ns != "" {
					stripJWTQueryParam(r)
					return ns, c, ""
				}
			}
		}
	}

	// 2) Try API key
	key := extractAPIKey(r)
	if key == "" {
		return "", nil, "" // No credentials provided
	}

	ns, err := g.lookupAPIKeyNamespace(r.Context(), key, g.client)
	if err != nil {
		return "", nil, "invalid API key"
	}
	return ns, nil, ""
}

// lookupAPIKeyNamespace resolves an API key to its namespace using cache and DB.
// dbClient controls which database is queried (global vs namespace-specific).
// Returns the namespace name or an error if the key is invalid.
//
// Dual lookup strategy for rolling upgrade: tries HMAC-hashed key first (new keys),
// then falls back to raw key lookup (existing unhashed keys during transition).
func (g *Gateway) lookupAPIKeyNamespace(ctx context.Context, key string, dbClient client.NetworkClient) (string, error) {
	// Cache uses raw key as cache key (in-memory only, never persisted)
	if g.mwCache != nil {
		if cachedNS, ok := g.mwCache.GetAPIKeyNamespace(key); ok {
			return cachedNS, nil
		}
	}

	db := dbClient.Database()
	internalCtx := client.WithInternalAuth(ctx)
	q := "SELECT namespaces.name FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? LIMIT 1"

	// Try HMAC-hashed lookup first (new keys stored as hashes)
	hashedKey := g.authService.HashAPIKey(key)
	res, err := db.Query(internalCtx, q, hashedKey)
	if err == nil && res != nil && res.Count > 0 && len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		if ns := getString(res.Rows[0][0]); ns != "" {
			if g.mwCache != nil {
				g.mwCache.SetAPIKeyNamespace(key, ns)
			}
			return ns, nil
		}
	}

	// Fallback: try raw key lookup (existing unhashed keys during rolling upgrade)
	if hashedKey != key {
		res, err = db.Query(internalCtx, q, key)
		if err == nil && res != nil && res.Count > 0 && len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
			if ns := getString(res.Rows[0][0]); ns != "" {
				if g.mwCache != nil {
					g.mwCache.SetAPIKeyNamespace(key, ns)
				}
				return ns, nil
			}
		}
	}

	return "", fmt.Errorf("invalid API key")
}

// isWebSocketUpgrade checks if the request is a WebSocket upgrade request
func isWebSocketUpgrade(r *http.Request) bool {
	connection := strings.ToLower(r.Header.Get("Connection"))
	upgrade := strings.ToLower(r.Header.Get("Upgrade"))
	return strings.Contains(connection, "upgrade") && upgrade == "websocket"
}

// proxyWebSocket proxies a WebSocket connection by hijacking the client connection
// and tunneling bidirectionally to the backend
func (g *Gateway) proxyWebSocket(w http.ResponseWriter, r *http.Request, targetHost string) bool {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket proxy not supported", http.StatusInternalServerError)
		return false
	}

	// Connect to backend
	backendConn, err := net.DialTimeout("tcp", targetHost, 10*time.Second)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "WebSocket backend dial failed",
			zap.String("target", targetHost),
			zap.Error(err),
		)
		http.Error(w, "Backend unavailable", http.StatusServiceUnavailable)
		return false
	}

	// Write the original request to backend (this initiates the WebSocket handshake)
	if err := r.Write(backendConn); err != nil {
		backendConn.Close()
		g.logger.ComponentError(logging.ComponentGeneral, "WebSocket handshake write failed",
			zap.Error(err),
		)
		http.Error(w, "Failed to initiate WebSocket", http.StatusBadGateway)
		return false
	}

	// Hijack client connection
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		backendConn.Close()
		g.logger.ComponentError(logging.ComponentGeneral, "WebSocket hijack failed",
			zap.Error(err),
		)
		return false
	}

	// Flush any buffered data from the client
	if clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		clientBuf.Read(buffered)
		backendConn.Write(buffered)
	}

	// Bidirectional copy between client and backend
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(clientConn, backendConn)
		clientConn.Close()
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		io.Copy(backendConn, clientConn)
		backendConn.Close()
	}()

	// Wait for one side to close
	<-done
	clientConn.Close()
	backendConn.Close()
	<-done

	return true
}

// withMiddleware adds CORS, security headers, rate limiting, and logging middleware
func (g *Gateway) withMiddleware(next http.Handler) http.Handler {
	// Order: logging -> security headers -> rate limit -> CORS -> domain routing -> auth -> authorization -> namespace rate limit -> handler
	return g.loggingMiddleware(
		g.securityHeadersMiddleware(
			g.rateLimitMiddleware(
				g.corsMiddleware(
					g.domainRoutingMiddleware(
						g.authMiddleware(
							g.authorizationMiddleware(
								g.namespaceRateLimitMiddleware(next))))))))
}

// securityHeadersMiddleware adds standard security headers to all responses
func (g *Gateway) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "0")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=(self), geolocation=()")
		// HSTS only when behind TLS (Caddy)
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
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

		// Enqueue log entry for batched persistence (replaces per-request DB writes)
		if g.logBatcher != nil {
			apiKey := ""
			if v := r.Context().Value(ctxKeyAPIKey); v != nil {
				if s, ok := v.(string); ok {
					apiKey = s
				}
			}
			g.logBatcher.Add(requestLogEntry{
				method:     r.Method,
				path:       r.URL.Path,
				statusCode: srw.status,
				bytesOut:   srw.bytes,
				durationMs: dur.Milliseconds(),
				ip:         getClientIP(r),
				apiKey:     apiKey,
			})
		}
	})
}

// authMiddleware enforces auth when enabled via config.
// Accepts:
//   - Authorization: Bearer <JWT> (RS256 / EdDSA issued by this gateway)
//   - Authorization: Bearer <API key> or ApiKey <API key>
//   - X-API-Key: <API key>
//   - ?api_key=<key> or ?token=<key> query string (WebSocket upgrade only)
//   - ?jwt=<token> query string (WebSocket upgrade only — bug #240; needed
//     because browser/RN WS clients can't reliably set custom headers)
//   - X-Internal-Auth-Validated: true (from internal IPs only - pre-authenticated by main gateway)
func (g *Gateway) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow preflight without auth
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		isPublic := isPublicPath(r.URL.Path)

		// 0) Trust internal auth headers from internal IPs (WireGuard network or localhost)
		// This allows the main gateway to pre-authenticate requests before proxying to namespace gateways
		// IMPORTANT: Use r.RemoteAddr (actual TCP peer), NOT getClientIP() which reads
		// X-Forwarded-For and would return the original client IP instead of the proxy's IP.
		if r.Header.Get(HeaderInternalAuthValidated) == "true" {
			clientIP := remoteAddrIP(r)
			if isInternalIP(clientIP) {
				ns := strings.TrimSpace(r.Header.Get(HeaderInternalAuthNamespace))
				if ns != "" {
					// Pre-authenticated by main gateway - trust the namespace
					reqCtx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, ns)
					// Bug #215: also rebuild ctxKeyJWT from the trusted
					// internal-auth headers (subject + custom claims) so
					// serverless host functions see a non-empty
					// caller_jwt_subject. We deliberately do NOT re-verify
					// the JWT here — namespace gateways may not share the
					// signing key with the main gateway, and the main gateway
					// already verified before forwarding. Trust gate is the
					// source-IP check above.
					if claims := claimsFromInternalAuthHeaders(r.Header, ns); claims != nil {
						reqCtx = context.WithValue(reqCtx, ctxKeyJWT, claims)
					}
					next.ServeHTTP(w, r.WithContext(reqCtx))
					return
				}
			}
			// If internal auth header is present but invalid (wrong IP or missing namespace),
			// fall through to normal auth flow
		}

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
							ctx = context.WithValue(ctx, CtxKeyNamespaceOverride, ns)
						}
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					// If it looked like a JWT but failed verification, fall through to API key check
				}
			}
		}

		// 1b) WebSocket-only fallback: JWT in the `?jwt=` query parameter.
		//
		// Browser and React Native WebSocket clients can't reliably set custom
		// headers on the upgrade request — the WebSocket constructor either
		// ignores the headers argument (browsers) or silently strips
		// Authorization (RN iOS). Without a fallback, every authenticated WS
		// endpoint is unreachable from those platforms. Bug #240.
		//
		// We gate this ONLY on WS upgrade requests to keep JWTs out of normal
		// HTTP URLs (where they end up in access logs, referrer headers, and
		// browser history). For WS, the upgrade URL is only emitted on
		// connection establishment — much smaller exposure surface — and TLS
		// (wss://) keeps it off the wire in transit.
		//
		// After a successful verify, we STRIP the `jwt` query param from the
		// request before passing downstream (`stripJWTQueryParam`). This
		// shrinks the replay window: the token doesn't propagate through the
		// proxy hop to the namespace gateway, doesn't reach the backend
		// handler's logs, and doesn't show up in any downstream `r.URL`
		// inspection. Belt-and-suspenders given the trust we've already
		// established by verifying the signature.
		if isWebSocketUpgrade(r) {
			tok := strings.TrimSpace(r.URL.Query().Get("jwt"))
			// Cheap length sanity-check before invoking the verifier. Real
			// EdDSA / RS256 JWTs issued by this gateway are well under 4 KB.
			// Anything larger is either malformed or a DoS attempt.
			if tok != "" && len(tok) <= maxQueryJWTLength && strings.Count(tok, ".") == 2 {
				if claims, err := g.authService.ParseAndVerifyJWT(tok); err == nil {
					stripJWTQueryParam(r)
					ctx := context.WithValue(r.Context(), ctxKeyJWT, claims)
					if ns := strings.TrimSpace(claims.Namespace); ns != "" {
						ctx = context.WithValue(ctx, CtxKeyNamespaceOverride, ns)
					}
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Invalid JWT in query — fall through to API key check
				// rather than 401-ing here, in case the caller also supplied
				// a valid api_key as belt-and-suspenders.
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

		// Look up API key → namespace (uses cache + DB)
		dbClient := g.client
		if g.authClient != nil {
			dbClient = g.authClient
		}
		ns, err := g.lookupAPIKeyNamespace(r.Context(), key, dbClient)
		if err != nil {
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
		reqCtx = context.WithValue(reqCtx, CtxKeyNamespaceOverride, ns)
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

	// Fallback to query parameter ONLY for WebSocket upgrade requests.
	// WebSocket clients cannot set custom headers, so query params are the
	// only way to authenticate. For regular HTTP requests, require headers.
	if isWebSocketUpgrade(r) {
		if v := strings.TrimSpace(r.URL.Query().Get("api_key")); v != "" {
			return v
		}
		if v := strings.TrimSpace(r.URL.Query().Get("token")); v != "" {
			return v
		}
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

	// Internal replica coordination endpoints (auth handled by replica handler)
	if strings.HasPrefix(p, "/v1/internal/deployments/replica/") {
		return true
	}

	// WireGuard peer exchange (auth handled by cluster secret in handler)
	if strings.HasPrefix(p, "/v1/internal/wg/") {
		return true
	}

	// Node join endpoint (auth handled by invite token in handler)
	if p == "/v1/internal/join" {
		return true
	}

	// Namespace spawn endpoint (auth handled by internal auth header)
	if p == "/v1/internal/namespace/spawn" {
		return true
	}

	// Namespace cluster repair endpoint (auth handled by internal auth header)
	if p == "/v1/internal/namespace/repair" {
		return true
	}

	// Vault proxy endpoints (no auth — rate-limited per identity hash within handler)
	if strings.HasPrefix(p, "/v1/vault/") {
		return true
	}

	// Phantom auth endpoints are public (session creation, status polling, completion)
	if strings.HasPrefix(p, "/v1/auth/phantom/") {
		return true
	}

	switch p {
	case "/health", "/v1/health", "/status", "/v1/status", "/v1/auth/jwks", "/.well-known/jwks.json", "/v1/version", "/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/register", "/v1/auth/refresh", "/v1/auth/logout", "/v1/auth/api-key", "/v1/network/status", "/v1/network/peers", "/v1/internal/tls/check", "/v1/internal/acme/present", "/v1/internal/acme/cleanup", "/v1/internal/ping":
		return true
	default:
		// Also exempt namespace status polling endpoint
		if strings.HasPrefix(p, "/v1/namespace/status") {
			return true
		}
		return false
	}
}

// authorizationMiddleware enforces that the authenticated actor owns the namespace
// for certain protected paths (e.g., apps CRUD and storage APIs).
// Also enforces cross-namespace access control:
// - "default" namespace: accessible by any valid API key
// - Other namespaces: API key must belong to that specific namespace
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

		// Exempt namespace status endpoint
		if strings.HasPrefix(r.URL.Path, "/v1/namespace/status") {
			next.ServeHTTP(w, r)
			return
		}

		// Skip ownership checks for requests pre-authenticated by the main gateway.
		// The main gateway already validated the API key and resolved the namespace
		// before proxying, so re-checking ownership against the namespace RQLite is
		// redundant and adds ~300ms of unnecessary latency (3 DB round-trips).
		if r.Header.Get(HeaderInternalAuthValidated) == "true" {
			clientIP := remoteAddrIP(r)
			if isInternalIP(clientIP) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Cross-namespace access control for namespace gateways
		// The gateway's ClientNamespace determines which namespace this gateway serves
		gatewayNamespace := "default"
		if g.cfg != nil && g.cfg.ClientNamespace != "" {
			gatewayNamespace = strings.TrimSpace(g.cfg.ClientNamespace)
		}

		// Get user's namespace from context (derived from API key/JWT)
		userNamespace := ""
		if v := r.Context().Value(CtxKeyNamespaceOverride); v != nil {
			if s, ok := v.(string); ok {
				userNamespace = strings.TrimSpace(s)
			}
		}

		// For non-default namespace gateways, the API key must belong to this namespace
		// This enforces physical isolation: alice's gateway only accepts alice's API keys
		if gatewayNamespace != "default" && userNamespace != "" && userNamespace != gatewayNamespace {
			g.logger.ComponentWarn(logging.ComponentGeneral, "cross-namespace access denied",
				zap.String("user_namespace", userNamespace),
				zap.String("gateway_namespace", gatewayNamespace),
				zap.String("path", r.URL.Path),
			)
			writeError(w, http.StatusForbidden, "API key does not belong to this namespace")
			return
		}

		// Only enforce ownership for specific resource paths
		if !requiresNamespaceOwnership(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Determine namespace from context
		ctx := r.Context()
		ns := ""
		if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
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
	if strings.HasPrefix(p, "/v1/webrtc/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/push/") {
		return true
	}
	if strings.HasPrefix(p, "/v1/serverless/") {
		return true
	}
	return false
}

// corsMiddleware applies CORS headers. Allows requests from the configured base
// domain and its subdomains. Falls back to permissive "*" only if no base domain
// is configured.
func (g *Gateway) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := g.getAllowedOrigin(origin)
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", strconv.Itoa(600))
		if allowedOrigin != "*" {
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// getAllowedOrigin returns the allowed origin for CORS based on the request origin.
// If no base domain is configured, allows all origins (*).
// Otherwise, allows the base domain and any subdomain of it.
func (g *Gateway) getAllowedOrigin(origin string) string {
	if g.cfg.BaseDomain == "" {
		return "*"
	}
	if origin == "" {
		return "https://" + g.cfg.BaseDomain
	}
	// Extract hostname from origin (e.g., "https://app.dbrs.space" -> "app.dbrs.space")
	host := origin
	if idx := strings.Index(host, "://"); idx != -1 {
		host = host[idx+3:]
	}
	// Strip port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	// Allow exact match or subdomain match
	if host == g.cfg.BaseDomain || strings.HasSuffix(host, "."+g.cfg.BaseDomain) {
		return origin
	}
	// Also allow common development origins
	if host == "localhost" || host == "127.0.0.1" {
		return origin
	}
	return "https://" + g.cfg.BaseDomain
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

// remoteAddrIP extracts the actual TCP peer IP from r.RemoteAddr, ignoring
// X-Forwarded-For and other proxy headers. Use this for security-sensitive
// checks like internal auth validation where we need to verify the direct
// connection source (e.g. WireGuard proxy IP), not the original client.
func remoteAddrIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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

// domainRoutingMiddleware handles requests to deployment domains and namespace gateways
// This must come BEFORE auth middleware so deployment domains work without API keys
//
// Domain routing patterns:
// - ns-{namespace}.{baseDomain} -> Namespace gateway (proxy to namespace cluster)
// - {name}-{random}.{baseDomain} -> Deployment domain
// - {name}.{baseDomain} -> Deployment domain (legacy)
// - {name}.node-xxx.{baseDomain} -> Legacy format (deprecated, returns 404 for new deployments)
func (g *Gateway) domainRoutingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0] // Strip port

		// Get base domain from config (default to dbrs.space)
		baseDomain := "dbrs.space"
		if g.cfg != nil && g.cfg.BaseDomain != "" {
			baseDomain = g.cfg.BaseDomain
		}

		// Only process base domain and its subdomains
		if !strings.HasSuffix(host, "."+baseDomain) && host != baseDomain {
			next.ServeHTTP(w, r)
			return
		}

		// Check for namespace gateway domain FIRST (before API path skip)
		// Namespace subdomains (ns-{name}.{baseDomain}) must be proxied to namespace gateways
		// regardless of path — including /v1/ paths
		suffix := "." + baseDomain
		if strings.HasSuffix(host, suffix) {
			subdomain := strings.TrimSuffix(host, suffix)
			if strings.HasPrefix(subdomain, "ns-") {
				namespaceName := strings.TrimPrefix(subdomain, "ns-")
				g.handleNamespaceGatewayRequest(w, r, namespaceName)
				return
			}
		}

		// Skip API paths (they should use JWT/API key auth on the main gateway)
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/.well-known/") {
			next.ServeHTTP(w, r)
			return
		}

		// Check if deployment handlers are available
		if g.deploymentService == nil || g.staticHandler == nil {
			next.ServeHTTP(w, r)
			return
		}

		// Try to find deployment by domain
		deployment, err := g.getDeploymentByDomain(r.Context(), host)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		if deployment == nil {
			// Domain matches .{baseDomain} but no deployment found
			http.NotFound(w, r)
			return
		}

		// Inject deployment context
		ctx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, deployment.Namespace)
		ctx = context.WithValue(ctx, "deployment", deployment)

		// Route based on deployment type
		if deployment.Port == 0 {
			// Static deployment - serve from IPFS
			g.staticHandler.HandleServe(w, r.WithContext(ctx), deployment)
		} else {
			// Dynamic deployment - proxy to local port
			g.proxyToDynamicDeployment(w, r.WithContext(ctx), deployment)
		}
	})
}

// handleNamespaceGatewayRequest proxies requests to a namespace's dedicated gateway cluster
// This enables physical isolation where each namespace has its own RQLite, Olric, and Gateway
//
// IMPORTANT: This function validates auth against the MAIN cluster RQLite before proxying.
// The validated namespace is passed to the namespace gateway via X-Internal-Auth-* headers.
// This is necessary because namespace gateways have their own isolated RQLite that doesn't
// contain API keys (API keys are stored in the main cluster RQLite only).
func (g *Gateway) handleNamespaceGatewayRequest(w http.ResponseWriter, r *http.Request, namespaceName string) {
	// Validate auth against main cluster RQLite BEFORE proxying
	// This ensures API keys work even though they're not in the namespace's RQLite
	validatedNamespace, validatedClaims, authErr := g.validateAuthForNamespaceProxy(r)
	isWS := isWebSocketUpgrade(r)
	isPublic := isPublicPath(r.URL.Path)

	// Bug #240/#249 root-cause hardening: previously, when
	// validateAuthForNamespaceProxy returned an empty namespace AND empty
	// error (i.e. "no credentials found"), the request fell through to a
	// silent forward to the namespace gateway WITHOUT internal-auth
	// headers. The namespace gateway then rejected the request with 401
	// "missing API key" in ~60µs. From the client's perspective the 401
	// appeared opaque; from our side the failure was logged only on the
	// namespace gateway (which itself can't validate API keys — they
	// live in the main cluster RQLite). This created a confusing
	// debugging experience and was the root cause of AnChat's
	// "intermittent 401" reports on the WS path.
	//
	// Two parts to the fix:
	//   1. Reject at MAIN when no credentials were extractable AND the
	//      path requires auth. Surfaces the failure with a clear message
	//      AT the gateway tier that actually knows about API keys.
	//   2. Log every WS upgrade auth outcome with enough context to
	//      diagnose the intermittent reports we've been seeing
	//      (presence of relevant query params, headers we care about,
	//      and the actor IP). Logged at debug level for success and
	//      warn for the reject path so steady-state noise stays low.
	if authErr != "" && !isPublic {
		if isWS {
			g.logger.ComponentWarn(logging.ComponentGeneral,
				"namespace-proxy WS upgrade rejected: auth error",
				zap.String("namespace_target", namespaceName),
				zap.String("auth_err", authErr),
				zap.String("path", r.URL.Path),
				zap.String("client_ip", getClientIP(r)),
				zap.Bool("has_api_key_query", r.URL.Query().Get("api_key") != ""),
				zap.Bool("has_token_query", r.URL.Query().Get("token") != ""),
				zap.Bool("has_jwt_query", r.URL.Query().Get("jwt") != ""),
				zap.Bool("has_authz_header", r.Header.Get("Authorization") != ""),
				zap.Bool("has_xapikey_header", r.Header.Get("X-API-Key") != ""),
				zap.String("connection_header", r.Header.Get("Connection")),
				zap.String("upgrade_header", r.Header.Get("Upgrade")),
				zap.String("user_agent", r.Header.Get("User-Agent")),
			)
		}
		w.Header().Set("WWW-Authenticate", "Bearer error=\"invalid_token\"")
		writeError(w, http.StatusUnauthorized, authErr)
		return
	}

	// No-credentials path: previously fell through to silent forward.
	// Now: reject at main with diagnostic context. Namespace gateways
	// cannot validate API keys themselves (no shared rqlite for them),
	// so forwarding unauthenticated requests can only ever produce
	// opaque 401s downstream.
	if validatedNamespace == "" && !isPublic {
		g.logger.ComponentWarn(logging.ComponentGeneral,
			"namespace-proxy request rejected: no credentials extracted",
			zap.String("namespace_target", namespaceName),
			zap.String("path", r.URL.Path),
			zap.Bool("is_ws_upgrade", isWS),
			zap.String("client_ip", getClientIP(r)),
			zap.Bool("has_api_key_query", r.URL.Query().Get("api_key") != ""),
			zap.Bool("has_token_query", r.URL.Query().Get("token") != ""),
			zap.Bool("has_jwt_query", r.URL.Query().Get("jwt") != ""),
			zap.Bool("has_authz_header", r.Header.Get("Authorization") != ""),
			zap.Bool("has_xapikey_header", r.Header.Get("X-API-Key") != ""),
			zap.String("connection_header", r.Header.Get("Connection")),
			zap.String("upgrade_header", r.Header.Get("Upgrade")),
			zap.String("origin", r.Header.Get("Origin")),
			zap.String("user_agent", r.Header.Get("User-Agent")),
			zap.Int("raw_query_len", len(r.URL.RawQuery)),
		)
		w.Header().Set("WWW-Authenticate", "Bearer realm=\"gateway\"")
		writeError(w, http.StatusUnauthorized,
			"authentication required for namespace endpoint (no api_key/token/jwt extracted)")
		return
	}

	// If auth succeeded, ensure the API key belongs to the target namespace
	if validatedNamespace != "" && validatedNamespace != namespaceName {
		g.logger.ComponentWarn(logging.ComponentGeneral,
			"namespace-proxy request rejected: API key namespace mismatch",
			zap.String("namespace_target", namespaceName),
			zap.String("validated_namespace", validatedNamespace),
			zap.String("path", r.URL.Path),
			zap.Bool("is_ws_upgrade", isWS),
			zap.String("client_ip", getClientIP(r)),
		)
		writeError(w, http.StatusForbidden, "API key does not belong to this namespace")
		return
	}

	// Success-path diagnostic for WS upgrades. Logged at debug to keep
	// the steady-state log volume low; flip the gateway log level to
	// `debug` to capture per-upgrade audit trail when reproducing
	// AnChat-style intermittent failures.
	if isWS {
		g.logger.ComponentDebug(logging.ComponentGeneral,
			"namespace-proxy WS upgrade authenticated, forwarding",
			zap.String("namespace", namespaceName),
			zap.String("path", r.URL.Path),
			zap.String("client_ip", getClientIP(r)),
			zap.Bool("has_jwt_claims", validatedClaims != nil),
		)
	}

	// Check middleware cache for namespace gateway targets
	type namespaceGatewayTarget struct {
		ip   string
		port int
	}
	var targets []namespaceGatewayTarget

	if g.mwCache != nil {
		if cached, ok := g.mwCache.GetNamespaceTargets(namespaceName); ok {
			for _, t := range cached {
				targets = append(targets, namespaceGatewayTarget{ip: t.ip, port: t.port})
			}
		}
	}

	// Cache miss — look up namespace cluster gateway from DB
	if len(targets) == 0 {
		db := g.client.Database()
		internalCtx := client.WithInternalAuth(r.Context())

		// Query all ready namespace gateways and choose a stable target.
		// Random selection causes WS subscribe and publish calls to hit different
		// nodes, which makes pubsub delivery flaky for short-lived subscriptions.
		query := `
			SELECT COALESCE(dn.internal_ip, dn.ip_address), npa.gateway_http_port
			FROM namespace_port_allocations npa
			JOIN namespace_clusters nc ON npa.namespace_cluster_id = nc.id
			JOIN dns_nodes dn ON npa.node_id = dn.id
			WHERE nc.namespace_name = ? AND nc.status = 'ready'
		`
		result, err := db.Query(internalCtx, query, namespaceName)
		if err != nil || result == nil || len(result.Rows) == 0 {
			g.logger.ComponentWarn(logging.ComponentGeneral, "namespace gateway not found",
				zap.String("namespace", namespaceName),
				zap.Error(err),
				zap.Bool("result_nil", result == nil),
				zap.Int("row_count", func() int { if result != nil { return len(result.Rows) }; return -1 }()),
			)
			http.Error(w, "Namespace gateway not found", http.StatusNotFound)
			return
		}

		for _, row := range result.Rows {
			if len(row) == 0 {
				continue
			}
			ip := getString(row[0])
			if ip == "" {
				continue
			}
			port := 10004
			if len(row) > 1 {
				if p := getInt(row[1]); p > 0 {
					port = p
				}
			}
			targets = append(targets, namespaceGatewayTarget{ip: ip, port: port})
		}

		// Cache the result for subsequent requests
		if g.mwCache != nil && len(targets) > 0 {
			cacheTargets := make([]gatewayTarget, len(targets))
			for i, t := range targets {
				cacheTargets[i] = gatewayTarget{ip: t.ip, port: t.port}
			}
			g.mwCache.SetNamespaceTargets(namespaceName, cacheTargets)
		}
	}

	if len(targets) == 0 {
		http.Error(w, "Namespace gateway not available", http.StatusServiceUnavailable)
		return
	}

	// Keep ordering deterministic before hashing, otherwise DB row order can vary.
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ip == targets[j].ip {
			return targets[i].port < targets[j].port
		}
		return targets[i].ip < targets[j].ip
	})

	// Build ordered target list: local gateway first, then hash-selected, then remaining.
	// This ordering is used by the circuit breaker fallback loop below.
	orderedTargets := make([]namespaceGatewayTarget, 0, len(targets))
	localIdx := -1
	if g.localWireGuardIP != "" {
		for i, t := range targets {
			if t.ip == g.localWireGuardIP {
				orderedTargets = append(orderedTargets, t)
				localIdx = i
				break
			}
		}
	}

	// Consistent hashing for affinity (keeps WS subscribe/publish on same node)
	affinityKey := namespaceName + "|" + validatedNamespace
	if apiKey := extractAPIKey(r); apiKey != "" {
		affinityKey = namespaceName + "|" + apiKey
	} else if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		affinityKey = namespaceName + "|" + authz
	} else {
		affinityKey = namespaceName + "|" + getClientIP(r)
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(affinityKey))
	hashIdx := int(hasher.Sum32()) % len(targets)
	if hashIdx != localIdx {
		orderedTargets = append(orderedTargets, targets[hashIdx])
	}
	for i, t := range targets {
		if i != localIdx && i != hashIdx {
			orderedTargets = append(orderedTargets, t)
		}
	}

	// Select the first target whose circuit breaker allows a request through.
	// This provides automatic failover when a namespace gateway node is down.
	var selected namespaceGatewayTarget
	var cb *CircuitBreaker
	for _, candidate := range orderedTargets {
		cbKey := "ns:" + candidate.ip
		candidateCB := g.circuitBreakers.Get(cbKey)
		if candidateCB.Allow() {
			selected = candidate
			cb = candidateCB
			break
		}
	}
	if selected.ip == "" {
		// Bug #219: emit canonical envelope with retryable=true (transient).
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable,
			"namespace gateway unavailable: all upstream circuits are open. "+
				"Wait a few seconds and retry, or check `orama monitor report` for unhealthy nodes.",
			httputil.WithRetryable())
		return
	}
	gatewayIP := selected.ip
	gatewayPort := selected.port
	targetHost := gatewayIP + ":" + strconv.Itoa(gatewayPort)

	// Handle WebSocket upgrade requests specially (http.Client can't handle 101 Switching Protocols)
	if isWebSocketUpgrade(r) {
		// Set forwarding headers on the original request
		r.Header.Set("X-Forwarded-For", getClientIP(r))
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", r.Host)
		// SECURITY (bug #215 follow-up): drop any X-Internal-Auth-* headers
		// the external client may have set BEFORE applying the trusted
		// values from this gateway. Prevents identity spoofing on the
		// namespace gateway, which gates on source IP only.
		stripInboundInternalAuthHeaders(r.Header)
		// Set internal auth headers if auth was validated
		if validatedNamespace != "" {
			r.Header.Set(HeaderInternalAuthValidated, "true")
			r.Header.Set(HeaderInternalAuthNamespace, validatedNamespace)
			// Bug #215: forward validated JWT subject + custom claims so the
			// namespace gateway's auth middleware can hydrate ctxKeyJWT and
			// host functions see a non-empty caller_jwt_subject.
			setInternalAuthJWTHeaders(r.Header, validatedClaims)
		}
		r.URL.Scheme = "http"
		r.URL.Host = targetHost
		r.Host = targetHost
		if g.proxyWebSocket(w, r, targetHost) {
			return
		}
		// If WebSocket proxy failed and already wrote error, return
		return
	}

	// Proxy regular HTTP request to the namespace gateway
	targetURL := "http://" + targetHost + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "failed to create namespace gateway proxy request",
			zap.String("namespace", namespaceName),
			zap.Error(err),
		)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	proxyReq.Header.Set("X-Forwarded-For", getClientIP(r))
	proxyReq.Header.Set("X-Forwarded-Proto", "https")
	proxyReq.Header.Set("X-Forwarded-Host", r.Host)
	proxyReq.Header.Set("X-Original-Host", r.Host)

	// SECURITY (bug #215 follow-up): drop any X-Internal-Auth-* headers
	// the header-copy loop above may have inherited from the inbound
	// request BEFORE writing the trusted values. The namespace gateway
	// gates on source IP only; without this strip, an external attacker
	// could forge X-Internal-Auth-JWT-Sub and impersonate any wallet.
	stripInboundInternalAuthHeaders(proxyReq.Header)
	// Set internal auth headers if auth was validated by main gateway
	// This allows the namespace gateway to trust the authentication
	if validatedNamespace != "" {
		proxyReq.Header.Set(HeaderInternalAuthValidated, "true")
		proxyReq.Header.Set(HeaderInternalAuthNamespace, validatedNamespace)
		// Bug #215: forward validated JWT subject + custom claims so the
		// namespace gateway's auth middleware can hydrate ctxKeyJWT and
		// host functions see a non-empty caller_jwt_subject.
		setInternalAuthJWTHeaders(proxyReq.Header, validatedClaims)
	}

	// Pick the proxy timeout based on the path's expected work bound.
	// Defaults to 30s for fast paths; bumps to 300s for known long-running
	// classes (uploads, function invocations) so we don't truncate before
	// the namespace gateway's own per-function timeout fires.
	//
	// Bug #219: function invokes were truncated at 30s and surfaced as
	// "Namespace gateway unavailable" — misleading because the gateway IS
	// up, the FUNCTION just exceeded its budget. Aligning the proxy
	// timeout with the function-level cap ensures the namespace gateway's
	// proper TIMEOUT envelope reaches the client first.
	proxyTimeout := 30 * time.Second
	if isLongRunningProxyPath(r.URL.Path) {
		proxyTimeout = 300 * time.Second
	}

	// Execute proxy request using shared transport for connection pooling
	httpClient := &http.Client{Timeout: proxyTimeout, Transport: g.proxyTransport}
	resp, err := httpClient.Do(proxyReq)
	if err != nil {
		cb.RecordFailure()
		g.logger.ComponentError(logging.ComponentGeneral, "namespace gateway proxy request failed",
			zap.String("namespace", namespaceName),
			zap.String("target", gatewayIP),
			zap.Error(err),
		)
		// Distinguish timeout from connection failure so clients get an
		// actionable code (#219). "Namespace gateway unavailable" was
		// emitted indiscriminately and sent operators down the wrong
		// debug path.
		if isProxyTimeoutErr(err) {
			httputil.WriteRPCError(w, http.StatusGatewayTimeout,
				httputil.ErrCodeTimeout,
				"function or upstream call exceeded the proxy budget ("+
					proxyTimeout.String()+
					"). Increase the function's timeout: in function.yaml (max 300s) or split the work into smaller invocations.",
			)
			return
		}
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable,
			"namespace gateway unavailable: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if IsResponseFailure(resp.StatusCode) {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code and body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// getDeploymentByDomain looks up a deployment by its domain
// Supports formats like:
//   - {name}-{random}.{baseDomain} (e.g., myapp-f3o4if.dbrs.space) - new format with random suffix
//   - {name}.{baseDomain} (e.g., myapp.dbrs.space) - legacy format (backwards compatibility)
//   - {name}.node-{shortID}.{baseDomain} (legacy format for backwards compatibility)
//   - custom domains via deployment_domains table
func (g *Gateway) getDeploymentByDomain(ctx context.Context, domain string) (*deployments.Deployment, error) {
	if g.deploymentService == nil {
		return nil, nil
	}

	// Strip trailing dot if present
	domain = strings.TrimSuffix(domain, ".")

	// Get base domain from config (default to dbrs.space)
	baseDomain := "dbrs.space"
	if g.cfg != nil && g.cfg.BaseDomain != "" {
		baseDomain = g.cfg.BaseDomain
	}

	db := g.client.Database()
	internalCtx := client.WithInternalAuth(ctx)

	// Parse domain to extract deployment subdomain/name
	suffix := "." + baseDomain
	if strings.HasSuffix(domain, suffix) {
		subdomain := strings.TrimSuffix(domain, suffix)
		parts := strings.Split(subdomain, ".")

		// Primary format: {subdomain}.{baseDomain} (e.g., myapp-f3o4if.dbrs.space)
		// The subdomain can be either:
		// - {name}-{random} (new format)
		// - {name} (legacy format)
		if len(parts) == 1 {
			subdomainOrName := parts[0]

			// First, try to find by subdomain (new format: name-random)
			query := `
				SELECT id, namespace, name, type, port, content_cid, status, home_node_id, subdomain
				FROM deployments
				WHERE subdomain = ?
				AND status IN ('active', 'degraded')
				LIMIT 1
			`
			result, err := db.Query(internalCtx, query, subdomainOrName)
			if err == nil && len(result.Rows) > 0 {
				row := result.Rows[0]
				return &deployments.Deployment{
					ID:         getString(row[0]),
					Namespace:  getString(row[1]),
					Name:       getString(row[2]),
					Type:       deployments.DeploymentType(getString(row[3])),
					Port:       getInt(row[4]),
					ContentCID: getString(row[5]),
					Status:     deployments.DeploymentStatus(getString(row[6])),
					HomeNodeID: getString(row[7]),
					Subdomain:  getString(row[8]),
				}, nil
			}

			// Fallback: try by name for legacy deployments (without random suffix)
			query = `
				SELECT id, namespace, name, type, port, content_cid, status, home_node_id, subdomain
				FROM deployments
				WHERE name = ?
				AND status IN ('active', 'degraded')
				LIMIT 1
			`
			result, err = db.Query(internalCtx, query, subdomainOrName)
			if err == nil && len(result.Rows) > 0 {
				row := result.Rows[0]
				return &deployments.Deployment{
					ID:         getString(row[0]),
					Namespace:  getString(row[1]),
					Name:       getString(row[2]),
					Type:       deployments.DeploymentType(getString(row[3])),
					Port:       getInt(row[4]),
					ContentCID: getString(row[5]),
					Status:     deployments.DeploymentStatus(getString(row[6])),
					HomeNodeID: getString(row[7]),
					Subdomain:  getString(row[8]),
				}, nil
			}
		}

	}

	// Try custom domain from deployment_domains table
	query := `
		SELECT d.id, d.namespace, d.name, d.type, d.port, d.content_cid, d.status, d.home_node_id
		FROM deployments d
		JOIN deployment_domains dd ON d.id = dd.deployment_id
		WHERE dd.domain = ? AND dd.verified_at IS NOT NULL
		AND d.status IN ('active', 'degraded')
		LIMIT 1
	`
	result, err := db.Query(internalCtx, query, domain)
	if err == nil && len(result.Rows) > 0 {
		row := result.Rows[0]
		return &deployments.Deployment{
			ID:         getString(row[0]),
			Namespace:  getString(row[1]),
			Name:       getString(row[2]),
			Type:       deployments.DeploymentType(getString(row[3])),
			Port:       getInt(row[4]),
			ContentCID: getString(row[5]),
			Status:     deployments.DeploymentStatus(getString(row[6])),
			HomeNodeID: getString(row[7]),
		}, nil
	}

	return nil, nil
}

// proxyToDynamicDeployment proxies requests to a dynamic deployment's local port
// If the deployment is on a different node, it forwards the request to that node.
// With replica support, it first checks if the current node is a replica and can
// serve the request locally using the replica's port.
func (g *Gateway) proxyToDynamicDeployment(w http.ResponseWriter, r *http.Request, deployment *deployments.Deployment) {
	if deployment.Port == 0 {
		http.Error(w, "Deployment has no assigned port", http.StatusServiceUnavailable)
		return
	}

	// Check if request was already forwarded by another node (loop prevention)
	proxyNode := r.Header.Get("X-Orama-Proxy-Node")

	// Check if this deployment is on the current node (primary)
	if g.nodePeerID != "" && deployment.HomeNodeID != "" &&
		deployment.HomeNodeID != g.nodePeerID && proxyNode == "" {

		// Check if this node is a replica and can serve locally
		if g.replicaManager != nil {
			replicaPort, err := g.replicaManager.GetReplicaPort(r.Context(), deployment.ID, g.nodePeerID)
			if err == nil && replicaPort > 0 {
				// This node is a replica — serve locally using the replica's port
				g.logger.Debug("Serving from local replica",
					zap.String("deployment", deployment.Name),
					zap.Int("replica_port", replicaPort),
				)
				deployment.Port = replicaPort
				// Fall through to local proxy below
				goto serveLocal
			}
		}

		// Not a replica on this node — proxy to a healthy replica node
		if g.proxyCrossNodeWithReplicas(w, r, deployment) {
			return
		}
		// Fall through if cross-node proxy failed - try local anyway
		g.logger.Warn("Cross-node proxy failed, attempting local fallback",
			zap.String("deployment", deployment.Name),
			zap.String("home_node", deployment.HomeNodeID),
		)
	}

serveLocal:

	// Create a simple reverse proxy to localhost
	targetHost := "localhost:" + strconv.Itoa(deployment.Port)
	target := "http://" + targetHost

	// SECURITY (bug #215 follow-up): drop X-Internal-Auth-* headers before
	// forwarding to a customer-deployed app on localhost. The app has no
	// reason to see Orama's internal-auth metadata, and stripping prevents
	// any accidental leakage if the customer ever adds header-based trust.
	stripInboundInternalAuthHeaders(r.Header)
	// Set proxy headers
	r.Header.Set("X-Forwarded-For", getClientIP(r))
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", r.Host)

	// Handle WebSocket upgrade requests specially
	if isWebSocketUpgrade(r) {
		r.URL.Scheme = "http"
		r.URL.Host = targetHost
		r.Host = targetHost
		if g.proxyWebSocket(w, r, targetHost) {
			return
		}
		// WebSocket proxy failed - try cross-node replicas as fallback
		if g.replicaManager != nil {
			if g.proxyCrossNodeWithReplicas(w, r, deployment) {
				return
			}
		}
		http.Error(w, "WebSocket connection failed", http.StatusServiceUnavailable)
		return
	}

	// Create a new request to the backend
	backendURL := target + r.URL.Path
	if r.URL.RawQuery != "" {
		backendURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, backendURL, r.Body)
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Execute proxy request using shared transport
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: g.proxyTransport}
	resp, err := httpClient.Do(proxyReq)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "local proxy request failed",
			zap.String("target", target),
			zap.Error(err),
		)

		// Local process is down — try other replica nodes before giving up
		if g.replicaManager != nil {
			if g.proxyCrossNodeWithReplicas(w, r, deployment) {
				return
			}
		}

		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code and body
	w.WriteHeader(resp.StatusCode)
	if _, err := w.(io.Writer).Write([]byte{}); err == nil {
		io.Copy(w, resp.Body)
	}
}

// proxyCrossNode forwards a request to the home node of a deployment
// Returns true if the request was successfully forwarded, false otherwise
func (g *Gateway) proxyCrossNode(w http.ResponseWriter, r *http.Request, deployment *deployments.Deployment) bool {
	// Get home node IP from dns_nodes table
	db := g.client.Database()
	internalCtx := client.WithInternalAuth(r.Context())

	query := "SELECT COALESCE(internal_ip, ip_address) FROM dns_nodes WHERE id = ? LIMIT 1"
	result, err := db.Query(internalCtx, query, deployment.HomeNodeID)
	if err != nil || result == nil || len(result.Rows) == 0 {
		g.logger.Warn("Failed to get home node IP",
			zap.String("home_node_id", deployment.HomeNodeID),
			zap.Error(err))
		return false
	}

	homeIP := getString(result.Rows[0][0])
	if homeIP == "" {
		g.logger.Warn("Home node IP is empty", zap.String("home_node_id", deployment.HomeNodeID))
		return false
	}

	g.logger.Info("Proxying request to home node",
		zap.String("deployment", deployment.Name),
		zap.String("home_node_id", deployment.HomeNodeID),
		zap.String("home_ip", homeIP),
		zap.String("current_node", g.nodePeerID),
	)

	// Proxy to home node via internal HTTP port (6001)
	// This is node-to-node internal communication - no TLS needed
	targetHost := homeIP + ":6001"

	// Handle WebSocket upgrade requests specially
	if isWebSocketUpgrade(r) {
		// SECURITY (bug #215 follow-up): drop X-Internal-Auth-* headers from
		// the inbound request before forwarding to another node. The target
		// node's authMiddleware gates internal-auth trust on source IP only;
		// without this strip, an external attacker could forge JWT-Sub here
		// and have the home node trust it after the cross-node hop.
		stripInboundInternalAuthHeaders(r.Header)
		r.Header.Set("X-Forwarded-For", getClientIP(r))
		r.Header.Set("X-Orama-Proxy-Node", g.nodePeerID)
		r.URL.Scheme = "http"
		r.URL.Host = targetHost
		// Keep original Host header for domain routing
		return g.proxyWebSocket(w, r, targetHost)
	}

	targetURL := "http://" + targetHost + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		g.logger.Error("Failed to create cross-node proxy request", zap.Error(err))
		return false
	}

	// Copy headers and set Host header to original domain for routing
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	// SECURITY (bug #215 follow-up): drop any X-Internal-Auth-* headers the
	// inbound request may have carried. See WS branch above.
	stripInboundInternalAuthHeaders(proxyReq.Header)
	proxyReq.Host = r.Host // Keep original host for domain routing on target node
	proxyReq.Header.Set("X-Forwarded-For", getClientIP(r))
	proxyReq.Header.Set("X-Orama-Proxy-Node", g.nodePeerID) // Prevent loops

	// Circuit breaker: check if target node is healthy
	cbKey := "node:" + homeIP
	cb := g.circuitBreakers.Get(cbKey)
	if !cb.Allow() {
		g.logger.Warn("Cross-node proxy skipped (circuit open)", zap.String("target_ip", homeIP))
		return false
	}

	// Internal node-to-node communication using shared transport
	httpClient := &http.Client{Timeout: 120 * time.Second, Transport: g.proxyTransport}
	resp, err := httpClient.Do(proxyReq)
	if err != nil {
		cb.RecordFailure()
		g.logger.Error("Cross-node proxy request failed",
			zap.String("target_ip", homeIP),
			zap.String("host", r.Host),
			zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	if IsResponseFailure(resp.StatusCode) {
		cb.RecordFailure()
	} else {
		cb.RecordSuccess()
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code and body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	return true
}

// proxyCrossNodeWithReplicas tries to proxy a request to any healthy replica node.
// It first tries the primary (home node), then falls back to other replicas.
// Returns true if the request was successfully proxied.
func (g *Gateway) proxyCrossNodeWithReplicas(w http.ResponseWriter, r *http.Request, deployment *deployments.Deployment) bool {
	if g.replicaManager == nil {
		// No replica manager — fall back to original single-node proxy
		return g.proxyCrossNode(w, r, deployment)
	}

	// Get all active replica nodes
	replicaNodes, err := g.replicaManager.GetActiveReplicaNodes(r.Context(), deployment.ID)
	if err != nil || len(replicaNodes) == 0 {
		// Fall back to original home node proxy
		return g.proxyCrossNode(w, r, deployment)
	}

	// Try each replica node (primary first if present)
	for _, nodeID := range replicaNodes {
		if nodeID == g.nodePeerID {
			continue // Skip self
		}

		nodeIP, err := g.replicaManager.GetNodeIP(r.Context(), nodeID)
		if err != nil {
			g.logger.Warn("Failed to get replica node IP",
				zap.String("node_id", nodeID),
				zap.Error(err),
			)
			continue
		}

		// Proxy using the same logic as proxyCrossNode
		proxyDeployment := *deployment
		proxyDeployment.HomeNodeID = nodeID
		if g.proxyCrossNodeToIP(w, r, &proxyDeployment, nodeIP) {
			return true
		}
	}

	return false
}

// proxyCrossNodeToIP forwards a request to a specific node IP.
// This is a variant of proxyCrossNode that takes a resolved IP directly.
func (g *Gateway) proxyCrossNodeToIP(w http.ResponseWriter, r *http.Request, deployment *deployments.Deployment, nodeIP string) bool {
	g.logger.Info("Proxying request to replica node",
		zap.String("deployment", deployment.Name),
		zap.String("node_id", deployment.HomeNodeID),
		zap.String("node_ip", nodeIP),
	)

	targetHost := nodeIP + ":6001"

	// Handle WebSocket upgrade requests specially
	if isWebSocketUpgrade(r) {
		// SECURITY (bug #215 follow-up): see proxyCrossNode for rationale.
		stripInboundInternalAuthHeaders(r.Header)
		r.Header.Set("X-Forwarded-For", getClientIP(r))
		r.Header.Set("X-Orama-Proxy-Node", g.nodePeerID)
		r.URL.Scheme = "http"
		r.URL.Host = targetHost
		return g.proxyWebSocket(w, r, targetHost)
	}

	targetURL := "http://" + targetHost + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		g.logger.Error("Failed to create cross-node proxy request", zap.Error(err))
		return false
	}

	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	// SECURITY (bug #215 follow-up): see proxyCrossNode for rationale.
	stripInboundInternalAuthHeaders(proxyReq.Header)
	proxyReq.Host = r.Host
	proxyReq.Header.Set("X-Forwarded-For", getClientIP(r))
	proxyReq.Header.Set("X-Orama-Proxy-Node", g.nodePeerID)

	// Circuit breaker: skip this replica if it's been failing
	cbKey := "node:" + nodeIP
	cb := g.circuitBreakers.Get(cbKey)
	if !cb.Allow() {
		g.logger.Warn("Replica proxy skipped (circuit open)", zap.String("target_ip", nodeIP))
		return false
	}

	httpClient := &http.Client{Timeout: 5 * time.Second, Transport: g.proxyTransport}
	resp, err := httpClient.Do(proxyReq)
	if err != nil {
		cb.RecordFailure()
		g.logger.Warn("Replica proxy request failed",
			zap.String("target_ip", nodeIP),
			zap.Error(err),
		)
		return false
	}
	defer resp.Body.Close()

	// If the remote node returned a gateway error, try the next replica
	if IsResponseFailure(resp.StatusCode) {
		cb.RecordFailure()
		g.logger.Warn("Replica returned gateway error, trying next",
			zap.String("target_ip", nodeIP),
			zap.Int("status", resp.StatusCode),
		)
		return false
	}
	cb.RecordSuccess()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

	return true
}

// Helper functions for type conversion
func getString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func getInt(v interface{}) int {
	if i, ok := v.(int); ok {
		return i
	}
	if i, ok := v.(int64); ok {
		return int(i)
	}
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
