package validate

import (
	"strings"
	"testing"
)

func TestValidateZone(t *testing.T) {
	for _, zone := range []string{"stagenet.orama.network", "orama.network", "a-b.example.com"} {
		if err := ValidateZone(zone); err != nil {
			t.Errorf("ValidateZone(%q) = %v", zone, err)
		}
	}
	for _, zone := range []string{
		"", "network", "Stagenet.orama.network", "stagenet.orama.network.", "203.0.113.5", "a..b",
		"-a.example.com", "a-.example.com", "a_b.example.com", "exa mple.com", strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 130) + "com",
	} {
		if err := ValidateZone(zone); err == nil {
			t.Errorf("ValidateZone(%q) succeeded", zone)
		}
	}
}

func TestZoneServedBy(t *testing.T) {
	for zone, want := range map[string]bool{
		"stagenet.orama.network":       true,
		"names.stagenet.orama.network": true,
		"orama.network":                false,
		"xstagenet.orama.network":      false,
		"other.example":                false,
	} {
		if got := ZoneServedBy(zone, "stagenet.orama.network"); got != want {
			t.Errorf("ZoneServedBy(%q) = %v, want %v", zone, got, want)
		}
	}
	if ZoneServedBy("stagenet.orama.network", "") {
		t.Error("a cluster with no base domain serves a zone")
	}
}

func TestValidateDNS(t *testing.T) {
	const base = "stagenet.orama.network"
	for name, c := range map[string]DNSConfig{
		"disabled":          {BaseDomain: base},
		"disabled, no base": {},
		"the base domain":   {NodeNamesZone: base, BaseDomain: base},
		"below the base":    {NodeNamesZone: "names." + base, BaseDomain: base},
	} {
		if errs := ValidateDNS(c); len(errs) != 0 {
			t.Errorf("%s: %v", name, errs)
		}
	}
	for name, c := range map[string]DNSConfig{
		"a malformed zone":       {NodeNamesZone: "Stagenet.Orama.Network", BaseDomain: base},
		"another cluster zone":   {NodeNamesZone: "testnet.orama.network", BaseDomain: base},
		"the parent of the base": {NodeNamesZone: "orama.network", BaseDomain: base},
		"no base domain":         {NodeNamesZone: base},
	} {
		errs := ValidateDNS(c)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "dns.node_names_zone") {
			t.Errorf("%s: errs = %v, want one naming dns.node_names_zone", name, errs)
		}
	}
}
