package repair

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrNotPublic is a provider endpoint that resolves to loopback, a private
// or link-local range, or an unspecified address. Endpoints come from
// x/nodes, which any registered node writes, so the delegate does not let
// them point at services on its own host or network.
var ErrNotPublic = errors.New("provider address is not a public address")

const dialTimeout = 10 * time.Second

// cgnat is 100.64.0.0/10, shared address space that is not public.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

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
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) {
		return fmt.Errorf("%w: %s", ErrNotPublic, host)
	}
	return nil
}
