// Package clientkey decides which address a request belongs to, in separate questions:
//
//   - Resolve: whom to rate-limit, and whether the caller is internal traffic exempt from limits.
//   - Attribute: whom to name in the request log, the audit trail, namespace affinity and the
//     X-Forwarded-For handed to proxied services.
//   - Public: whom a route that exempts nobody (the faucet) holds responsible.
//
// and the bucket key an address is limited under (BucketKey). The cluster gateway, its auth audit,
// the serverless handlers and the vault proxy share them, so no handler keeps its own reading of
// X-Forwarded-For. The one thing they all depend on is Prover, which tells a request that came
// through the node's own Caddy from one a process on the node wrote.
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
// The peer address is the only thing a caller cannot choose, and behind the node's Caddy it is
// always 127.0.0.1. X-Forwarded-For is honoured only on a request that Caddy proved it forwarded
// (Prover): Caddy puts a per-node secret in ProofHeader, and the last entry is then the address
// Caddy is actually talking to, the real client; the ones before it are whatever the client
// claimed. A process on the node, tenant code included, also reaches the gateway from the loopback
// address and can write any X-Forwarded-For, but it cannot read the secret, so its header is
// never believed and it is keyed by its peer address.
//
// Two things follow that are worth being explicit about, because getting either
// backwards would be worse than the bug:
//
//   - Loopback is not exempt when the request arrived through Caddy, or when it carries a
//     forwarding header that is not proven. Every public request reaches the gateway from
//     127.0.0.1, so exempting loopback would exempt the whole internet; a Caddy that has lost its
//     proof leaves the public sharing one bucket, which is the safe side.
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
		if client, ok := provenClient(r); ok {
			return client, false
		}
		// Through Caddy but with no usable address, or a forwarding header nobody proved: not a
		// local process, and not exempt.
		if proven(r) || r.Header.Get("X-Forwarded-For") != "" {
			return peer, false
		}
		// Nothing forwarded, so this really is a process on this machine.
		return peer, true
	}

	// A direct connection from off the node. X-Forwarded-For here is entirely
	// the caller's invention and is ignored.
	return peer, false
}

// Attribute returns the address a request is attributed to. The peer address is the client unless
// the peer is one that writes a trustworthy X-Forwarded-For: the local reverse proxy (loopback,
// when the request carries its proof), which appends the address it is talking to, or another
// node's gateway on the WireGuard mesh, which forwards a single value it resolved itself. From
// such a peer the last entry is the client, when it parses as an IP. Any other peer, a private
// address that is not on the mesh included, is the client itself and its headers (X-Forwarded-For,
// X-Real-IP) are ignored. Unlike Resolve it never exempts.
func Attribute(r *http.Request) string {
	peer := peerIP(r)
	ip := net.ParseIP(peer)
	if ip == nil {
		return peer
	}
	if ip.IsLoopback() {
		if client, ok := provenClient(r); ok {
			return client
		}
		return peer
	}
	if wireGuardNet != nil && wireGuardNet.Contains(ip) {
		if forwarded := lastForwardedFor(r); forwarded != "" {
			return forwarded
		}
	}
	return peer
}

// Public returns the client of a route open to the internet that exempts nobody (the faucet): the
// address Caddy proved it is talking to, or else the address the connection came from. A process
// on the node, tenant code included, reaches the gateway from the loopback address and can write
// any X-Forwarded-For; without Caddy's proof its header is not read, so it is one client however
// many names it claims.
func Public(r *http.Request) string {
	if client, ok := provenClient(r); ok {
		return client
	}
	return peerIP(r)
}

// provenClient returns the client of a request that came through the node's Caddy: the peer is the
// loopback address, the request carries Caddy's proof, and the last X-Forwarded-For entry (the
// address Caddy appended) is an IP.
func provenClient(r *http.Request) (string, bool) {
	if !isLoopback(peerIP(r)) || !proven(r) {
		return "", false
	}
	client := lastForwardedFor(r)
	return client, client != ""
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
