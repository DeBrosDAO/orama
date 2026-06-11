package templates

import (
	"strings"
	"testing"
)

func TestRenderNodeConfig(t *testing.T) {
	bootstrapMultiaddr := "/ip4/127.0.0.1/tcp/4001/p2p/Qm1234567890"
	data := NodeConfigData{
		NodeID:            "node2",
		P2PPort:           4002,
		DataDir:           "/opt/orama/.orama/node2",
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
		"tcp/4002",
		"rqlite_port: 5002",
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

func TestRenderNodeConfig_secretsEncryptionKey(t *testing.T) {
	const key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Happy path: key present → rendered under http_gateway.
	withKey, err := RenderNodeConfig(NodeConfigData{
		NodeID:               "node1",
		SecretsEncryptionKey: key,
	})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	want := "secrets_encryption_key: \"" + key + "\""
	if !strings.Contains(withKey, want) {
		t.Errorf("rendered node config missing secrets key line %q\n---\n%s", want, withKey)
	}

	// Edge case: empty key → line omitted entirely (no empty value rendered).
	withoutKey, err := RenderNodeConfig(NodeConfigData{NodeID: "node1"})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	if strings.Contains(withoutKey, "secrets_encryption_key") {
		t.Errorf("empty key should omit secrets_encryption_key line, got:\n%s", withoutKey)
	}
}

func TestRenderNodeConfig_webRTC(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// Happy path: TURN secret present → full webrtc block rendered.
	withWebRTC, err := RenderNodeConfig(NodeConfigData{
		NodeID:        "node1",
		WebRTCEnabled: true,
		SFUPort:       30007,
		TURNDomain:    "turn.ns-anchat.dbrs.space",
		TURNSecret:    secret,
	})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	for _, want := range []string{
		"webrtc:",
		"enabled: true",
		"sfu_port: 30007",
		"turn_domain: \"turn.ns-anchat.dbrs.space\"",
		"turn_secret: \"" + secret + "\"",
	} {
		if !strings.Contains(withWebRTC, want) {
			t.Errorf("rendered node config missing webrtc line %q\n---\n%s", want, withWebRTC)
		}
	}

	// Edge case: no TURN secret → block omitted entirely.
	withoutWebRTC, err := RenderNodeConfig(NodeConfigData{NodeID: "node1"})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	if strings.Contains(withoutWebRTC, "webrtc:") {
		t.Errorf("empty TURN secret should omit webrtc block, got:\n%s", withoutWebRTC)
	}
}

func TestRenderNodeConfig_sniRouter(t *testing.T) {
	// Enabled: top-level sni_router block renders enabled: true.
	enabled, err := RenderNodeConfig(NodeConfigData{
		NodeID:           "node1",
		SNIRouterEnabled: true,
	})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	if !strings.Contains(enabled, "sni_router:") {
		t.Errorf("rendered node config missing sni_router block\n---\n%s", enabled)
	}
	if !strings.Contains(enabled, "enabled: true") {
		t.Errorf("sni_router should render enabled: true\n---\n%s", enabled)
	}

	// Default: the block is always present, defaulting to false (so the flag is
	// discoverable to operators and round-trips through regen).
	disabled, err := RenderNodeConfig(NodeConfigData{NodeID: "node1"})
	if err != nil {
		t.Fatalf("RenderNodeConfig failed: %v", err)
	}
	if !strings.Contains(disabled, "sni_router:") {
		t.Errorf("sni_router block should always be present\n---\n%s", disabled)
	}
	if !strings.Contains(disabled, "enabled: false") {
		t.Errorf("default sni_router should render enabled: false\n---\n%s", disabled)
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
		ServerBindAddr:        "127.0.0.1",
		HTTPPort:              3320,
		MemberlistBindAddr:    "0.0.0.0",
		MemberlistPort:        3322,
		MemberlistEnvironment: "lan",
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
		"environment: lan",
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
