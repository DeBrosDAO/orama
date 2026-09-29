package inspector

import (
	"strings"
	"testing"
	"time"
)

func TestGlobalCollectScript_readsTheChainOnTheNamespaceAddressWhenCoLocated(t *testing.T) {
	script := globalCollectScript()
	for _, want := range []string{
		"chain_host=127.0.0.1",
		"[ -e /etc/systemd/system/orama-global-netns.service ] && chain_host=198.18.0.2",
		"http://$chain_host:31001/status",
		"http://$chain_host:31003/cosmos/slashing/v1beta1/params",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "http://127.0.0.1:31001") || strings.Contains(script, "http://127.0.0.1:31003") {
		t.Errorf("script still reads the chain on loopback unconditionally")
	}
}

func TestGlobalCollectScript_asksThroughSudoOnTheNamespaceAddressAndSaysWhenItFails(t *testing.T) {
	script := globalCollectScript()
	if !strings.Contains(script, `if [ "$chain_host" = 198.18.0.2 ]; then sudo -n curl -sf --max-time 3 "$1"; else curl -sf --max-time 3 "$1"; fi || echo `+chainCurlFailed) {
		t.Errorf("chain_curl does not use sudo -n on the namespace address and report failure:\n%s", script)
	}
	if strings.Contains(script, "curl -sf --max-time 3 http://$chain_host") || strings.Contains(script, `curl -sf --max-time 3 "http://$chain_host`) {
		t.Errorf("a chain request bypasses chain_curl")
	}
}

// An active chain unit whose RPC could not be asked is an error, not an empty report that reads as
// a chain that is merely down.
func TestChainFromSections_anActiveChainThatCannotBeAskedIsAnError(t *testing.T) {
	r := chainFromSections(map[string]string{
		"chain_load": "loaded", "chain_state": "active", "status": chainCurlFailed,
	}, time.Now())
	if r == nil || r.Error == "" || r.Responsive {
		t.Fatalf("got %+v, want an error report", r)
	}
	quiet := chainFromSections(map[string]string{
		"chain_load": "loaded", "chain_state": "inactive", "status": chainCurlFailed,
	}, time.Now())
	if quiet == nil || strings.Contains(quiet.Error, "sudo") {
		t.Errorf("a stopped chain must not be reported as a sudo failure: %+v", quiet)
	}
}
