package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestCheckGlobal_healthyChainPasses(t *testing.T) {
	jailed := false
	nd := makeNodeData("10.0.0.1", "node")
	nd.Chain = &report.ChainReport{
		ServiceActive: true, UnitState: "active", Responsive: true,
		LatestHeight: 10, Peers: 2, ValidatorCount: 3, BlockAgeSec: 1,
		Jailed: &jailed,
	}
	results := CheckGlobal(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd}))
	expectStatus(t, results, "chain.healthy", inspector.StatusPass)
}

func TestCheckGlobal_jailedFails(t *testing.T) {
	jailed := true
	nd := makeNodeData("10.0.0.1", "node")
	nd.Chain = &report.ChainReport{
		ServiceActive: true, UnitState: "active", Responsive: true,
		LatestHeight: 10, Peers: 2, ValidatorCount: 3, BlockAgeSec: 1,
		Jailed: &jailed,
	}
	results := CheckGlobal(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd}))
	expectStatus(t, results, "chain.jailed", inspector.StatusFail)
	if findCheck(results, "chain.healthy") != nil {
		t.Fatal("a jailed validator passed")
	}
}

func TestCheckGlobal_skipsAClusterNode(t *testing.T) {
	nd := makeNodeData("10.0.0.1", "node")
	if results := CheckGlobal(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": nd})); len(results) != 0 {
		t.Fatalf("a node with no global section produced %d checks", len(results))
	}
}

func TestCheckGlobal_everyCheckIsInTheGlobalSubsystem(t *testing.T) {
	jailed := true
	healthy := makeNodeData("10.0.0.1", "node")
	healthy.Chain = &report.ChainReport{
		ServiceActive: true, UnitState: "active", Responsive: true,
		LatestHeight: 10, Peers: 2, ValidatorCount: 3, BlockAgeSec: 1,
	}
	failing := makeNodeData("10.0.0.2", "node")
	failing.Chain = &report.ChainReport{
		ServiceActive: true, UnitState: "active", Responsive: true,
		LatestHeight: 10, Peers: 2, ValidatorCount: 3, BlockAgeSec: 1,
		Jailed: &jailed,
	}
	results := CheckGlobal(makeCluster(map[string]*inspector.NodeData{"10.0.0.1": healthy, "10.0.0.2": failing}))
	if len(results) == 0 {
		t.Fatal("no checks")
	}
	for _, c := range results {
		if c.Subsystem != "global" {
			t.Errorf("check %s is in subsystem %q, want global", c.ID, c.Subsystem)
		}
	}
}
