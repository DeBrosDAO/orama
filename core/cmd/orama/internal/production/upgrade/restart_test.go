package upgrade

import (
	"reflect"
	"testing"
)

// The supervisor's own stack is started by orama-node on the new binaries and
// is not restarted again after the health gate; a tenant namespace's units are.
func TestTenantServices_leavesTheSupervisorsStackAlone(t *testing.T) {
	services := []string{
		"orama-node",
		"orama-namespace-rqlite@index",
		"orama-namespace-gateway@index",
		"orama-namespace-ipfs-gc@index.timer",
		"orama-namespace-coredns@nameserver",
		"orama-namespace-coredns@nameserver.service",
		"wg-quick@wg0",
		"orama-namespace-rqlite@acme",
		"orama-namespace-gateway@acme",
		"orama-namespace-gateway@indexer",
	}
	want := []string{"wg-quick@wg0", "orama-namespace-rqlite@acme", "orama-namespace-gateway@acme", "orama-namespace-gateway@indexer"}
	if got := tenantServices(services); !reflect.DeepEqual(got, want) {
		t.Fatalf("tenantServices = %v, want %v", got, want)
	}
}

func TestTenantServices_aNodeWithNoTenantsRestartsNothing(t *testing.T) {
	if got := tenantServices([]string{"orama-node", "orama-namespace-olric@index.service"}); len(got) != 0 {
		t.Fatalf("tenantServices = %v, want none", got)
	}
	if got := tenantServices(nil); len(got) != 0 {
		t.Fatalf("tenantServices(nil) = %v, want none", got)
	}
}
