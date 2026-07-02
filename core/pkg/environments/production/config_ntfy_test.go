package production

import (
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
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if !strings.Contains(out, `ntfy_base_url: "https://push.dbrs.space"`) {
		t.Errorf("node.yaml missing ntfy_base_url derived from base domain\n---\n%s", out)
	}
}

func TestGenerateNodeConfig_ntfyBaseURL_fallsBackToDomain(t *testing.T) {
	// No base domain → derive from the node domain (matches the orchestrator's
	// dnsZone := baseDomain; if empty -> domain).
	cg := NewConfigGenerator(t.TempDir())
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "anchor.example.net", "", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if !strings.Contains(out, `ntfy_base_url: "https://push.anchor.example.net"`) {
		t.Errorf("node.yaml missing ntfy_base_url fallback to node domain\n---\n%s", out)
	}
}
