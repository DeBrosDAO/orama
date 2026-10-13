package inspector

import (
	"strings"
	"testing"
)

func TestDNSCollectScript_asksAboutTheNamespaceUnits(t *testing.T) {
	for _, want := range []string{
		"systemctl is-active orama-namespace-caddy@index",
		"systemctl is-active --quiet orama-namespace-coredns@nameserver",
		"systemctl show orama-namespace-coredns@nameserver",
	} {
		if !strings.Contains(dnsCollectScript, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(dnsCollectScript, "is-active caddy") {
		t.Error("script asks about the retired caddy.service")
	}
}

func TestDNSCollectScript_readsTheCorefileAsRootAndGuardsEmptyZone(t *testing.T) {
	if !strings.Contains(dnsCollectScript, "sudo -n grep") {
		t.Error("the 0640 Corefile is read without sudo")
	}
	if got := strings.Count(dnsCollectScript, `[ -n "$DOMAIN" ] && dig`); got != 4 {
		t.Errorf("%d dig calls are guarded against an empty zone, want 4", got)
	}
	if !strings.Contains(dnsCollectScript, "command -v dig") {
		t.Error("script does not probe for dig")
	}
}
