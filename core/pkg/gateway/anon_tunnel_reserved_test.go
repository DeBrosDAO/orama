package gateway

import (
	"net"
	"testing"
)

func TestIsPublicIP_refusesTheChainNamespaceAndBenchmarkingBlocks(t *testing.T) {
	for _, s := range []string{"198.18.0.2", "198.19.0.1", "192.0.0.8", "100.64.0.1", "10.0.0.1"} {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be a public tunnel destination", s)
		}
	}
	if !isPublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("a public address was refused")
	}
}
