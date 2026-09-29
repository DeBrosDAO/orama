//go:build !linux

package globalnetns

import (
	"context"
	"fmt"
	"net"
)

// InNamespace is Linux only.
func InNamespace(path string, fn func() error) error {
	return fmt.Errorf("network namespaces are a Linux feature; cannot join %s", path)
}

// DialContext is Linux only.
func DialContext(path string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("network namespaces are a Linux feature; cannot join %s", path)
	}
}
