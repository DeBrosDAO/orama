package hostfunctions

import (
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/netguard"
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

// errBlockedDestination is what a refused dial returns (see netguard.BlockedError).
type errBlockedDestination = netguard.BlockedError

// guardEgressAddress refuses a connection to an internal address; it is used as
// net.Dialer.Control (see netguard.GuardAddress).
func guardEgressAddress(network, address string, c syscall.RawConn) error {
	return netguard.GuardAddress(network, address, c)
}

// newGuardedHTTPClient returns the client used for a function's outbound HTTP,
// with every dial checked against the shared reserved-range list.
func newGuardedHTTPClient(timeout time.Duration) *http.Client { return netguard.NewHTTPClient(timeout) }
