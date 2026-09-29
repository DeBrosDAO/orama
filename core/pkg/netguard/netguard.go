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
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// Ranges are the CIDRs refused beyond what net.IP's own predicates cover. Keep one CIDR per line and
// nothing else in the list (::/96 is IPv4-compatible and ::ffff:0:0/96 IPv4-mapped, both embedding an IPv4 host): the cross-check test reads them from this source file.
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
	"::/96",
	"::ffff:0:0/96",
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

// BlockedError is what a refused dial returns. It names the address so a caller can see which
// destination was refused.
type BlockedError struct{ Address string }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("destination %s is on an internal network and is not reachable from here", e.Address)
}

// GuardAddress is a net.Dialer.Control: it refuses a connection to a reserved address. Control runs
// after resolution with the concrete address the socket is about to connect to, once per attempt,
// for every address the resolver returned and for every hop of a redirect, so a name that resolves
// (or is rebound) to an internal address is refused at the connection itself. An address it cannot
// parse is refused too.
func GuardAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return &BlockedError{Address: address}
	}
	ip := net.ParseIP(host)
	if ip == nil || Reserved(ip) {
		return &BlockedError{Address: address}
	}
	return nil
}

const dialTimeout = 10 * time.Second

// NewHTTPClient returns an HTTP client whose every connection is checked by GuardAddress. It is
// for requests a tenant controls the destination of.
func NewHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, Control: GuardAddress}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsutil.GetTLSConfig(),
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}
