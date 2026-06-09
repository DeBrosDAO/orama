package push

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// url_guard.go — SSRF guard for TENANT-supplied push base URLs.
//
// A tenant can override the ntfy base URL the gateway POSTs to (BYO-ntfy is a
// legitimate use case). Without a guard, a tenant could point it at an internal
// address — cloud metadata (169.254.169.254), the WireGuard mesh (10.0.0.x),
// loopback — turning the gateway's push sender into an SSRF proxy. These checks
// reject internal/reserved targets while still allowing real external hosts.
//
// IMPORTANT: apply these ONLY to tenant-supplied base URLs (the per-namespace
// override). The operator's gateway default (e.g. 127.0.0.1:8090, the local
// ntfy) is trusted and must NOT pass through here — it would be (correctly)
// rejected as loopback.

// baseURLDNSTimeout bounds the hostname-resolution step in CheckBaseURLResolvable.
const baseURLDNSTimeout = 5 * time.Second

// lookupIP resolves a host to its IPs. A package var so tests can substitute a
// deterministic resolver instead of touching real DNS.
var lookupIP = func(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

// CheckBaseURLSyntax validates a tenant base URL's scheme and rejects a host
// that is a LITERAL internal/reserved IP. It does NOT resolve hostnames, so it
// is safe to call on hot paths (e.g. per-send dispatcher construction). An
// empty base URL is allowed — it means "use the gateway default".
func CheckBaseURLSyntax(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("base_url: invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url: must start with http:// or https:// (got scheme %q)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("base_url: missing host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isReservedIP(ip) {
			return fmt.Errorf("base_url: host %s is a reserved/internal address and is not allowed", host)
		}
		return nil
	}
	// net.ParseIP only accepts canonical dotted-decimal / standard IPv6, but the
	// OS resolver + net.Dial ALSO accept decimal ("2130706433"), hex
	// ("0x7f000001") and octal ("0177.0.0.1") IPv4 encodings — a literal-check
	// bypass to internal addresses. Reject these non-standard numeric hosts
	// outright (no legitimate push host is all-numeric or 0x-hex).
	if looksLikeNumericHost(host) {
		return fmt.Errorf("base_url: host %q is a non-standard numeric/IP encoding and is not allowed", host)
	}
	return nil
}

// CheckBaseURLResolvable runs CheckBaseURLSyntax AND, when the host is a name
// rather than a literal IP, resolves it (bounded) and rejects if ANY resolved
// address is internal/reserved — blocking a tenant from pointing a domain at an
// internal host. It performs DNS, so call it ONLY at config-set time (the PUT
// handlers), never on the hot send path.
//
// Resolution failure FAILS OPEN (allowed): an unresolvable host reaches nothing
// (delivery would fail anyway), and rejecting it would break a legitimate host
// that's momentarily unresolvable at config time. The hard floor is
// CheckBaseURLSyntax's literal-IP block, which applies on every code path.
//
// Residual: as a set-time check it does not defend against DNS rebinding (the
// host re-pointing to an internal IP AFTER it was accepted). Closing that would
// require a send-time IP check, which is complicated here by the operator's
// loopback default ntfy.
func CheckBaseURLResolvable(ctx context.Context, baseURL string) error {
	if err := CheckBaseURLSyntax(baseURL); err != nil {
		return err
	}
	if baseURL == "" {
		return nil
	}
	u, _ := url.Parse(baseURL) // already validated by CheckBaseURLSyntax
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return nil // literal IP already vetted by CheckBaseURLSyntax
	}

	rctx, cancel := context.WithTimeout(ctx, baseURLDNSTimeout)
	defer cancel()
	ips, err := lookupIP(rctx, host)
	if err != nil || len(ips) == 0 {
		return nil // fail open on resolution failure (see doc)
	}
	for _, ip := range ips {
		if isReservedIP(ip) {
			return fmt.Errorf("base_url: host %q resolves to reserved/internal address %s and is not allowed", host, ip)
		}
	}
	return nil
}

// IsInternalBaseURL reports whether baseURL parses to a host that is a LITERAL
// internal/reserved IP. Malformed URLs and hostname URLs return false — this is
// the no-false-positive guard for hot paths (e.g. dispatcher build), where the
// goal is only to drop an internal-address override, not to re-validate syntax
// or do DNS (the set-path handlers cover those).
func IsInternalBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return isReservedIP(ip)
	}
	// Non-standard numeric encodings (decimal/hex/octal) that net.ParseIP misses
	// but net.Dial resolves to an IP — treat as internal so the build-path guard
	// matches what the dialer would actually reach.
	return looksLikeNumericHost(host)
}

// isReservedIP reports whether ip is in a range a tenant must never be able to
// reach via a push base URL: loopback, link-local (incl. 169.254.169.254 cloud
// metadata), RFC1918 private, ULA, unspecified, multicast, and 100.64/10 CGNAT.
func isReservedIP(ip net.IP) bool {
	if ip == nil {
		return true // unparseable → treat as unsafe
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 100.64.0.0/10 — carrier-grade NAT (not covered by IsPrivate). The
		// second-octet band [64,127] is the /10.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	} else if ip16 := ip.To16(); ip16 != nil {
		// NAT64 well-known prefix 64:ff9b::/96 (RFC 6052) embeds an IPv4 address
		// a NAT64 gateway would translate — so it can reach internal v4.
		if bytes.Equal(ip16[:12], []byte{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}) {
			return true
		}
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate() || // 10/8, 172.16/12, 192.168/16, fc00::/7
		ip.IsUnspecified()
}

// looksLikeNumericHost reports whether host is a non-standard numeric IPv4
// encoding — hex ("0x7f000001", "0x7f.0.0.1"), decimal ("2130706433"), or octal
// ("0177.0.0.1") — that net.ParseIP rejects but the OS resolver and net.Dial
// accept (resolving to a real, possibly internal, IPv4). Such hosts are never a
// legitimate push server name, so callers reject them rather than let them slip
// past the literal-IP guard. Hosts containing any letter (other than a leading
// "0x") are treated as ordinary DNS names and return false.
func looksLikeNumericHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(host), "0x") {
		return true // hex literal
	}
	// All-numeric (optionally dotted) host that net.ParseIP already failed to
	// accept: a decimal or octal IPv4 encoding (or a malformed all-numeric
	// dotted form). Either way, not a real hostname.
	for _, r := range host {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
