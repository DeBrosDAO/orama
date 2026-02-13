package gateway

import (
	"net"

	"golang.org/x/net/netutil"
)

const (
	// DefaultMaxConnections is the maximum number of concurrent connections per server.
	DefaultMaxConnections = 10000
)

// LimitedListener wraps a net.Listener with a maximum concurrent connection limit.
// When the limit is reached, new connections block until an existing one closes.
func LimitedListener(l net.Listener, maxConns int) net.Listener {
	if maxConns <= 0 {
		maxConns = DefaultMaxConnections
	}
	return netutil.LimitListener(l, maxConns)
}
