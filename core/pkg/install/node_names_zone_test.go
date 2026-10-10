package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"gopkg.in/yaml.v3"
)

// An upgrade regenerates node.yaml. The zone an operator wrote into it must survive, or the
// network's node names leave DNS at the next upgrade.
func TestNodeNamesZone_carriedForwardFromNodeYAML(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "node:\n  id: x\ndns:\n  node_names_zone: nodes.stagenet.orama.network\n")
	cg := NewConfigGenerator(dir)
	got, err := cg.NodeNamesZone()
	if err != nil || got != "nodes.stagenet.orama.network" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRegeneratedNodeYAML_keepsTheNodeNamesZoneAndStillParses(t *testing.T) {
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "node:\n  id: x\nhttp_gateway:\n  base_domain: stagenet.orama.network\ndns:\n  node_names_zone: nodes.stagenet.orama.network\n")
	rendered, err := NewConfigGenerator(dir).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.orama.network", "stagenet.orama.network", false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("regenerated node.yaml does not parse as config.Config: %v\n%s", err, rendered)
	}
	if cfg.DNS.NodeNamesZone != "nodes.stagenet.orama.network" {
		t.Fatalf("the zone was dropped by the regeneration: %q\n%s", cfg.DNS.NodeNamesZone, rendered)
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			if strings.Contains(e.Error(), "dns.node_names_zone") {
				t.Errorf("the regenerated zone fails validation: %v", e)
			}
		}
	}
}

func TestNodeNamesZone_fresh_none_andABrokenOrEditedFileIsAnError(t *testing.T) {
	if got, err := NewConfigGenerator(t.TempDir()).NodeNamesZone(); err != nil || got != "" {
		t.Fatalf("fresh install: %q, %v", got, err)
	}
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "dns: [unclosed\n")
	if _, err := NewConfigGenerator(dir).NodeNamesZone(); err == nil || !strings.Contains(err.Error(), "node_names_zone") {
		t.Fatalf("an unreadable node.yaml must be an error, got %v", err)
	}
	for _, bad := range []string{"Bad.Zone", "localhost", "zone.", "a b.example"} {
		dir := t.TempDir()
		writeACMENodeYAML(t, dir, "dns:\n  node_names_zone: \""+bad+"\"\n")
		if _, err := NewConfigGenerator(dir).NodeNamesZone(); err == nil {
			t.Errorf("zone %q was carried forward", bad)
		}
	}
}

func TestNodeYAML_withoutAZoneHasNoDNSBlock(t *testing.T) {
	rendered, err := NewConfigGenerator(t.TempDir()).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.example", "stagenet.example", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "node_names_zone") {
		t.Fatalf("a node with no zone renders one:\n%s", rendered)
	}
}

// The node refuses to start on a zone that is not below its base domain, so the regeneration that
// would write one fails first, and writes nothing.
func TestGenerateNodeConfig_refusesAZoneTheNodeWouldNotStartWith(t *testing.T) {
	for zone, base := range map[string]string{
		"stagenet.orama.network":        "stagenet.orama.network", // the base domain itself
		"nodes.testnet.orama.network":   "stagenet.orama.network", // another network's
		"orama.network":                 "stagenet.orama.network", // the parent
		"nodes.xstagenet.orama.network": "stagenet.orama.network", // a suffix that is not a subdomain
	} {
		dir := t.TempDir()
		writeACMENodeYAML(t, dir, "dns:\n  node_names_zone: \""+zone+"\"\n")
		_, err := NewConfigGenerator(dir).GenerateNodeConfig(nil, "10.0.0.5", "", base, base, false)
		if err == nil || !strings.Contains(err.Error(), "dns.node_names_zone") {
			t.Errorf("zone %q under base %q: err = %v", zone, base, err)
		}
	}
	dir := t.TempDir()
	writeACMENodeYAML(t, dir, "dns:\n  node_names_zone: \"nodes.stagenet.orama.network\"\n")
	if _, err := NewConfigGenerator(dir).GenerateNodeConfig(nil, "10.0.0.5", "", "stagenet.orama.network", "stagenet.orama.network", false); err != nil {
		t.Fatalf("a sub-zone below the base domain was refused: %v", err)
	}
}
