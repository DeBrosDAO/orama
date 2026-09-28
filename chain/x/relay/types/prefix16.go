package types

import (
	"fmt"
	"net"
)

// CanonicalPrefix16 maps an IPv4 address or CIDR to the /16 that contains it,
// "A.B.0.0/16". IPv6 is rejected. The per-/16 cap buckets on this string.
func CanonicalPrefix16(raw string) (string, error) {
	if ip := net.ParseIP(raw); ip != nil {
		ip4 := ip.To4()
		if ip4 == nil {
			return "", fmt.Errorf("prefix /16: %q is not ipv4", raw)
		}
		return fmt.Sprintf("%d.%d.0.0/16", ip4[0], ip4[1]), nil
	}
	ip, network, err := net.ParseCIDR(raw)
	if err != nil {
		return "", fmt.Errorf("prefix /16: %q: %w", raw, err)
	}
	ip4 := ip.To4()
	if ip4 == nil || network.IP.To4() == nil {
		return "", fmt.Errorf("prefix /16: %q is not ipv4", raw)
	}
	return fmt.Sprintf("%d.%d.0.0/16", ip4[0], ip4[1]), nil
}
