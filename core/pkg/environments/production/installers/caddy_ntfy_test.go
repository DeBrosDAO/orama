package installers

import (
	"fmt"
	"strings"
	"testing"
)

// Phase 4 (#72) — when the orchestrator enables ntfy on a node, the
// generated Caddyfile must include a reverse-proxy block routing
// push.<dnsZone> to localhost:<NtfyListenPort>. Without this block,
// public clients can't reach the ntfy server (it listens on
// 127.0.0.1 only).

func TestGenerateCaddyfile_NoNtfyByDefault(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	if strings.Contains(cf, "push.dbrs.space") {
		t.Errorf("Caddyfile should NOT include push.<dnsZone> by default; got:\n%s", cf)
	}
	if strings.Contains(cf, fmt.Sprintf("localhost:%d", NtfyListenPort)) {
		t.Errorf("Caddyfile should NOT route to ntfy port by default; got:\n%s", cf)
	}
}

func TestGenerateCaddyfile_NtfyEnabledEmitsBlock(t *testing.T) {
	ci := newTestCaddyInstaller()
	ci.EnableNtfyProxy("push.dbrs.space")

	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	// Block exists with the right hostname.
	if !strings.Contains(cf, "push.dbrs.space {") {
		t.Errorf("Caddyfile missing push hostname block; got:\n%s", cf)
	}
	// Reverse-proxy target points at the ntfy listen port.
	want := fmt.Sprintf("reverse_proxy localhost:%d", NtfyListenPort)
	if !strings.Contains(cf, want) {
		t.Errorf("Caddyfile missing %q; got:\n%s", want, cf)
	}
	// TLS block still references the orama ACME issuer.
	if !strings.Contains(cf, "dns orama") {
		t.Errorf("ntfy block missing orama TLS issuer; got:\n%s", cf)
	}
}

func TestGenerateCaddyfile_NtfyBlockHasOwnTLS(t *testing.T) {
	ci := newTestCaddyInstaller()
	ci.EnableNtfyProxy("push.dbrs.space")
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	// The ntfy block should be its OWN block — i.e. there are now MORE
	// `tls {` occurrences than there would be without ntfy. This is a
	// guard against accidental collapsing into the wildcard block, which
	// would mix the cert lifecycle with the gateway cert.
	ci2 := newTestCaddyInstaller()
	cf2 := ci2.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	withCount := strings.Count(cf, "issuer acme")
	withoutCount := strings.Count(cf2, "issuer acme")
	if withCount != withoutCount+1 {
		t.Errorf("expected exactly one EXTRA `issuer acme` block with ntfy enabled; with=%d without=%d", withCount, withoutCount)
	}
}

func TestGenerateCaddyfile_NtfyEmptyHostnameSkipped(t *testing.T) {
	// withNtfy=true but no hostname — the block is omitted (defensive;
	// the installer's EnableNtfyProxy requires a hostname so this is a
	// guard against programmer error in the orchestrator).
	ci := newTestCaddyInstaller()
	ci.withNtfy = true
	ci.ntfyHostname = ""

	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")
	if strings.Contains(cf, fmt.Sprintf("localhost:%d", NtfyListenPort)) {
		t.Errorf("empty ntfy hostname should suppress block; got:\n%s", cf)
	}
}
