//go:build !linux && !darwin

package pubsub

import (
	"fmt"
	"net"
)

// peerUID cannot be read here, so every connection is refused.
func peerUID(*net.UnixConn) (uint32, error) {
	return 0, fmt.Errorf("peer credentials are not read on this platform")
}
