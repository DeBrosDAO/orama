//go:build !linux

package main

import (
	"errors"
	"net"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// errPeerCredsLinuxOnly is why identifyCaller refuses on every other system.
var errPeerCredsLinuxOnly = errors.New("peer credentials are only read on Linux")

// identifyCaller is Linux-only; the helper runs only on nodes.
func identifyCaller(*net.UnixConn) (privhelper.Caller, error) {
	return privhelper.Caller{}, errPeerCredsLinuxOnly
}
