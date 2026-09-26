package installers

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// testRQLite is the index rqlite a node's CoreDNS talks to: its WireGuard
// address, with credentials (rqlited always runs with -auth).
var testRQLite = rqlite.Endpoint{Host: "10.0.0.1", Port: 10100, Username: "orama", Password: "0123456789abcdef"}

// newTestCoreDNSInstaller creates a CoreDNSInstaller suitable for unit tests.
func newTestCoreDNSInstaller() *CoreDNSInstaller {
	return &CoreDNSInstaller{
		BaseInstaller: NewBaseInstaller("amd64", io.Discard),
		version:       "1.11.1",
		oramaHome:     "/nonexistent",
	}
}

// The recursive block must refuse everyone but this node (no open resolver,
// BSI/CERT-Bund), and must do it with an acl rather than "bind 127.0.0.1": a
// socket bound to 127.0.0.1:53 takes every local query, so the node resolved
// its OWN zone through the forwarder, and Caddy's ACME DNS-01 propagation
// check never saw its challenge record.
func TestGenerateCorefile_RecursionIsLocalOnlyWithoutSplittingTheListener(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", testRQLite)

	if regexp.MustCompile(`(?m)^\s*bind\s`).MatchString(corefile) {
		t.Errorf("no block may bind its own address; one listener must serve both:\n%s", corefile)
	}
	dotBlockIdx := strings.Index(corefile, "\n. {")
	if dotBlockIdx == -1 {
		t.Fatal("Corefile must contain a catch-all '. {' server block")
	}
	dotBlock := corefile[dotBlockIdx:]
	for _, want := range []string{"acl {", "allow net 127.0.0.0/8 ::1/128", "block", "forward . "} {
		if !strings.Contains(dotBlock, want) {
			t.Errorf("catch-all block missing %q:\n%s", want, dotBlock)
		}
	}
	domainStart := strings.Index(corefile, "dbrs.space {")
	domainBlock := corefile[domainStart : domainStart+strings.Index(corefile[domainStart:], "\n}\n")]
	if strings.Contains(domainBlock, "forward") || strings.Contains(domainBlock, "acl") {
		t.Errorf("the authoritative block must answer everyone and forward nothing:\n%s", domainBlock)
	}
}

func TestGenerateCorefile_AuthoritativeBlockNoBindRestriction(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", testRQLite)

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
			corefile := ci.generateCorefile(tt.domain, testRQLite)

			if !strings.Contains(corefile, tt.domain+" {") {
				t.Errorf("Corefile must contain server block for domain %q", tt.domain)
			}

			if !strings.Contains(corefile, "rqlite {") {
				t.Error("Corefile must contain rqlite plugin block")
			}
		})
	}
}

// CoreDNS must read rqlite where it binds (the WireGuard IP, never localhost)
// and without credentials in the DSN line itself.
func TestGenerateCorefile_ContainsRQLiteAddress(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", testRQLite)

	if !strings.Contains(corefile, "dsn http://10.0.0.1:10100\n") {
		t.Errorf("Corefile must point the rqlite plugin at %s:\n%s", testRQLite, corefile)
	}
	if strings.Contains(corefile, "localhost:10100") {
		t.Error("Corefile points the rqlite plugin at localhost, where rqlited does not listen")
	}
}

// rqlited always runs with -auth, so the plugin always gets credentials.
func TestGenerateCorefile_AlwaysCarriesCredentials(t *testing.T) {
	ci := newTestCoreDNSInstaller()
	corefile := ci.generateCorefile("dbrs.space", testRQLite)

	if !strings.Contains(corefile, "username orama") || !strings.Contains(corefile, "password "+testRQLite.Password) {
		t.Errorf("Corefile rqlite block lacks credentials:\n%s", corefile)
	}
}
