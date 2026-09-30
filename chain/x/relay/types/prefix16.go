package types

import (
	"fmt"
	"net"
	"net/netip"
)

// UnidentifiedPrefix16 is the one shared bucket for relays whose node has no
// identified network (no literal-IP endpoint, or an identity still inside
// x/nodes' network_identity_lock_seconds). Every such relay shares a single
// per_prefix16_cap, matching x/nodes' "counts as unidentified" convention.
const UnidentifiedPrefix16 = "unidentified"

// RelayPrefix16 maps the network x/nodes reports for a node (a canonical
// "A.B.0.0/16", or "" when unidentified) to the bucket the per-/16 cap uses.
func RelayPrefix16(network string) (string, error) {
	if network == "" {
		return UnidentifiedPrefix16, nil
	}
	// The cap buckets IPv4 /16s. A node identified by an IPv6 network (x/nodes reports a /32 for a
	// literal IPv6 endpoint) has no IPv4 /16, so it shares the unidentified bucket rather than being
	// refused a relay.
	if prefix, err := netip.ParsePrefix(network); err == nil && prefix.Addr().Is6() && !prefix.Addr().Is4In6() {
		return UnidentifiedPrefix16, nil
	}
	return CanonicalPrefix16(network)
}

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
