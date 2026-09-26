package auth

import (
	"net"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// WireGuardSubnet is the internal WireGuard mesh CIDR.
const WireGuardSubnet = constants.WireGuardSubnet

// IsWireGuardPeer checks whether remoteAddr (host:port format) originates
// from the WireGuard mesh subnet. This provides cryptographic peer
// authentication since WireGuard validates keys at the tunnel layer.
func IsWireGuardPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	_, wgNet, _ := net.ParseCIDR(WireGuardSubnet)
	return wgNet.Contains(ip)
}

// ReachedWithoutPublicProxy reports whether a request arrived from a process
// on this host or from a node on the overlay, rather than from the internet
// through Caddy. It is a reachability filter and never a credential: every
// process on this host — a tenant's deployment included — and every peer on the
// mesh passes it, so an endpoint may use it only in front of a check that
// authenticates the caller, to keep answering the internet with 404.
//
// Loopback alone is not that distinction. Caddy terminates TLS and
// reverse-proxies **every path** to the gateway on localhost, so the source
// address of every public request is 127.0.0.1. A request that arrives on
// loopback carrying a forwarding header came through that proxy and is
// somebody on the internet.
//
// It used to be called IsNodeLocal and was the whole of the check on the ACME
// DNS-01 endpoints, so any process on the node could publish a TXT record and
// be issued a certificate for any name under the cluster's domains. Those
// endpoints now require a MAC (ACMEChallengeKey).
func ReachedWithoutPublicProxy(r *http.Request) bool {
	if r == nil {
		return false
	}
	if IsWireGuardPeer(r.RemoteAddr) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	return strings.TrimSpace(r.Header.Get("X-Forwarded-For")) == ""
}
