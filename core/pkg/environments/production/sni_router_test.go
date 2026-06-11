package production

import (
	"strings"
	"testing"
)

// TestGenerateNodeConfig_preservesSNIRouterEnabled is the regression test for
// the feat-124 regen-wipe class of outage (cf. bugboard #259/#846 for webrtc):
// a config regeneration must NOT silently reset an operator's
// sni_router.enabled: true back to false, which would stop the :443 router and
// break stealth TURN. We write a node.yaml with the flag set, regenerate, and
// assert it survives.
func TestGenerateNodeConfig_preservesSNIRouterEnabled(t *testing.T) {
	dir := t.TempDir()
	writeNodeYAML(t, dir, `sni_router:
  enabled: true

http_gateway:
  enabled: true
`)

	cg := NewConfigGenerator(dir)
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}

	if !strings.Contains(out, "sni_router:") {
		t.Fatalf("regenerated node.yaml missing sni_router block\n---\n%s", out)
	}
	if !strings.Contains(out, "enabled: true") {
		t.Errorf("regenerated node.yaml did not preserve sni_router.enabled: true\n---\n%s", out)
	}
}

// TestGenerateNodeConfig_sniRouterDefaultsFalse verifies a fresh install (no
// existing node.yaml) renders sni_router.enabled: false — default OFF.
func TestGenerateNodeConfig_sniRouterDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	cg := NewConfigGenerator(dir)

	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if !strings.Contains(out, "sni_router:") {
		t.Fatalf("node.yaml missing sni_router block\n---\n%s", out)
	}
	if !strings.Contains(out, "enabled: false") {
		t.Errorf("fresh node.yaml should render sni_router.enabled: false\n---\n%s", out)
	}
	if cg.SNIRouterEnabled() {
		t.Errorf("SNIRouterEnabled() should be false on a fresh install")
	}
}

// TestGenerateNodeConfig_sniRouterDisabledStaysFalse verifies an existing
// node.yaml that explicitly disabled the router does not flip on during regen.
func TestGenerateNodeConfig_sniRouterDisabledStaysFalse(t *testing.T) {
	dir := t.TempDir()
	writeNodeYAML(t, dir, "sni_router:\n  enabled: false\nhttp_gateway:\n  enabled: true\n")

	cg := NewConfigGenerator(dir)
	out, err := cg.GenerateNodeConfig(nil, "10.0.0.5", "", "node-1.dbrs.space", "dbrs.space", false)
	if err != nil {
		t.Fatalf("GenerateNodeConfig failed: %v", err)
	}
	if !strings.Contains(out, "enabled: false") {
		t.Errorf("disabled sni_router should stay false on regen\n---\n%s", out)
	}
}
