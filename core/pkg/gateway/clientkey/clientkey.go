// Package clientkey decides which address a request belongs to, in two separate questions:
//
//   - Resolve: whom to rate-limit, and whether the caller is internal traffic exempt from limits.
//   - Attribute: whom to name in the request log, the audit trail, namespace affinity and the
//     X-Forwarded-For handed to proxied services.
//
// and the bucket key an address is limited under (BucketKey). The cluster gateway, its auth audit,
// the serverless handlers and the vault proxy share them, so no handler keeps its own reading of
// X-Forwarded-For.
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

// Attribute returns the address a request is attributed to. The peer address is the client unless
// the peer is one that writes a trustworthy X-Forwarded-For: the local reverse proxy (loopback),
// which appends the address it is talking to, or another node's gateway on the WireGuard mesh, which
// forwards a single value it resolved itself. From such a peer the last entry is the client, when it
// parses as an IP. Any other peer, a private address that is not on the mesh included, is the client
// itself and its headers (X-Forwarded-For, X-Real-IP) are ignored. Unlike Resolve it never exempts.
func Attribute(r *http.Request) string {
	peer := peerIP(r)
	ip := net.ParseIP(peer)
	if ip == nil {
		return peer
	}
	if ip.IsLoopback() || (wireGuardNet != nil && wireGuardNet.Contains(ip)) {
		if forwarded := lastForwardedFor(r); forwarded != "" {
			return forwarded
		}
	}
	return peer
}

// Peer returns the address the connection came from and nothing else: no forwarding header is read.
// It is the client of a route that must not take a caller's word for who it is (the faucet): a
// process on the node, tenant code included, reaches the gateway from the loopback address and can
// write any X-Forwarded-For, and a header written by whoever sent the request is not proof of the
// local reverse proxy. Behind that proxy every public caller therefore shares the proxy's address
// until the proxy proves itself to the gateway with a header only it can write.
func Peer(r *http.Request) string { return peerIP(r) }

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
