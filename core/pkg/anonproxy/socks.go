// Package anonproxy dials the internet through the node's anonymity backend:
// the client-only Tor daemon listening on constants.TorSOCKSAddr().
//
// Every connection goes through the SOCKS port, whatever its destination.
// There is no direct path: a caller that asked for anonymised egress must never
// be downgraded to the node's own address, and a redirect to a private address
// is refused by Tor itself (ClientRejectInternalAddresses) instead of being
// dialled onto this node's network.
package anonproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	goproxy "golang.org/x/net/proxy"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// runningProbeTimeout bounds the TCP probe Running makes against the SOCKS
// port. The port is on loopback, so anything slower means it is not there.
const runningProbeTimeout = 200 * time.Millisecond

// Address returns the SOCKS5 address of the node's Tor client.
func Address() string { return constants.TorSOCKSAddr() }

// Running reports whether the Tor SOCKS port accepts connections.
func Running() bool { return socksReachable(Address()) }

func socksReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, runningProbeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// NewHTTPClient returns an *http.Client whose every connection is made through
// the Tor SOCKS5 proxy. Host names are handed to the proxy unresolved, so the
// Tor exit performs DNS and this node's resolver never sees the destination.
func NewHTTPClient() *http.Client { return newHTTPClient(Address()) }

func newHTTPClient(socksAddr string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSOCKS(ctx, socksAddr, network, addr, nil)
		},
	}}
}

// DialThrough opens a TCP connection to addr ("host:port") through the Tor
// SOCKS5 proxy, for the authenticated tunnelling proxy (bugboard #168).
//
// isolationKey selects the circuit. Tor isolates streams by SOCKS credentials
// (IsolateSOCKSAuth, set explicitly in the Orama torrc), so passing a distinct
// value per end user gives each user their own circuit and therefore their own
// exit. Without it every tunnel on a node would share one circuit: one user's
// traffic would be linkable to another's at the exit, and one slow circuit
// would degrade everybody. The value is an opaque identifier — never a wallet
// address or anything else that identifies the user to the exit, which sees
// only that two streams differ.
//
// The hostname is passed to the proxy UNRESOLVED so the exit performs DNS.
// Resolving locally would leak the destination to this node's resolver and, for
// a name pointing at a private address, would aim the dial at our own network.
func DialThrough(ctx context.Context, addr, isolationKey string) (net.Conn, error) {
	return dialThrough(ctx, Address(), addr, isolationKey)
}

func dialThrough(ctx context.Context, socksAddr, addr, isolationKey string) (net.Conn, error) {
	var auth *goproxy.Auth
	if isolationKey != "" {
		// Tor's SOCKS port is unauthenticated; the pair exists only as the
		// isolation token, so the password carries no meaning.
		auth = &goproxy.Auth{User: isolationKey, Password: isolationKey}
	}
	return dialSOCKS(ctx, socksAddr, "tcp", addr, auth)
}

// dialSOCKS connects to addr through the SOCKS5 proxy at socksAddr, honouring
// ctx for both the dial to the proxy and the proxied CONNECT.
func dialSOCKS(ctx context.Context, socksAddr, network, addr string, auth *goproxy.Auth) (net.Conn, error) {
	base := &net.Dialer{}
	if deadline, ok := ctx.Deadline(); ok {
		base.Timeout = time.Until(deadline)
		if base.Timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
	}
	dialer, err := goproxy.SOCKS5("tcp", socksAddr, auth, base)
	if err != nil {
		return nil, fmt.Errorf("build SOCKS5 dialer for %s: %w", socksAddr, err)
	}
	cd, ok := dialer.(goproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS5 dialer for %s does not support contexts", socksAddr)
	}
	conn, err := cd.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s through Tor SOCKS5 at %s: %w", addr, socksAddr, err)
	}
	return conn, nil
}
