package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bugboard #858 — the ntfy fan-out only activates when the gateway config
// carries NtfyBaseURL. The fan-out consumer (dependencies.go) shipped, but the
// template + parse field + node→gateway mapping were missed, so cfg.NtfyBaseURL
// stayed empty and every publish went single-host (~87% loss). These pin that
// the generated node.yaml now renders ntfy_base_url under http_gateway, derived
// as push.<dnsZone> to match the ntfy server + Caddy reverse-proxy host.

func TestGenerateNodeConfig_rendersNtfyBaseURL_fromBaseDomain(t *testing.T) {
	cg := NewConfigGenerator(t.TempDir())
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.example.com", "example.com", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if !strings.Contains(out, `ntfy_base_url: "https://push.example.com"`) {
		t.Errorf("node.yaml missing ntfy_base_url derived from base domain\n---\n%s", out)
	}
}

// There is no fallback zone: a node config without a base domain used to be
// written for the node's own domain (and before that for a domain the project
// no longer owns), configuring CoreDNS, Caddy and ntfy for the wrong zone.
func TestGenerateNodeConfig_requiresTheBaseDomain(t *testing.T) {
	cg := NewConfigGenerator(t.TempDir())
	for _, base := range []string{"", "   "} {
		if out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "anchor.example.net", base, false); err == nil {
			t.Errorf("base domain %q accepted; rendered:\n%s", base, out)
		}
	}
}

// Phase 4 refuses before it writes anything.
func TestPhase4GenerateConfigs_requiresTheBaseDomain(t *testing.T) {
	home := t.TempDir()
	ps := NewProductionSetup(home, io.Discard, false, true)
	err := ps.Phase4GenerateConfigs(nil, "10.0.0.5", false, "node-1.example.com", "", "")
	if err == nil || !strings.Contains(err.Error(), "base domain") {
		t.Fatalf("Phase4GenerateConfigs = %v, want an error naming the base domain", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".orama")); len(entries) != 0 {
		t.Errorf("wrote %d entries before refusing", len(entries))
	}
}
