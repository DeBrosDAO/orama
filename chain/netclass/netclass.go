// Package netclass is the one classifier the chain uses to decide whether an endpoint host is a
// public address. x/nodes validation, the node network identity (LiteralIPs, NetworkOf), the
// LiveIPs uniqueness index and the repair fetcher all share it, so a host form that one of them
// reads as a name cannot be read as an address by another.
//
// net.ParseIP is deliberately not used: it accepts some forms and rejects others that resolvers
// and URL libraries read differently, and a host it calls a name (2130706433, 0x7f.1, 127.1,
// 010.0.0.1) can still reach an address through the OS resolver.
package netclass

import (
	"fmt"
	"net/netip"
	"strings"
)

// forbiddenHostChars are characters that never occur in a bare host: a zone id or its escaped
// form (%), a path, query or fragment (/ ? #), userinfo (@), and a backslash some URL parsers
// treat as a slash.
const forbiddenHostChars = "%/?#@\\ "

// special lists every range that is not globally routable unicast. IPv4-mapped IPv6 addresses are
// unmapped before they are checked, so ::ffff:10.0.0.1 is classified as 10.0.0.1.
var special = mustPrefixes(
	// IPv4.
	"0.0.0.0/8",       // "this network" (RFC 1122)
	"10.0.0.0/8",      // private (RFC 1918)
	"100.64.0.0/10",   // carrier-grade NAT (RFC 6598)
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local
	"172.16.0.0/12",   // private
	"192.0.0.0/24",    // IETF protocol assignments (RFC 6890)
	"192.0.2.0/24",    // documentation (TEST-NET-1)
	"192.88.99.0/24",  // deprecated 6to4 relay anycast
	"192.168.0.0/16",  // private
	"198.18.0.0/15",   // benchmarking (RFC 2544)
	"198.51.100.0/24", // documentation (TEST-NET-2)
	"203.0.113.0/24",  // documentation (TEST-NET-3)
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, including the limited broadcast 255.255.255.255
	// IPv6. Anything outside 2000::/3 is refused before this list is consulted.
	"64:ff9b::/96",   // NAT64 (RFC 6052): reaches whatever IPv4 host is embedded
	"64:ff9b:1::/48", // local-use NAT64 (RFC 8215)
	"100::/64",       // discard-only
	"2001::/23",      // IETF protocol assignments, including Teredo
	"2001:db8::/32",  // documentation
	"2002::/16",      // 6to4: embeds an IPv4 host
	"3fff::/20",      // documentation
	"5f00::/16",      // segment routing SIDs
	"fc00::/7",       // unique local
	"fe80::/10",      // link-local
	"fec0::/10",      // deprecated site-local
	"ff00::/8",       // multicast
)

var globalUnicastV6 = netip.MustParsePrefix("2000::/3")

func mustPrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrs))
	for i, c := range cidrs {
		out[i] = netip.MustParsePrefix(c)
	}
	return out
}

// IsPublic reports whether addr is a globally routable unicast address.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	if addr.Is6() && !globalUnicastV6.Contains(addr) {
		return false
	}
	for _, p := range special {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// Literal parses host as a strict literal IP address and returns it in canonical form (an
// IPv4-mapped IPv6 address becomes its IPv4 form). It reports false for anything else: a name, an
// obfuscated IPv4 (integer, hex, octal or short form), or an address with a zone. Brackets are not
// accepted; callers strip them from a URL host first.
func Literal(host string) (netip.Addr, bool) {
	if host == "" || strings.ContainsAny(host, forbiddenHostChars) {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// CheckHost refuses a host that is not a public name or public literal address: an empty host, a
// host with a character that cannot be part of a bare host, a local name, a literal in a
// non-public range, and any host that a resolver would read as an IPv4 address without being a
// strict dotted quad (its last label is a number: 2130706433, 0x7f.1, 127.1, 010.0.0.1).
func CheckHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	if strings.ContainsAny(host, forbiddenHostChars) {
		return fmt.Errorf("host %q contains a character that is not part of a host (zone id, path, query, userinfo or space)", host)
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "localhost" || strings.HasSuffix(name, ".localhost") || strings.HasSuffix(name, ".local") {
		return fmt.Errorf("host %q is not public", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !IsPublic(addr) {
			return fmt.Errorf("ip %s is not a public address", addr.Unmap())
		}
		return nil
	}
	if strings.Contains(host, ":") {
		return fmt.Errorf("host %q is not a valid IPv6 address", host)
	}
	if isNumericLabel(name[strings.LastIndex(name, ".")+1:]) {
		return fmt.Errorf("host %q ends in a number, so a resolver may read it as an obfuscated IPv4 address", host)
	}
	return nil
}

// isNumericLabel reports whether label is decimal digits or a 0x-prefixed hex number: the forms
// inet_aton and URL parsers accept for the last part of an IPv4 address.
func isNumericLabel(label string) bool {
	if label == "" {
		return false
	}
	if len(label) >= 2 && label[0] == '0' && (label[1] == 'x' || label[1] == 'X') {
		for _, c := range label[2:] {
			if !isHexDigit(c) {
				return false
			}
		}
		return true
	}
	for _, c := range label {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
