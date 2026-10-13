package checks

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// An unreachable node has no data in any subsystem. It must produce one
// node.reachable failure and nothing fabricated from the missing data.
func TestRunChecks_unreachable_node_yields_single_failure(t *testing.T) {
	nd := makeNodeData("10.0.0.1", "nameserver-ns1")
	nd.Unreachable = "ssh failed: Connection reset"
	res := inspector.RunChecks(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd}), nil)

	var bad []inspector.CheckResult
	for _, c := range res.Checks {
		if c.Status != inspector.StatusSkip {
			bad = append(bad, c)
		}
	}
	if len(bad) != 1 {
		t.Fatalf("want exactly 1 non-skipped result, got %d: %+v", len(bad), bad)
	}
	c := bad[0]
	if c.ID != inspector.CheckNodeReachable || c.Status != inspector.StatusFail || c.Severity != inspector.Critical {
		t.Fatalf("unexpected result: %+v", c)
	}
	if !strings.Contains(c.Message, "Connection reset") {
		t.Errorf("message should carry the cause: %q", c.Message)
	}
}

// One failed section reports itself and leaves the other sections' checks alone.
func TestRunChecks_partial_failure_only_affects_its_subsystem(t *testing.T) {
	nd := makeNodeData("10.0.0.1", "nameserver-ns1")
	nd.Failed = map[string]string{inspector.SubsystemDNS: "ssh command returned no output"}
	nd.Tor = &inspector.TorData{ClientActive: true, SocksListening: true, BootstrapPct: 100, Bootstrapped: true}
	res := inspector.RunChecks(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd}), nil)

	if c := findCheck(res.Checks, "dns.collected"); c == nil || c.Status != inspector.StatusFail {
		t.Fatalf("dns.collected failure missing: %+v", res.Checks)
	}
	if findCheck(res.Checks, "dns.coredns_active") != nil {
		t.Error("dns checks must be skipped when dns was not collected")
	}
	if c := findCheck(res.Checks, "tor.client_active"); c == nil || c.Status != inspector.StatusPass {
		t.Errorf("tor checks must still run on the collected data: %+v", res.Checks)
	}
	if findCheck(res.Checks, inspector.CheckNodeReachable) != nil {
		t.Error("a reachable node must not report node.reachable")
	}
}

// A node whose data was collected is judged exactly as before.
func TestRunChecks_collected_node_unchanged(t *testing.T) {
	nd := makeNodeData("10.0.0.1", "nameserver-ns1")
	nd.DNS = &inspector.DNSData{CoreDNSActive: false}
	res := inspector.RunChecks(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd}), []string{"dns"})

	if c := findCheck(res.Checks, "dns.coredns_active"); c == nil || c.Status != inspector.StatusFail {
		t.Fatalf("a genuinely inactive CoreDNS must still fail: %+v", res.Checks)
	}
	if findCheck(res.Checks, "dns.collected") != nil {
		t.Error("no collection failure to report")
	}
}
