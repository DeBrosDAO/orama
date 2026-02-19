package auth

import "net"

// WireGuardSubnet is the internal WireGuard mesh CIDR.
const WireGuardSubnet = "10.0.0.0/24"

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
