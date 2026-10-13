package hostfunctions

import (
	"net"
	"testing"
)

func TestBlockedIP_usesTheSharedReservedList(t *testing.T) {
	for _, s := range []string{"198.18.0.2", "192.0.0.8", "10.0.0.1", "100.64.0.1", "192.31.196.1"} {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s must be blocked", s)
		}
	}
	if blockedIP(net.ParseIP("8.8.8.8")) {
		t.Error("a public address was blocked")
	}
}
