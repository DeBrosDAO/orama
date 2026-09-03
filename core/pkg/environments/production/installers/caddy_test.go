package installers

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// newTestCaddyInstaller returns a CaddyInstaller suitable for unit tests —
// no real filesystem or network dependencies.
func newTestCaddyInstaller() *CaddyInstaller {
	return &CaddyInstaller{
		BaseInstaller: NewBaseInstaller("amd64", io.Discard),
		oramaHome:     "/nonexistent",
	}
}

// TestGenerateCaddyfile_DisablesHTTP2 is the regression guard for bug
// #249: HTTP/2 forbids the `Connection: Upgrade` and `Upgrade: websocket`
// headers per RFC 7540 §8.1.2.2, so a WebSocket-upgrade request sent
// over an h2 connection arrives at Caddy with the upgrade headers
// stripped. Caddy then forwards a plain HTTP/1.1 GET to the gateway,
// the gateway's `isWebSocketUpgrade(r)` returns false, the
// query-string `?api_key=` / `?jwt=` WS-auth fallback is ignored, and
// the client gets 401.
//
// Disabling h2 at the listener means ALPN negotiates h1 every time, so
// WS upgrades work cleanly. h3 is also disabled (so Caddy doesn't bind
// UDP 443, which TURN needs).
//
// If anyone adds `h2` back to the `protocols` line without a deliberate
// migration of every mobile-WS client to RFC 8441 ("Bootstrapping
// WebSockets with HTTP/2"), this test fails loud.
func TestGenerateCaddyfile_DisablesHTTP2(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	if !strings.Contains(cf, "protocols h1\n") {
		t.Errorf("Caddyfile must declare `protocols h1` (bug #249); got:\n%s", cf)
	}
	if strings.Contains(cf, "protocols h1 h2") {
		t.Errorf("Caddyfile must NOT advertise h2 (bug #249 regression); got:\n%s", cf)
	}
	if strings.Contains(cf, "h3") {
		t.Errorf("Caddyfile must NOT advertise h3 (TURN UDP 443 conflict); got:\n%s", cf)
	}
}

func TestGenerateCaddyfile_ContainsCanonicalReverseProxy(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "")

	// Sanity checks on the basics; cheap insurance against fat-finger edits.
	for _, want := range []string{
		"*.node1.dbrs.space {",
		"node1.dbrs.space {",
		"reverse_proxy localhost:10104",
		"http://*.node1.dbrs.space",
		":80 {",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Caddyfile missing %q; got:\n%s", want, cf)
		}
	}
}

func TestGenerateCaddyfile_BaseDomainAddsSeparateBlocks(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	// Both node-domain and base-domain blocks should be present.
	for _, want := range []string{
		"*.node1.dbrs.space",
		"*.dbrs.space",
		"dbrs.space {",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Caddyfile missing %q (base-domain block); got:\n%s", want, cf)
		}
	}
}

func TestGenerateCaddyfile_BaseDomainSameAsDomainOmitsDuplicates(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	// When base == node domain, the duplicate base blocks must be skipped:
	// one TLS `*.dbrs.space { ... }` block + one HTTP `http://*.dbrs.space {
	// ... }` block. The substring `*.dbrs.space {` matches both so we
	// expect a count of exactly 2, not 4 (which would mean the dedupe
	// guard at `if baseDomain != "" && baseDomain != domain` regressed).
	if got := strings.Count(cf, "*.dbrs.space {"); got != 2 {
		t.Errorf("expected exactly 2 `*.dbrs.space {` occurrences (1 TLS + 1 HTTP), got %d in:\n%s", got, cf)
	}
}

// TestGenerateCaddyfile_SNIRouterDisabledByteIdentical is the safety guard for
// feat-124: when EnableSNIRouterMode has NOT been called, the generated
// Caddyfile must be byte-identical to the pre-feature output (HTTPS stays on
// :443, no `https_port` global option). This is the default for every existing
// node — any drift here is a silent production change.
func TestGenerateCaddyfile_SNIRouterDisabledByteIdentical(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	if strings.Contains(cf, "https_port") {
		t.Errorf("default Caddyfile must NOT contain `https_port` (SNI router off); got:\n%s", cf)
	}
	if strings.Contains(cf, "8443") {
		t.Errorf("default Caddyfile must NOT reference :8443 (SNI router off); got:\n%s", cf)
	}
	// The global options block must be exactly the pre-feature shape.
	if !strings.Contains(cf, "{\n    email admin@dbrs.space\n    servers {\n        protocols h1\n    }\n}\n") {
		t.Errorf("default global options block drifted from pre-feature output; got:\n%s", cf)
	}
}

// TestGenerateCaddyfile_SNIRouterEnabledMovesHTTPSTo8443 verifies that after
// EnableSNIRouterMode, Caddy's HTTPS listener is moved to :8443 via the
// `https_port` global option, while plain HTTP (:80) is unchanged so ACME
// HTTP-01 and the HTTP catch-all still work.
func TestGenerateCaddyfile_SNIRouterEnabledMovesHTTPSTo8443(t *testing.T) {
	ci := newTestCaddyInstaller()
	ci.EnableSNIRouterMode()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space",
		"http://localhost:10104/v1/internal/acme", "dbrs.space")

	want := fmt.Sprintf("https_port %d", CaddyHTTPSPortBehindSNI)
	if !strings.Contains(cf, want) {
		t.Errorf("SNI-router Caddyfile must contain %q; got:\n%s", want, cf)
	}
	// The global option belongs inside the top-level options block, before the
	// servers stanza.
	if !strings.Contains(cf, "{\n    email admin@dbrs.space\n    https_port 8443\n    servers {\n        protocols h1\n    }\n}\n") {
		t.Errorf("https_port not placed correctly in global options block; got:\n%s", cf)
	}
	// Plain HTTP :80 catch-all must be unchanged.
	if !strings.Contains(cf, ":80 {") {
		t.Errorf("HTTP :80 block must remain when SNI router enabled; got:\n%s", cf)
	}
}
