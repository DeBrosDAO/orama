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

// IsNodeLocal reports whether a request came from a process on this host or
// from a node on the overlay — and never from the internet.
//
// Loopback alone is not evidence of a local caller, and that is the whole
// point: Caddy terminates TLS and reverse-proxies **every path** to the gateway
// on localhost, so the source address of every public request is 127.0.0.1. A
// request that arrives on loopback carrying a forwarding header came through
// that proxy and is somebody on the internet; one with no forwarding header is
// a process on this machine.
//
// This is the same distinction the rate limiter draws, for the same reason. An
// endpoint that trusted loopback on its own would be open to the world.
func IsNodeLocal(r *http.Request) bool {
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
