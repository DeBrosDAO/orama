package hostfunctions

import (
	"context"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/netguard"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// Outbound HTTP from a WASM function is a request the tenant controls, made by
// a process that sits on the cluster's own network. The guard used to be a
// check on the URL string: it parsed the host, and if the host was not an IP
// literal it returned nil. So `http://rqlite.internal/` passed, and so did any
// name the tenant controlled that resolved to 10.0.0.x — the overlay carrying
// rqlite, Olric and every other namespace's services.
//
// Checking the name cannot work. A name is not an address, the resolver decides
// what it becomes, the answer can change between the check and the connection,
// and a redirect can send the client somewhere the first URL never named. The
// check belongs where the address is finally known: the dial.
//
// net.Dialer.Control runs after resolution with the concrete address the socket
// is about to connect to, once per attempt, for every address the resolver
// returned and for every hop of a redirect. Refusing there refuses the
// connection itself.

// blockedIP reports whether an address belongs to a range tenant code has no
// business reaching from a cluster node.
func blockedIP(ip net.IP) bool { return netguard.Reserved(ip) }

// errBlockedDestination is what a refused dial returns. It names the address so
// a function author can see which destination was refused, and says why.
type errBlockedDestination struct{ address string }

func (e *errBlockedDestination) Error() string {
	return fmt.Sprintf("destination %s is on an internal network and is not reachable from a function", e.address)
}

// guardEgressAddress refuses a connection to an internal address.
//
// It is used as net.Dialer.Control, which receives the address the socket is
// about to connect to — already resolved, one call per attempt.
func guardEgressAddress(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Control is documented to receive host:port. Anything else is a shape
		// this guard does not understand, and an address it cannot check is
		// not an address it may allow.
		return &errBlockedDestination{address: address}
	}
	ip := net.ParseIP(host)
	if ip == nil || blockedIP(ip) {
		return &errBlockedDestination{address: address}
	}
	return nil
}

// egressDialTimeout bounds one connection attempt. The client's own timeout
// bounds the whole request; this keeps a single unreachable address from
// spending all of it.
const egressDialTimeout = 10 * time.Second

// newGuardedHTTPClient returns the client used for a function's outbound HTTP,
// with every dial checked against guardEgressAddress.
func newGuardedHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   egressDialTimeout,
		KeepAlive: 30 * time.Second,
		Control:   guardEgressAddress,
	}
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
