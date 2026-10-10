package config

import (
	"strings"
	"testing"
)

func TestDNSConfig_decodesNodeNamesZone(t *testing.T) {
	var cfg Config
	err := DecodeStrict(strings.NewReader("dns:\n  node_names_zone: stagenet.orama.network\n"), &cfg)
	if err != nil || cfg.DNS.NodeNamesZone != "stagenet.orama.network" {
		t.Fatalf("zone %q err %v", cfg.DNS.NodeNamesZone, err)
	}
	var empty Config
	if err := DecodeStrict(strings.NewReader("node:\n  id: n\n"), &empty); err != nil || empty.DNS.NodeNamesZone != "" {
		t.Fatalf("a node.yaml with no dns block: zone %q err %v", empty.DNS.NodeNamesZone, err)
	}
}

func TestConfigValidate_nodeNamesZone(t *testing.T) {
	cfg := validConfigForNode()
	cfg.HTTPGateway.BaseDomain = "stagenet.orama.network"
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("a config with no node names zone: %v", errs)
	}
	cfg.DNS.NodeNamesZone = "stagenet.orama.network"
	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("the cluster's own zone: %v", errs)
	}
	for _, zone := range []string{"testnet.orama.network", "BAD", "orama.network"} {
		cfg.DNS.NodeNamesZone = zone
		errs := cfg.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "dns.node_names_zone") {
			t.Errorf("zone %q: errs = %v", zone, errs)
		}
	}
}
