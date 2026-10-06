package repair

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/chain/netclass"
)

// ErrNotPublic is a provider endpoint that resolves to loopback, a private, shared, link-local,
// documentation, benchmarking, reserved, multicast, NAT64 or unique-local range, or an
// unspecified address (netclass.IsPublic decides). Endpoints come from
// x/nodes, which any registered node writes, so the delegate does not let
// them point at services on its own host or network.
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
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func refuseNonPublic(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrNotPublic, address, err)
	}
	if !netclass.IsPublic(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrNotPublic, ap.Addr())
	}
	return nil
}
