package main

import (
	"errors"
	"testing"
)

func fixedIP(ip string, err error) func() (string, error) {
	return func() (string, error) { return ip, err }
}

// The libp2p host listened on 0.0.0.0, the public interface included. It
// listens on the node's WireGuard address, and refuses to start without one
// rather than falling back to every interface.
func TestOverlayListenAddr(t *testing.T) {
	got, err := overlayListenAddr(fixedIP("10.0.0.7", nil))
	if err != nil || got != "/ip4/10.0.0.7/tcp/0" {
		t.Fatalf("got %q, %v", got, err)
	}
	for name, ip := range map[string]func() (string, error){
		"no wg0":            fixedIP("", errors.New("wg0 interface not found")),
		"public address":    fixedIP("203.0.113.7", nil),
		"unspecified":       fixedIP("0.0.0.0", nil),
		"outside the mesh":  fixedIP("10.1.0.7", nil),
		"not an IP address": fixedIP("wg0", nil),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := overlayListenAddr(ip); err == nil {
				t.Fatalf("listened on %q", got)
			}
		})
	}
}
