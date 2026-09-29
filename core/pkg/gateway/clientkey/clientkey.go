// Package clientkey decides which address a request is attributed to (rate limits, the request log,
// namespace affinity, the audit trail, the X-Forwarded-For handed to proxied services) and the
// bucket key that address is limited under. The cluster gateway, its auth audit and the vault proxy
// share it, so no handler keeps its own reading of X-Forwarded-For.
package clientkey

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/auth"
)

var wireGuardNet *net.IPNet

func init() {
	_, wireGuardNet, _ = net.ParseCIDR(auth.WireGuardSubnet)
}

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

// Resolve returns the address to hold responsible for a request, and
// whether it is internal traffic exempt from limits.
func Resolve(r *http.Request) (client string, exempt bool) {
	peer := peerIP(r)

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

func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipv6BucketBits is the prefix an IPv6 client is limited by. An IPv6 subscriber is routinely handed a
// whole /64 and can source a request from any address inside it, so a bucket per address would give
// one client 2^64 of them; the /64 is the smallest network a single host can be assumed to own.
const ipv6BucketBits = 64

// BucketKey is the key a client address is limited under: an IPv4 address as it is, an IPv6
// address as its /64 prefix (an IPv4-mapped one as the IPv4 address). Anything that does not parse
// is used as it is.
func BucketKey(client string) string {
	addr, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	addr = addr.Unmap()
	if !addr.Is6() {
		return addr.String()
	}
	prefix, err := addr.WithZone("").Prefix(ipv6BucketBits)
	if err != nil {
		return client
	}
	return prefix.String()
}
