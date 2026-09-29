// Package netguard is the one list of address ranges a tenant-reachable component must never
// connect to: loopback, private and link-local networks, carrier-grade NAT, the benchmarking and
// IETF-assignment blocks (the co-located chain namespace lives in 198.18.0.0/24), multicast and
// reserved space, and the IPv6 forms that embed an IPv4 host. Push base URLs, function egress and
// the anonymity tunnel all use it, so a range added for one is refused by all.
//
// chain/netclass classifies the same ranges for the chain module, which core cannot import; a test
// there (TestNetclassRangesAreCoveredByCoreNetguard) reads Ranges from this file and fails when the
// two drift.
package netguard

import (
	"net"
	"net/netip"
)

// Ranges are the CIDRs refused beyond what net.IP's own predicates cover. Keep one CIDR per line and
// nothing else in the list: the cross-check test reads them from this source file.
var Ranges = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"3fff::/20",
	"5f00::/16",
	"fc00::/7",
	"fe80::/10",
	"fec0::/10",
	"ff00::/8",
}

var prefixes = func() []netip.Prefix {
	out := make([]netip.Prefix, len(Ranges))
	for i, c := range Ranges {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}()

// Reserved reports whether ip is in a range no tenant-reachable connection may target. A nil or
// unparseable address is reserved: an address that cannot be checked is not one that may be used.
// An IPv4-mapped IPv6 address is judged as its IPv4 form.
func Reserved(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
