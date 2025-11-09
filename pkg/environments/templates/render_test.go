package templates

import (
	"strings"
	"testing"
)

func TestRenderBootstrapConfig(t *testing.T) {
	data := BootstrapConfigData{
		NodeID:         "bootstrap",
		P2PPort:        4001,
		DataDir:        "/home/debros/.debros/bootstrap",
		RQLiteHTTPPort: 5001,
		RQLiteRaftPort: 7001,
		ClusterAPIPort: 9094,
		IPFSAPIPort:    5001,
	}

	result, err := RenderBootstrapConfig(data)
	if err != nil {
		t.Fatalf("RenderBootstrapConfig failed: %v", err)
	}

	// Check for required fields
	checks := []string{
		"id: \"bootstrap\"",
		"type: \"bootstrap\"",
		"tcp/4001",
		"rqlite_port: 5001",
		"rqlite_raft_port: 7001",
		"cluster_api_url: \"http://localhost:9094\"",
		"api_url: \"http://localhost:5001\"",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("Bootstrap config missing: %s", check)
		}
	}
}

func TestRenderNodeConfig(t *testing.T) {
	bootstrapMultiaddr := "/ip4/127.0.0.1/tcp/4001/p2p/Qm1234567890"
	data := NodeConfigData{
		NodeID:            "node2",
		P2PPort:           4002,
		DataDir:           "/home/debros/.debros/node2",
		RQLiteHTTPPort:    5002,
		RQLiteRaftPort:    7002,
		RQLiteJoinAddress: "localhost:5001",
		BootstrapPeers:    []string{bootstrapMultiaddr},
		ClusterAPIPort:    9104,
		IPFSAPIPort:       5002,
	}

	result, err := RenderNodeConfig(data)
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}

	// Check for required fields
	checks := []string{
		"id: \"node2\"",
		"type: \"node\"",
		"tcp/4002",
		"rqlite_port: 5002",
		"rqlite_raft_port: 7002",
		"rqlite_join_address: \"localhost:5001\"",
		bootstrapMultiaddr,
		"cluster_api_url: \"http://localhost:9104\"",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("Node config missing: %s", check)
		}
	}
}

func TestRenderGatewayConfig(t *testing.T) {
	bootstrapMultiaddr := "/ip4/127.0.0.1/tcp/4001/p2p/Qm1234567890"
	data := GatewayConfigData{
		ListenPort:     6001,
		BootstrapPeers: []string{bootstrapMultiaddr},
		OlricServers:   []string{"127.0.0.1:3320"},
		ClusterAPIPort: 9094,
		IPFSAPIPort:    5001,
	}

	result, err := RenderGatewayConfig(data)
	if err != nil {
		t.Fatalf("RenderGatewayConfig failed: %v", err)
	}

	// Check for required fields
	checks := []string{
		"listen_addr: \":6001\"",
		bootstrapMultiaddr,
		"127.0.0.1:3320",
		"ipfs_cluster_api_url: \"http://localhost:9094\"",
		"ipfs_api_url: \"http://localhost:5001\"",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("Gateway config missing: %s", check)
		}
	}
}

func TestRenderOlricConfig(t *testing.T) {
	data := OlricConfigData{
		BindAddr:       "127.0.0.1",
		HTTPPort:       3320,
		MemberlistPort: 3322,
	}

	result, err := RenderOlricConfig(data)
	if err != nil {
		t.Fatalf("RenderOlricConfig failed: %v", err)
	}

	// Check for required fields
	checks := []string{
		"bindAddr: \"127.0.0.1\"",
		"bindPort: 3320",
		"memberlist",
		"bindPort: 3322",
	}

	for _, check := range checks {
		if !strings.Contains(result, check) {
			t.Errorf("Olric config missing: %s", check)
		}
	}
}

func TestRenderWithMultipleBootstrapPeers(t *testing.T) {
	peers := []string{
		"/ip4/127.0.0.1/tcp/4001/p2p/Qm1111",
		"/ip4/127.0.0.1/tcp/4002/p2p/Qm2222",
	}

	data := NodeConfigData{
		NodeID:            "node-test",
		P2PPort:           4002,
		DataDir:           "/test/data",
		RQLiteHTTPPort:    5002,
		RQLiteRaftPort:    7002,
		RQLiteJoinAddress: "localhost:5001",
		BootstrapPeers:    peers,
		ClusterAPIPort:    9104,
		IPFSAPIPort:       5002,
	}

	result, err := RenderNodeConfig(data)
	if err != nil {
		t.Fatalf("RenderNodeConfig with multiple peers failed: %v", err)
	}

	for _, peer := range peers {
		if !strings.Contains(result, peer) {
			t.Errorf("Bootstrap peer missing: %s", peer)
		}
	}
}
