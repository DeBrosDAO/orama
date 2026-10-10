package install

import (
	"strings"
	"testing"
)

func TestValidateNodeNamesZone(t *testing.T) {
	const base = "stagenet.orama.network"
	for _, ok := range []string{"", "nodes." + base, "a.b." + base} {
		if err := (&Flags{BaseDomain: base, NodeNamesZone: ok}).validateNodeNamesZone(); err != nil {
			t.Errorf("zone %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{base, "orama.network", "nodes.testnet.orama.network", "nodes.x" + base, "Nodes." + base, "nodes..x"} {
		err := (&Flags{BaseDomain: base, NodeNamesZone: bad}).validateNodeNamesZone()
		if err == nil || !strings.Contains(err.Error(), "--node-names-zone") {
			t.Errorf("zone %q: err = %v, want one naming --node-names-zone", bad, err)
		}
	}
}

func TestRemoteInstallArgs_noZoneNoFlag(t *testing.T) {
	if line := strings.Join(remoteInstallArgs(&Flags{VpsIP: "1.2.3.4"}), " "); strings.Contains(line, "node-names-zone") {
		t.Fatalf("a zone nobody set is forwarded: %q", line)
	}
}
