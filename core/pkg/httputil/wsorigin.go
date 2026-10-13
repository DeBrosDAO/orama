package httputil

import (
	"net/http"
	"net/url"
	"strings"
)

// CheckWebSocketOrigin is the Origin policy for WebSocket upgrades served by a
// gateway that sits behind a reverse proxy.
//
// A same-origin comparison against r.Host is wrong here: Caddy forwards to the
// gateway's internal address, so a browser or React-Native client — which always
// sends Origin — would be rejected because its Origin
// ("ns-<ns>.<base-domain>") can never equal the proxied target. curl, which
// sends no Origin, would slip through and mask the bug. So the ORIGINAL public
// host from X-Forwarded-Host is preferred, falling back to r.Host for a direct
// connection.
//
// A missing Origin is allowed: non-browser clients omit it, and Origin is not
// an authentication signal — it only defends against a browser on another site
// silently opening a socket with the user's cookies. Every endpoint using this
// authenticates separately.
//
// NOTE: pkg/gateway/handlers/serverless and pkg/gateway/handlers/pubsub each
// carry their own older copy of this logic. New callers should use this one.
func CheckWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return false
	}
	// Strip a port if present. An IPv6 literal is bracketed, so only cut at a
	// colon that follows the closing bracket.
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.Contains(host[idx:], "]") {
		host = host[:idx]
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := parsed.Hostname()
	if originHost == "" {
		return false
	}
	return originHost == host || strings.HasSuffix(originHost, "."+host)
}
