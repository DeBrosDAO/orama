package clusterreg

import "testing"

// Validate says it checks what x/nodes ValidateBasic checks, including that every endpoint is a
// public address. The chain refuses these (chain/netclass.CheckHost); a check here that let them
// through would have the wallet build, sign and submit a registration that fails on the chain.
// Found by FuzzClusterregRegistration.
func TestValidate_refusesEndpointsTheChainRefuses(t *testing.T) {
	operator := "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	for _, endpoint := range []string{
		"100.64.0.1",                  // carrier-grade NAT
		"198.18.0.1",                  // benchmarking, where the co-located chain namespace lives
		"192.0.2.1",                   // documentation
		"https://[fe80::1%25eth0]/",   // a zone id
		"[2606:4700::1111%eth0]:443",  // a zone id
		"127.1", "0x7f.1", "01.2.3.4", // a number a resolver reads as an IPv4 address
		"2130706433", "4294967295",
		"gw.example.123",
		"/ip4/100.64.0.1/tcp/4001",
		"::ffff:10.0.0.1",
		"64:ff9b::7f00:1",
		".", // no host once the root dot is dropped
	} {
		err := Validate(Registration{
			Operator: operator, ClusterID: "cluster-1", BaseDomain: "example.com", Endpoints: []string{endpoint},
		})
		if err == nil {
			t.Errorf("endpoint %q was accepted", endpoint)
		}
	}
}

func TestValidate_acceptsPublicEndpoints(t *testing.T) {
	operator := "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	for _, endpoint := range []string{
		"93.184.216.34", "93.184.216.34:443", "https://gw.example.com:8443", "gw.example.com.",
		"/ip4/93.184.216.34/tcp/4001", "[2606:4700::1111]:443", "https://[2606:4700::1111]/",
	} {
		err := Validate(Registration{
			Operator: operator, ClusterID: "cluster-1", BaseDomain: "example.com", Endpoints: []string{endpoint},
		})
		if err != nil {
			t.Errorf("endpoint %q was refused: %v", endpoint, err)
		}
	}
}
