//go:build !linux

package ipfs

import (
	"errors"
	"net/netip"
)

// sockDiagOwner has no kernel to ask outside Linux, where the proxy runs; it
// refuses rather than admit a connection it cannot attribute.
func sockDiagOwner(netip.AddrPort, netip.AddrPort) (uint32, error) {
	return 0, errors.New("socket owners can only be read on Linux")
}
