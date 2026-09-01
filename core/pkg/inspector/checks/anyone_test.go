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
	nd.Anyone = &inspector.AnyoneData{}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results when both services inactive, got %d", len(results))
	}
}

func TestCheckAnyone_HealthyClient(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:     true,
		Mode:             "client",
		SocksListening:   true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.control_listening", inspector.StatusPass)
	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusPass)

	if findCheck(results, "anyone.orport_listening") != nil {
		t.Error("client node should not have ORPort check")
	}
	if findCheck(results, "anyone.relay_leftover") != nil {
		t.Error("healthy client should not warn about leftover relay")
	}
}

func TestCheckAnyone_LeftoverRelayWarns(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:    true,
		ClientActive:   false,
		Mode:           "relay",
		SocksListening: true,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.relay_leftover", inspector.StatusWarn)
	expectStatus(t, results, "anyone.client_active", inspector.StatusFail)
}

func TestCheckAnyone_ClientNotBootstrapped(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:    true,
		Mode:            "client",
		SocksListening:  true,
		BootstrapPct:    0,
		Bootstrapped:    false,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusFail)
}

func TestCheckAnyone_ClientPartialBootstrap(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:    true,
		Mode:            "client",
		SocksListening:  true,
		BootstrapPct:    50,
		Bootstrapped:    false,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.client_bootstrapped", inspector.StatusWarn)
}

func TestCheckAnyone_ClientSocksDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "nameserver")
	nd.Anyone = &inspector.AnyoneData{
		ClientActive:   true,
		Mode:           "client",
		SocksListening: false,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.socks_listening", inspector.StatusFail)
}

func TestCheckAnyone_ClientAndLeftoverRelay(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.Anyone = &inspector.AnyoneData{
		RelayActive:      true,
		ClientActive:     true,
		Mode:             "client",
		SocksListening:   true,
		ControlListening: true,
		Bootstrapped:     true,
		BootstrapPct:     100,
	}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckAnyone(data)

	expectStatus(t, results, "anyone.relay_leftover", inspector.StatusWarn)
	expectStatus(t, results, "anyone.socks_listening", inspector.StatusPass)
	if findCheck(results, "anyone.orport_listening") != nil {
		t.Error("should not produce ORPort checks")
	}
}
