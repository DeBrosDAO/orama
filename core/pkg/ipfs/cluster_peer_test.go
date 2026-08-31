package ipfs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multiformats/go-multiaddr"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
)

func TestExtractIPFromMultiaddr(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected string
	}{
		{
			name:     "ipv4 tcp address",
			addr:     "/ip4/10.0.0.1/tcp/4001",
			expected: "10.0.0.1",
		},
		{
			name:     "ipv4 public address",
			addr:     "/ip4/203.0.113.5/tcp/4001",
			expected: "203.0.113.5",
		},
		{
			name:     "ipv4 loopback",
			addr:     "/ip4/127.0.0.1/tcp/4001",
			expected: "127.0.0.1",
		},
		{
			name:     "ipv6 address",
			addr:     "/ip6/::1/tcp/4001",
			expected: "[::1]",
		},
		{
			name:     "wireguard ip with udp",
			addr:     "/ip4/10.0.0.3/udp/4001/quic",
			expected: "10.0.0.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ma, err := multiaddr.NewMultiaddr(tt.addr)
			if err != nil {
				t.Fatalf("failed to parse multiaddr %q: %v", tt.addr, err)
			}
			got := extractIPFromMultiaddr(ma)
			if got != tt.expected {
				t.Errorf("extractIPFromMultiaddr(%q) = %q, want %q", tt.addr, got, tt.expected)
			}
		})
	}
}

func TestExtractIPFromMultiaddr_Nil(t *testing.T) {
	got := extractIPFromMultiaddr(nil)
	if got != "" {
		t.Errorf("extractIPFromMultiaddr(nil) = %q, want empty string", got)
	}
}

// TestWireGuardIPFiltering verifies that only 10.0.0.x IPs would be selected
// for peer discovery queries. This tests the filtering logic used in
// DiscoverClusterPeersFromLibP2P.
func TestWireGuardIPFiltering(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		accepted bool
	}{
		{"wireguard ip", "/ip4/10.0.0.1/tcp/4001", true},
		{"wireguard ip high", "/ip4/10.0.0.254/tcp/4001", true},
		{"public ip", "/ip4/203.0.113.5/tcp/4001", false},
		{"private 192.168", "/ip4/192.168.1.1/tcp/4001", false},
		{"private 172.16", "/ip4/172.16.0.1/tcp/4001", false},
		{"loopback", "/ip4/127.0.0.1/tcp/4001", false},
		{"different 10.x subnet", "/ip4/10.1.0.1/tcp/4001", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ma, err := multiaddr.NewMultiaddr(tt.addr)
			if err != nil {
				t.Fatalf("failed to parse multiaddr: %v", err)
			}
			ip := extractIPFromMultiaddr(ma)
			// Replicate the filtering logic from DiscoverClusterPeersFromLibP2P
			accepted := ip != "" && len(ip) >= 7 && ip[:7] == "10.0.0."
			if accepted != tt.accepted {
				t.Errorf("IP %q: accepted=%v, want %v", ip, accepted, tt.accepted)
			}
		})
	}
}

// Bugboard #153 blocker.
//
// IPFS-Cluster runs CRDT consensus, where an untrusted peer's writes are
// silently dropped by every peer that does not trust it. The trust set used to
// be an allowlist assembled per node from a file: a joining node received the
// current file from whichever node it joined through and appended itself, so it
// trusted {bootstrap set} ∪ {self} — and nothing ever added the joiner to the
// nodes already running. The bootstrap node therefore trusted only itself.
//
// The consequence was silent and severe: a pin or unpin served by any other
// node returned HTTP 200, applied locally, and was discarded everywhere else.
// Tenant traffic is spread across nodes by round-robin DNS, so most storage
// writes and deletes never replicated, and privacy-grade immediate reclaim
// could not work because the blocks it evicted were still pinned elsewhere.
//
// Membership is gated by the shared cluster secret, the WireGuard mesh and
// invite-token enrolment. Every peer that clears those is a trusted writer.
func TestEnsureConfig_trustsEveryAuthenticatedPeer(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "ipfs-cluster")
	if err := os.MkdirAll(clusterPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing allowlist naming ONE peer — the exact state that broke
	// replication on devnet.
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	trustedFile := filepath.Join(secretsDir, "ipfs-cluster-trusted-peers")
	if err := os.WriteFile(trustedFile, []byte("12D3KooWOnlyTheBootstrapPeer\n"), 0o600); err != nil {
		t.Fatalf("write trusted peers: %v", err)
	}

	// A pre-existing service.json so EnsureConfig edits it in place instead of
	// shelling out to `ipfs-cluster-service init`.
	serviceJSON := filepath.Join(clusterPath, "service.json")
	if err := os.WriteFile(serviceJSON, []byte(`{"cluster":{},"consensus":{"crdt":{"trusted_peers":["12D3KooWOnlyTheBootstrapPeer"]}}}`), 0o600); err != nil {
		t.Fatalf("write service.json: %v", err)
	}
	// identity.json so the peer ID is still recorded into the shared file.
	if err := os.WriteFile(filepath.Join(clusterPath, "identity.json"),
		[]byte(`{"id":"12D3KooWThisNode"}`), 0o600); err != nil {
		t.Fatalf("write identity.json: %v", err)
	}

	cfg := &config.Config{}
	cfg.Database.IPFS.ClusterAPIURL = "http://localhost:9094"
	cfg.Database.IPFS.APIURL = "http://localhost:4501"
	cfg.Node.DataDir = dir
	cfg.Node.ID = "node-1"

	cm := &ClusterConfigManager{
		cfg:              cfg,
		logger:           zap.NewNop(),
		clusterPath:      clusterPath,
		secret:           "test-secret",
		trustedPeersPath: trustedFile,
	}
	if err := cm.EnsureConfig(); err != nil {
		t.Fatalf("EnsureConfig: %v", err)
	}

	raw, err := os.ReadFile(serviceJSON)
	if err != nil {
		t.Fatalf("read service.json: %v", err)
	}
	var out struct {
		Consensus struct {
			CRDT struct {
				TrustedPeers []string `json:"trusted_peers"`
			} `json:"crdt"`
		} `json:"consensus"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode service.json: %v", err)
	}
	got := out.Consensus.CRDT.TrustedPeers
	if len(got) != 1 || got[0] != "*" {
		t.Errorf("trusted_peers = %v, want [\"*\"] — a per-node allowlist cannot stay consistent across joins, and an untrusted peer's pins and unpins are silently dropped by everyone else", got)
	}

	// The shared peer-ID file is still maintained for the join handshake.
	peersFile, err := os.ReadFile(trustedFile)
	if err != nil {
		t.Fatalf("read peers file: %v", err)
	}
	if !strings.Contains(string(peersFile), "12D3KooWThisNode") {
		t.Errorf("this node's cluster peer ID was not recorded for the join handshake; file = %q", peersFile)
	}
}
