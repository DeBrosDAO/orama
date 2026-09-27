package gateway

import "testing"

func TestTenantGatewayProbeHost_usesTheWireGuardAddress(t *testing.T) {
	host, ok := tenantGatewayProbeHost("10.0.0.2")
	if !ok || host != "10.0.0.2" {
		t.Fatalf("probe host = %q ok=%v, want 10.0.0.2", host, ok)
	}
}

func TestTenantGatewayProbeHost_doesNotSubstituteLoopback(t *testing.T) {
	for _, in := range []string{"", " ", "\t"} {
		host, ok := tenantGatewayProbeHost(in)
		if ok || host != "" {
			t.Fatalf("input %q: probe host = %q ok=%v, want no probe", in, host, ok)
		}
	}
}
