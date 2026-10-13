package installers

import (
	"strings"
	"testing"
)

const (
	testACMEEndpoint = "http://localhost:10104/v1/internal/acme"
	localResolvers   = "            resolvers 127.0.0.1:53\n"
	localDelay       = "            propagation_delay 15s\n"
)

// The live stagenet create run of 2026-10-10: Caddy's first DNS-01 attempt ran
// while the founder's CoreDNS was restarting, the public walk ended at the parent
// zone, and certmagic cached that for the parent's SOA refresh, asking Cloudflare
// for hours. A nameserver node checks its own CoreDNS, in every issuer block.
func TestGenerateCaddyfile_nameserverChecksItsOwnCoreDNS(t *testing.T) {
	ci := newTestCaddyInstaller()
	ci.EnableLocalNameserverChecks()
	cf := ci.generateCaddyfile("stagenet.orama.network", "admin@stagenet.orama.network", testACMEEndpoint, "stagenet.orama.network", "")
	issuers := strings.Count(cf, "issuer acme {")
	if issuers == 0 {
		t.Fatalf("no acme issuer:\n%s", cf)
	}
	if got := strings.Count(cf, localResolvers); got != issuers {
		t.Errorf("%d of %d issuers check 127.0.0.1:53:\n%s", got, issuers, cf)
	}
	if got := strings.Count(cf, localDelay); got != issuers {
		t.Errorf("%d of %d issuers wait for the other nameservers to reload:\n%s", got, issuers, cf)
	}
	if !strings.Contains(cf, "            }\n"+localResolvers+localDelay+"        }\n    }") {
		t.Errorf("the options must sit in the issuer block, after the dns provider:\n%s", cf)
	}
}

func TestGenerateCaddyfile_nodeThatIsNoNameserverKeepsCaddysDefaults(t *testing.T) {
	ci := newTestCaddyInstaller()
	cf := ci.generateCaddyfile("node1.dbrs.space", "admin@dbrs.space", testACMEEndpoint, "dbrs.space", "")
	for _, opt := range []string{"resolvers", "propagation_delay"} {
		if strings.Contains(cf, opt) {
			t.Errorf("a node without CoreDNS must not set %s:\n%s", opt, cf)
		}
	}
	if !strings.Contains(cf, "                key_file "+CaddyACMEKeyPath+"\n            }\n        }\n    }") {
		t.Errorf("the issuer block drifted:\n%s", cf)
	}
}

// The delay covers the CoreDNS reload interval the Corefile sets.
func TestChallengePropagationDelay_coversTheCorefilesRefresh(t *testing.T) {
	ci := &CoreDNSInstaller{}
	corefile := ci.generateCorefile("dbrs.space", testRQLite)
	if !strings.Contains(corefile, "        refresh "+coreDNSRecordRefresh.String()+"\n") {
		t.Fatalf("the Corefile's refresh is not coreDNSRecordRefresh:\n%s", corefile)
	}
	if !strings.Contains(corefile, "refresh 5s\n") {
		t.Errorf("the Corefile's refresh changed; check challengePropagationDelay still covers it:\n%s", corefile)
	}
	if challengePropagationDelay < 2*coreDNSRecordRefresh {
		t.Errorf("propagation delay %s does not cover two reloads of %s", challengePropagationDelay, coreDNSRecordRefresh)
	}
}
