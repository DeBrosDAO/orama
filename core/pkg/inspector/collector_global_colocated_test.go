package inspector

import (
	"strings"
	"testing"
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
