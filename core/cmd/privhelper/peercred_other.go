//go:build !linux

package main

import (
	"fmt"
	"net"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// identifyCaller is Linux-only; the helper runs only on nodes.
func identifyCaller(*net.UnixConn) (privhelper.Caller, error) {
	return privhelper.Caller{}, fmt.Errorf("peer credentials are only read on Linux")
}
