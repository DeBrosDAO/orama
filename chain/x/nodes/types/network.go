package types

import (
	"fmt"
	"net"
)

const (
	// ipv6NetworkBits is the prefix length an IPv6 endpoint is grouped by. A
	// /16 would put a large share of the IPv6 space in one bucket, so IPv6
	// nodes are grouped by their /32 allocation instead.
	ipv6NetworkBits = 32
	ipv4NetworkBits = 16

	// The ASN ranges below are reserved by IANA: AS_TRANS (RFC 6793),
	// documentation (RFC 5398), private use (RFC 6996) and the last value of
	// each block (RFC 7300).
	asnTrans          = 23456
	asnDocFirst16     = 64496
	asnDocLast16      = 64511
	asnPrivateFirst16 = 64512
	asnDocFirst32     = 65536
	asnDocLast32      = 65551
	asnPrivateFirst32 = 4200000000
)

// ValidateASN checks a declared autonomous system number. Zero means
// "undeclared" and is handled by the caller. Reserved, documentation and
// private-use numbers are refused: they identify no real network, so a node
// declaring one would carry no diversity information.
func ValidateASN(asn uint32) error {
	switch {
	case asn == 0:
		return fmt.Errorf("asn 0 is reserved")
	case asn == asnTrans:
		return fmt.Errorf("asn %d (AS_TRANS) is reserved", asn)
	case asn >= asnDocFirst16 && asn <= asnDocLast16:
		return fmt.Errorf("asn %d is reserved for documentation", asn)
	case asn >= asnPrivateFirst16 && asn < asnDocFirst32:
		return fmt.Errorf("asn %d is reserved for private use or is the last 16-bit value", asn)
	case asn >= asnDocFirst32 && asn <= asnDocLast32:
		return fmt.Errorf("asn %d is reserved for documentation", asn)
	case asn >= asnPrivateFirst32:
		return fmt.Errorf("asn %d is reserved for private use or is the last 32-bit value", asn)
	}
	return nil
}

// NetworkOf derives a node's network group from its endpoints: the /16 of the
// first endpoint whose host is a literal IPv4 address ("a.b.0.0/16"), or the
// /32 of a literal IPv6 address. Hostnames are skipped because the chain cannot
// resolve DNS deterministically. It returns "" when no endpoint carries a
// literal IP. Endpoints were already validated as public by ValidateEndpoints.
func NetworkOf(endpoints []string) string {
	for _, ep := range endpoints {
		hosts, err := endpointHosts(ep)
		if err != nil {
			continue
		}
		for _, host := range hosts {
			ip := net.ParseIP(host)
			if ip == nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				mask := net.CIDRMask(ipv4NetworkBits, 32)
				return (&net.IPNet{IP: v4.Mask(mask), Mask: mask}).String()
			}
			mask := net.CIDRMask(ipv6NetworkBits, 128)
			return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
		}
	}
	return ""
}

// LiteralIPs returns the distinct literal IP hosts among endpoints, normalised (an IPv4-mapped
// IPv6 address becomes its IPv4 form), in first-seen order. Hostnames are skipped: the chain
// cannot resolve DNS deterministically.
func LiteralIPs(endpoints []string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, ep := range endpoints {
		hosts, err := endpointHosts(ep)
		if err != nil {
			continue
		}
		for _, host := range hosts {
			ip := net.ParseIP(host)
			if ip == nil {
				continue
			}
			key := ip.String()
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	return out
}

// IdentityEffective reports whether a network identity that last changed at sinceUnix has stood
// for at least lockSeconds at nowUnix. A lock of 0 or less is off.
func IdentityEffective(sinceUnix, nowUnix, lockSeconds int64) bool {
	return lockSeconds <= 0 || nowUnix-sinceUnix >= lockSeconds
}
