package gateway

import (
	"net"
	"net/http"
	"strings"
)

// Rate limiting used to key on getClientIP, which returns the first entry of
// X-Forwarded-For. Anyone could send that header, and any address in the
// WireGuard subnet was exempt from every limit — so one header removed all
// rate limiting, including from the endpoints that mint credentials.
//
// The peer address is the only thing a caller cannot choose. X-Forwarded-For
// is honoured only when the peer is the local reverse proxy, and only its last
// entry: Caddy appends the address it is actually talking to, so the last entry
// is the real client and the ones before it are whatever the client claimed.
//
// Two things follow that are worth being explicit about, because getting either
// backwards would be worse than the bug:
//
//   - Loopback is not exempt when the request arrived through Caddy. Every
//     public request reaches the gateway from 127.0.0.1, so exempting loopback
//     after this fix would exempt the whole internet.
//   - Loopback with no forwarding header is a genuinely local caller — a
//     service on the node talking to the index gateway — and stays exempt.

// rateLimitClient returns the address to hold responsible for a request, and
// whether it is internal traffic exempt from limits.
func rateLimitClient(r *http.Request) (client string, exempt bool) {
	peer := remoteAddrIP(r)

	// A caller on the overlay is another node's service, and the mesh is not
	// reachable from outside.
	if ip := net.ParseIP(peer); ip != nil && wireGuardNet != nil && wireGuardNet.Contains(ip) {
		return peer, true
	}

	if isLoopback(peer) {
		// Came through the local reverse proxy: the last entry is the address
		// Caddy is talking to, and everything before it is what that caller
		// claimed. Anything the caller can write is not a rate-limit key.
		if forwarded := lastForwardedFor(r); forwarded != "" {
			return forwarded, false
		}
		// Nothing forwarded, so this really is a process on this machine.
		return peer, true
	}

	// A direct connection from off the node. X-Forwarded-For here is entirely
	// the caller's invention and is ignored.
	return peer, false
}

// lastForwardedFor returns the final entry of X-Forwarded-For, which is the
// address the nearest proxy appended.
func lastForwardedFor(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(last) == nil {
		return ""
	}
	return last
}

func isLoopback(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.IsLoopback()
}

// authRateLimitPaths are the endpoints that mint or exchange credentials.
//
// They are cheap to call and expensive to serve — a challenge writes a nonce
// row and can create a namespace, a verify runs signature recovery, a token
// exchange mints a JWT — and they are the ones worth grinding. They get their
// own, much tighter bucket than the general one.
func isAuthRateLimitPath(path string) bool {
	switch path {
	case "/v1/auth/challenge", "/v1/auth/verify", "/v1/auth/api-key",
		"/v1/auth/token", "/v1/auth/refresh":
		return true
	}
	return false
}
