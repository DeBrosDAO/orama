package enroll

import "net"

// reservedNodeIP reports whether ip is not a usable public endpoint for an
// OramaOS node. The value is stored as wireguard_peers.public_ip (the
// Endpoint of every other node) and is the target of this gateway's outbound
// enroll push, so loopback, RFC1918, link-local, CGNAT and unspecified
// addresses are both a broken mesh endpoint and an SSRF.
func reservedNodeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 — carrier-grade NAT (not covered by IsPrivate).
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
