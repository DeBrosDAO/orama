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
		Mode:             "relay",
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
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true, // service is debros-anyone-relay for both modes
		Mode:             "client",
		SocksListening:   true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
		ORPortReachable:  make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.relay_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.control_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusPass)

	// Should NOT have relay-specific checks
	if findCheck(results, "anyone.orport_listening") != nil {
		t.Error("client-mode node should not have ORPort check")
	}
	if findCheck(results, "anyone.bootstrapped") != nil {
		t.Error("client-mode node should not have relay bootstrap check")
	}
	if findCheck(results, "anyone.fingerprint") != nil {
		t.Error("client-mode node should not have fingerprint check")
	}
	if findCheck(results, "anyone.nickname") != nil {
		t.Error("client-mode node should not have nickname check")
	}
}

func TestCheckAnyone_ClientNotBootstrapped(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "client",
		SocksListening:  true,
		BootstrapPct:    0,
		Bootstrapped:    false,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusFail)
}

func TestCheckAnyone_ClientPartialBootstrap(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "client",
		SocksListening:  true,
		BootstrapPct:    50,
		Bootstrapped:    false,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusWarn)
}

func TestCheckAnyone_RelayORPortDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		Mode:             "relay",
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
		Mode:            "relay",
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
		Mode:            "relay",
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
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "client",
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
		Mode:            "relay",
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
		Mode:            "relay",
		ORPortListening: true,
		ORPortReachable: map[string]bool{"2.2.2.2": true},
	}

	nd2 := makeNodeData("2.2.2.2", "node")
	nd2.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "relay",
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
		Mode:            "relay",
		ORPortListening: true,
		ORPortReachable: map[string]bool{"2.2.2.2": false},
	}

	nd2 := makeNodeData("2.2.2.2", "node")
	nd2.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "relay",
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
		Mode:             "relay", // relay mode with legacy client also running
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

	// Should have both relay and legacy client checks
	expectStatus(t, results, "anyone.relay_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.client_active", inspector.StatusPass)
	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.orport_listening", inspector.StatusPass)
}

func TestCheckAnyone_ClientModeNoRelayChecks(t *testing.T) {
	// A client-mode node should never produce relay-specific check IDs
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:     true,
		Mode:            "client",
		SocksListening:  true,
		Bootstrapped:    true,
		BootstrapPct:    100,
		ORPortReachable: make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	relayOnlyChecks := []string{
		"anyone.orport_listening",
		"anyone.bootstrapped",
		"anyone.fingerprint",
		"anyone.nickname",
		"anyone.client_active",
	}
	for _, id := range relayOnlyChecks {
		if findCheck(results, id) != nil {
			t.Errorf("client-mode node should not produce check %q", id)
		}
	}
}

func TestCheckAnyone_MixedCluster(t *testing.T) {
	// Simulate a cluster with both relay and client-mode nodes
	relay := makeNodeData("1.1.1.1", "node")
	relay.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		Mode:             "relay",
		ORPortListening:  true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
		Fingerprint:      "ABCDEF",
		Nickname:         "relay1",
		ORPortReachable:  make(map[string]bool),
	}

	client := makeNodeData("2.2.2.2", "nameserver")
	client.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		Mode:             "client",
		SocksListening:   true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
		ORPortReachable:  make(map[string]bool),
	}

	data := makeCluster(map[string]*inspector.NodeData{
		"1.1.1.1": relay,
		"2.2.2.2": client,
	})
	results := CheckAnyone(data)

	// Relay node should have relay checks
	relayResults := filterByNode(results, "ubuntu@1.1.1.1")
	if findCheckIn(relayResults, "anyone.orport_listening") == nil {
		t.Error("relay node should have ORPort check")
	}
	if findCheckIn(relayResults, "anyone.bootstrapped") == nil {
		t.Error("relay node should have relay bootstrap check")
	}

	// Client node should have client checks
	clientResults := filterByNode(results, "ubuntu@2.2.2.2")
	if findCheckIn(clientResults, "anyone.client_bootstrapped") == nil {
		t.Error("client node should have client bootstrap check")
	}
	if findCheckIn(clientResults, "anyone.orport_listening") != nil {
		t.Error("client node should NOT have ORPort check")
	}
}

// filterByNode returns checks for a specific node.
func filterByNode(results []inspector.CheckResult, node string) []inspector.CheckResult {
	var out []inspector.CheckResult
	for _, r := range results {
		if r.Node == node {
			out = append(out, r)
		}
	}
	return out
}

// findCheckIn returns a pointer to the first check matching the given ID in a slice.
func findCheckIn(results []inspector.CheckResult, id string) *inspector.CheckResult {
	for i := range results {
		if results[i].ID == id {
			return &results[i]
		}
	}
	return nil
}
