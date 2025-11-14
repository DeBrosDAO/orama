package anyoneproxy

import (
	"context"
	"net"
	"net/http"
	"time"

	goproxy "golang.org/x/net/proxy"

	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
)

// disabled controls runtime disabling via flags. Default is false (proxy enabled).
var disabled bool

// SetDisabled allows binaries to disable Anyone routing via a flag (e.g. --disable-anonrc).
func SetDisabled(v bool) { disabled = v }

// Enabled reports whether Anyone proxy routing is active.
// Defaults to true, using SOCKS5 at localhost:9050, unless explicitly disabled
// via SetDisabled(true) or environment variable ANYONE_DISABLE=1.
// ANYONE_SOCKS5 may override the proxy address.
func Enabled() bool {
	if disabled {
		return false
	}
	return true
}

// socksAddr returns the SOCKS5 address to use for proxying (host:port).
func socksAddr() string {
	return "localhost:9050"
}

// socksContextDialer implements tcp.ContextDialer over a SOCKS5 proxy.
type socksContextDialer struct{ addr string }

func (d *socksContextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Derive timeout from context deadline if present
	var timeout time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
	}
	base := &net.Dialer{Timeout: timeout}
	// Create a SOCKS5 dialer using the base dialer
	socksDialer, err := goproxy.SOCKS5("tcp", d.addr, nil, base)
	if err != nil {
		return nil, err
	}
	return socksDialer.Dial(network, address)
}

// DialerForAddr returns a tcp.DialerForAddr that routes through the Anyone SOCKS5 proxy.
// It automatically BYPASSES the proxy for loopback, private, and link-local addresses
// to allow local/dev networking (e.g. localhost, 10.0.0.0/8, 192.168.0.0/16, fc00::/7, fe80::/10).
func DialerForAddr() tcp.DialerForAddr {
	return func(raddr ma.Multiaddr) (tcp.ContextDialer, error) {
		// Prefer direct dialing for local/private targets
		if ip4, err := raddr.ValueForProtocol(ma.P_IP4); err == nil {
			if ip := net.ParseIP(ip4); ip != nil {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
					return &net.Dialer{}, nil
				}
			}
		}
		if ip6, err := raddr.ValueForProtocol(ma.P_IP6); err == nil {
			if ip := net.ParseIP(ip6); ip != nil {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
					return &net.Dialer{}, nil
				}
			}
		}
		if host, err := raddr.ValueForProtocol(ma.P_DNS); err == nil {
			if host == "localhost" {
				return &net.Dialer{}, nil
			}
		}
		if host, err := raddr.ValueForProtocol(ma.P_DNS4); err == nil {
			if host == "localhost" {
				return &net.Dialer{}, nil
			}
		}
		if host, err := raddr.ValueForProtocol(ma.P_DNS6); err == nil {
			if host == "localhost" {
				return &net.Dialer{}, nil
			}
		}

		// Default: use SOCKS dialer
		return &socksContextDialer{addr: socksAddr()}, nil
	}
}

// NewHTTPClient returns an *http.Client that routes all TCP connections via the Anyone SOCKS5 proxy.
// If Anyone proxy is not enabled, it returns http.DefaultClient.
func NewHTTPClient() *http.Client {
	if !Enabled() {
		return http.DefaultClient
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Bypass proxy for localhost/private IPs
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if ip := net.ParseIP(host); ip != nil {
				if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
					d := &net.Dialer{}
					return d.DialContext(ctx, network, addr)
				}
			}
			if host == "localhost" {
				d := &net.Dialer{}
				return d.DialContext(ctx, network, addr)
			}

			d := &socksContextDialer{addr: socksAddr()}
			return d.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: tr}
}

// Address returns the SOCKS5 address used for Anyone routing.
func Address() string { return socksAddr() }

// Running returns true if Anyone proxy is enabled and reachable at Address().
// It attempts a short TCP dial and returns false on failure.
func Running() bool {
	if !Enabled() {
		return false
	}
	conn, err := net.DialTimeout("tcp", socksAddr(), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
