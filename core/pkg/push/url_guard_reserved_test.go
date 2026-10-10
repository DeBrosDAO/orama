package push

import (
	"net"
	"testing"
)

// The co-located chain namespace (198.18.0.0/24) and the IETF assignment block are not valid push
// targets: the shared netguard list covers them.
func TestIsReservedIP_coversTheBenchmarkingAndIETFBlocks(t *testing.T) {
	for _, s := range []string{"198.18.0.2", "198.19.1.1", "192.0.0.8"} {
		if !isReservedIP(net.ParseIP(s)) {
			t.Errorf("%s must be reserved", s)
		}
		if err := CheckBaseURLSyntax("http://" + s + ":8080"); err == nil {
			t.Errorf("a push base URL at %s was accepted", s)
		}
	}
}
