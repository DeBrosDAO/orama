package checks

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckAnyone_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil Anyone data, got %d", len(results))
	}
}

func TestCheckAnyone_BothInactive(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		ORPortReachable: make(map[string]bool),
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results when both services inactive, got %d", len(results))
	}
}

func TestCheckAnyone_HealthyRelay(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		ORPortListening:  true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
		Fingerprint:      "ABCDEF1234567890",
		Nickname:         "OramaRelay1",
		ORPortReachable:  make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.relay_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.orport_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.control_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.bootstrapped", inspector.StatusPass)
	expectStatus(t, results, "anyone.fingerprint", inspector.StatusPass)
	expectStatus(t, results, "anyone.nickname", inspector.StatusPass)
}

func TestCheckAnyone_HealthyClient(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:    true,
		SocksListening:  true,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.client_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
}

func TestCheckAnyone_RelayORPortDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		ORPortListening:  false,
		ControlListening: true,
		ORPortReachable:  make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.orport_listening", inspector.StatusFail)
}

func TestCheckAnyone_RelayNotBootstrapped(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		BootstrapPct:    0,
		Bootstrapped:    false,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.bootstrapped", inspector.StatusFail)
}

func TestCheckAnyone_RelayPartialBootstrap(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		BootstrapPct:    75,
		Bootstrapped:    false,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.bootstrapped", inspector.StatusWarn)
}

func TestCheckAnyone_ClientSocksDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:    true,
		SocksListening:  false,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.socks_listening", inspector.StatusFail)
}

func TestCheckAnyone_NoFingerprint(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		Fingerprint:     "",
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.fingerprint", inspector.StatusWarn)
}

func TestCheckAnyone_CrossNode_ORPortReachable(t *testing.T) {
	nd1 := makeNodeData("1.1.1.1", "node")
	nd1.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		ORPortReachable: map[string]bool{"2.2.2.2": true},
	}

	nd2 := makeNodeData("2.2.2.2", "node")
	nd2.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		ORPortReachable: map[string]bool{"1.1.1.1": true},
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd1, "2.2.2.2": nd2})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.orport_reachable", inspector.StatusPass)
}

func TestCheckAnyone_CrossNode_ORPortUnreachable(t *testing.T) {
	nd1 := makeNodeData("1.1.1.1", "node")
	nd1.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		ORPortReachable: map[string]bool{"2.2.2.2": false},
	}

	nd2 := makeNodeData("2.2.2.2", "node")
	nd2.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		ORPortListening: true,
		ORPortReachable: map[string]bool{"1.1.1.1": true},
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd1, "2.2.2.2": nd2})
	results := CheckAnyone(data)

	// Should have at least one fail for the unreachable connection
	hasFail := false
	for _, r := range results {
		if r.ID == "anyone.orport_reachable" && r.Status == inspector.StatusFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Error("expected at least one anyone.orport_reachable fail")
	}
}

func TestCheckAnyone_BothRelayAndClient(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		ClientActive:     true,
		ORPortListening:  true,
		SocksListening:   true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
		Fingerprint:      "ABCDEF",
		Nickname:         "test",
		ORPortReachable:  make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	// Should have both relay and client checks
	expectStatus(t, results, "anyone.relay_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.client_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.orport_listening", inspector.StatusPass)
}
