package installers

import (
	"io"
	"strings"
	"testing"
)

// newTestCoreDNSInstaller creates a CoreDNSInstaller suitable for unit tests.
// It uses a non-existent oramaHome so generateCorefile won't find a password file
// and will produce output without auth credentials.
func newTestCoreDNSInstaller() *CoreDNSInstaller {
	return &CoreDNSInstaller{
		BaseInstaller: NewBaseInstaller("amd64", io.Discard),
		version:       "1.11.1",
		oramaHome:     "/nonexistent",
	}
}

func TestGenerateCorefile_ContainsBindLocalhost(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", "http://localhost:5001")

	if !strings.Contains(corefile, "bind 127.0.0.1") {
		t.Fatal("Corefile forward block must contain 'bind 127.0.0.1' to prevent open resolver")
	}
}

func TestGenerateCorefile_ForwardBlockIsLocalhostOnly(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", "http://localhost:5001")

	// The bind directive must appear inside the catch-all (.) block,
	// not inside the authoritative domain block.
	// Find the ". {" block and verify bind is inside it.
	dotBlockIdx := strings.Index(corefile, ". {")
	if dotBlockIdx == -1 {
		t.Fatal("Corefile must contain a catch-all '. {' server block")
	}

	dotBlock := corefile[dotBlockIdx:]
	closingIdx := strings.Index(dotBlock, "}")
	if closingIdx == -1 {
		t.Fatal("Catch-all block has no closing brace")
	}
	dotBlock = dotBlock[:closingIdx]

	if !strings.Contains(dotBlock, "bind 127.0.0.1") {
		t.Error("bind 127.0.0.1 must be inside the catch-all (.) block, not the domain block")
	}

	if !strings.Contains(dotBlock, "forward .") {
		t.Error("forward directive must be inside the catch-all (.) block")
	}
}

func TestGenerateCorefile_AuthoritativeBlockNoBindRestriction(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", "http://localhost:5001")

	// The authoritative domain block should NOT have a bind directive
	// (it must listen on all interfaces to serve external DNS queries).
	domainBlockStart := strings.Index(corefile, "dbrs.space {")
	if domainBlockStart == -1 {
		t.Fatal("Corefile must contain 'dbrs.space {' server block")
	}

	// Extract the domain block (up to the first closing brace)
	domainBlock := corefile[domainBlockStart:]
	closingIdx := strings.Index(domainBlock, "}")
	if closingIdx == -1 {
		t.Fatal("Domain block has no closing brace")
	}
	domainBlock = domainBlock[:closingIdx]

	if strings.Contains(domainBlock, "bind ") {
		t.Error("Authoritative domain block must not have a bind directive — it must listen on all interfaces")
	}
}

func TestGenerateCorefile_ContainsDomainZone(t *testing.T) {
	ci := newTestCoreDNSInstaller()

	tests := []struct {
		domain string
	}{
		{"dbrs.space"},
		{"orama.network"},
		{"example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			corefile := ci.generateCorefile(tt.domain, "http://localhost:5001")

			if !strings.Contains(corefile, tt.domain+" {") {
				t.Errorf("Corefile must contain server block for domain %q", tt.domain)
			}

			if !strings.Contains(corefile, "rqlite {") {
				t.Error("Corefile must contain rqlite plugin block")
			}
		})
	}
}

func TestGenerateCorefile_ContainsRQLiteDSN(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	dsn := "http://10.0.0.1:5001"
	corefile := ci.generateCorefile("dbrs.space", dsn)

	if !strings.Contains(corefile, "dsn "+dsn) {
		t.Errorf("Corefile must contain RQLite DSN %q", dsn)
	}
}

func TestGenerateCorefile_NoAuthBlockWithoutCredentials(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", "http://localhost:5001")

	if strings.Contains(corefile, "username") || strings.Contains(corefile, "password") {
		t.Error("Corefile must not contain auth credentials when secrets file is absent")
	}
}

func TestGeneratePluginConfig_ContainsBindPlugin(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	cfg := ci.generatePluginConfig()

	if !strings.Contains(cfg, "bind:bind") {
		t.Error("Plugin config must include the bind plugin (required for localhost-only forwarding)")
	}
}

func TestGeneratePluginConfig_ContainsACLPlugin(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	cfg := ci.generatePluginConfig()

	if !strings.Contains(cfg, "acl:acl") {
		t.Error("Plugin config must include the acl plugin")
	}
}

func TestGeneratePluginConfig_ContainsRQLitePlugin(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	cfg := ci.generatePluginConfig()

	if !strings.Contains(cfg, "rqlite:rqlite") {
		t.Error("Plugin config must include the rqlite plugin")
	}
}
