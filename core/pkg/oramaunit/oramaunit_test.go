package oramaunit

import (
	"slices"
	"testing"
)

func TestIs(t *testing.T) {
	for name, want := range map[string]bool{
		"orama-node.service":                         true,
		"orama-namespace-ipfs-cluster@index.service": true,
		"wg-quick@wg0.service":                       true,
		"caddy.service":                              true,
		"coredns.service":                            true,
		"cloud-init.service":                         false,
		"e2e-fail-ab12.service":                      false,
		"":                                           false,
	} {
		if got := Is(name); got != want {
			t.Errorf("Is(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestFilter_keepsOnlyOramasInOrder(t *testing.T) {
	got := Filter([]string{"cloud-init.service", "orama-node.service", "rpcbind.service", "caddy.service"})
	if !slices.Equal(got, []string{"orama-node.service", "caddy.service"}) {
		t.Fatalf("Filter = %v", got)
	}
	if Filter(nil) != nil {
		t.Fatal("Filter(nil) is not empty")
	}
}
