package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func torCluster(td *inspector.TorData) []inspector.CheckResult {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Tor = td
	return CheckTor(makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd}))
}

func TestCheckTor_nilDataIsSkipped(t *testing.T) {
	if results := torCluster(nil); len(results) != 0 {
		t.Errorf("expected no results without Tor data, got %d", len(results))
	}
}

func TestCheckTor_healthyClient(t *testing.T) {
	results := torCluster(&inspector.TorData{
		ClientActive: true, SocksListening: true, Bootstrapped: true, BootstrapPct: 100,
	})
	expectStatus(t, results, "tor.client_active", inspector.StatusPass)
	expectStatus(t, results, "tor.socks_listening", inspector.StatusPass)
	expectStatus(t, results, "tor.client_bootstrapped", inspector.StatusPass)
	if findCheck(results, "tor.legacy_anyone") != nil {
		t.Error("a clean node must not warn about Anyone")
	}
}

// Every node runs Tor, so an inactive unit is a failure, not "not installed".
func TestCheckTor_inactiveClientFails(t *testing.T) {
	results := torCluster(&inspector.TorData{BootstrapPct: inspector.TorBootstrapUnknown})
	expectStatus(t, results, "tor.client_active", inspector.StatusFail)
	if findCheck(results, "tor.socks_listening") != nil {
		t.Error("port checks are meaningless once the unit is down")
	}
}

func TestCheckTor_socksNotBoundFails(t *testing.T) {
	results := torCluster(&inspector.TorData{ClientActive: true, Bootstrapped: true, BootstrapPct: 100})
	expectStatus(t, results, "tor.socks_listening", inspector.StatusFail)
}

func TestCheckTor_bootstrapStates(t *testing.T) {
	cases := []struct {
		pct  int
		want inspector.Status
	}{
		{0, inspector.StatusFail},
		{45, inspector.StatusWarn},
		{inspector.TorBootstrapUnknown, inspector.StatusWarn},
	}
	for _, c := range cases {
		results := torCluster(&inspector.TorData{ClientActive: true, SocksListening: true, BootstrapPct: c.pct})
		expectStatus(t, results, "tor.client_bootstrapped", c.want)
	}
}

func TestCheckTor_legacyAnyoneWarns(t *testing.T) {
	results := torCluster(&inspector.TorData{
		ClientActive: true, SocksListening: true, Bootstrapped: true, BootstrapPct: 100, LegacyAnyone: true,
	})
	expectStatus(t, results, "tor.legacy_anyone", inspector.StatusWarn)
}
