package storageclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/netguard"
)

// ErrNotPublic is a provider address that resolves to loopback, a private
// or link-local range, or an unspecified address. Provider endpoints come
// from x/nodes, which any registered node writes, so they are not trusted
// to point inside this machine's network.
var ErrNotPublic = errors.New("provider address is not a public address")

const dialTimeout = 10 * time.Second

// PublicHTTPClient dials only public addresses and follows no redirects.
func PublicHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: dialTimeout, Control: refuseNonPublic}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// refuseNonPublic runs on the resolved address, after DNS, so a name that
// resolves inward is refused too.
func refuseNonPublic(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !IsPublic(ip) {
		return fmt.Errorf("%w: %s", ErrNotPublic, host)
	}
	return nil
}

// IsPublic reports whether ip is a public address: not in any range of the shared list in
// pkg/netguard.
func IsPublic(ip net.IP) bool { return !netguard.Reserved(ip) }
