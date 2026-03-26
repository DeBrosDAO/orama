package checks

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestCheckWireGuard_InterfaceDown(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{InterfaceUp: false}

	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)

	expectStatus(t, results, "wg.interface_up", inspector.StatusFail)
	// Early return — no further per-node checks
	if findCheck(results, "wg.service_active") != nil {
		t.Error("should not check service_active when interface down")
	}
}

func TestCheckWireGuard_HealthyNode(t *testing.T) {
	now := time.Now().Unix()
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp:   true,
		ServiceActive: true,
		WgIP:          "10.0.0.1",
		ListenPort:    51820,
		PeerCount:     2,
		MTU:           1420,
		ConfigExists:  true,
		ConfigPerms:   "600",
		Peers: []inspector.WGPeer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: "10.0.0.2/32", LatestHandshake: now - 30, TransferRx: 1000, TransferTx: 2000},
			{PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", AllowedIPs: "10.0.0.3/32", LatestHandshake: now - 60, TransferRx: 500, TransferTx: 800},
		},
	}

	// Single-node for per-node assertions (avoids helper node interference)
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)

	expectStatus(t, results, "wg.interface_up", inspector.StatusPass)
	expectStatus(t, results, "wg.service_active", inspector.StatusPass)
	expectStatus(t, results, "wg.correct_ip", inspector.StatusPass)
	expectStatus(t, results, "wg.listen_port", inspector.StatusPass)
	expectStatus(t, results, "wg.mtu", inspector.StatusPass)
	expectStatus(t, results, "wg.config_exists", inspector.StatusPass)
	expectStatus(t, results, "wg.config_perms", inspector.StatusPass)
	expectStatus(t, results, "wg.handshake_all", inspector.StatusPass)
	expectStatus(t, results, "wg.handshake_fresh", inspector.StatusPass)
	expectStatus(t, results, "wg.peer_traffic", inspector.StatusPass)
}

func TestCheckWireGuard_WrongIP(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "192.168.1.5",
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.correct_ip", inspector.StatusWarn)
}

func TestCheckWireGuard_WrongPort(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "10.0.0.1",
		ListenPort:  12345,
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.listen_port", inspector.StatusWarn)
}

func TestCheckWireGuard_PeerCountMismatch(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{InterfaceUp: true, WgIP: "10.0.0.1", PeerCount: 1}

	nodes := map[string]*inspector.NodeData{"1.1.1.1": nd}
	for _, host := range []string{"2.2.2.2", "3.3.3.3", "4.4.4.4"} {
		other := makeNodeData(host, "node")
		other.WireGuard = &inspector.WireGuardData{InterfaceUp: true, PeerCount: 3}
		nodes[host] = other
	}
	data := makeCluster(nodes)
	results := CheckWireGuard(data)

	// Node 1.1.1.1 has 1 peer but expects 3 (4 nodes - 1)
	c := findCheck(results, "wg.peer_count")
	if c == nil {
		t.Fatal("expected wg.peer_count check")
	}
	// At least one node should have a warn
	hasWarn := false
	for _, r := range results {
		if r.ID == "wg.peer_count" && r.Status == inspector.StatusWarn {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error("expected at least one wg.peer_count warn for mismatched peer count")
	}
}

func TestCheckWireGuard_ZeroPeers(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{InterfaceUp: true, WgIP: "10.0.0.1", PeerCount: 0}

	nodes := map[string]*inspector.NodeData{"1.1.1.1": nd}
	for _, host := range []string{"2.2.2.2", "3.3.3.3"} {
		other := makeNodeData(host, "node")
		other.WireGuard = &inspector.WireGuardData{InterfaceUp: true, PeerCount: 2}
		nodes[host] = other
	}
	data := makeCluster(nodes)
	results := CheckWireGuard(data)

	// At least one node should fail (zero peers = isolated)
	hasFail := false
	for _, r := range results {
		if r.ID == "wg.peer_count" && r.Status == inspector.StatusFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Error("expected wg.peer_count fail for isolated node")
	}
}

func TestCheckWireGuard_StaleHandshakes(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "10.0.0.1",
		PeerCount:   2,
		Peers: []inspector.WGPeer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: "10.0.0.2/32", LatestHandshake: time.Now().Unix() - 600, TransferRx: 100, TransferTx: 200},
			{PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", AllowedIPs: "10.0.0.3/32", LatestHandshake: time.Now().Unix() - 600, TransferRx: 100, TransferTx: 200},
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.handshake_fresh", inspector.StatusWarn)
}

func TestCheckWireGuard_NeverHandshaked(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "10.0.0.1",
		PeerCount:   1,
		Peers: []inspector.WGPeer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: "10.0.0.2/32", LatestHandshake: 0},
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.handshake_all", inspector.StatusFail)
}

func TestCheckWireGuard_NoTraffic(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "10.0.0.1",
		PeerCount:   1,
		Peers: []inspector.WGPeer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: "10.0.0.2/32", LatestHandshake: time.Now().Unix(), TransferRx: 0, TransferTx: 0},
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.peer_traffic", inspector.StatusWarn)
}

func TestCheckWireGuard_CatchAllRoute(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	nd.WireGuard = &inspector.WireGuardData{
		InterfaceUp: true,
		WgIP:        "10.0.0.1",
		PeerCount:   1,
		Peers: []inspector.WGPeer{
			{PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", AllowedIPs: "0.0.0.0/0", LatestHandshake: time.Now().Unix(), TransferRx: 100, TransferTx: 200},
		},
	}
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.peer_catch_all", inspector.StatusFail)
}

func TestCheckWireGuard_CrossNode_PeerCountConsistent(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	for _, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.WireGuard = &inspector.WireGuardData{InterfaceUp: true, PeerCount: 2, MTU: 1420}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.peer_count_consistent", inspector.StatusPass)
	expectStatus(t, results, "wg.mtu_consistent", inspector.StatusPass)
}

func TestCheckWireGuard_CrossNode_PeerCountInconsistent(t *testing.T) {
	nodes := map[string]*inspector.NodeData{}
	counts := []int{2, 2, 1}
	for i, host := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		nd := makeNodeData(host, "node")
		nd.WireGuard = &inspector.WireGuardData{InterfaceUp: true, PeerCount: counts[i], MTU: 1420}
		nodes[host] = nd
	}
	data := makeCluster(nodes)
	results := CheckWireGuard(data)
	expectStatus(t, results, "wg.peer_count_consistent", inspector.StatusWarn)
}

func TestCheckWireGuard_NilData(t *testing.T) {
	nd := makeNodeData("1.1.1.1", "node")
	data := makeCluster(map[string]*inspector.NodeData{"1.1.1.1": nd})
	results := CheckWireGuard(data)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil WireGuard data, got %d", len(results))
	}
}
