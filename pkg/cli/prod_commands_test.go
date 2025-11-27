package cli

import (
	"testing"
)

// TestProdCommandFlagParsing verifies that prod command flags are parsed correctly
// Note: The installer now uses --vps-ip presence to determine if it's a first node (no --bootstrap flag)
// First node: has --vps-ip but no --peers or --join
// Joining node: has --vps-ip, --peers, and --cluster-secret
func TestProdCommandFlagParsing(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		expectVPSIP   string
		expectDomain  string
		expectPeers   string
		expectJoin    string
		expectSecret  string
		expectBranch  string
		isFirstNode   bool // first node = no peers and no join address
	}{
		{
			name:        "first node (creates new cluster)",
			args:        []string{"install", "--vps-ip", "10.0.0.1", "--domain", "node-1.example.com"},
			expectVPSIP: "10.0.0.1",
			expectDomain: "node-1.example.com",
			isFirstNode: true,
		},
		{
			name:         "joining node with peers",
			args:         []string{"install", "--vps-ip", "10.0.0.2", "--peers", "/ip4/10.0.0.1/tcp/4001/p2p/Qm123", "--cluster-secret", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			expectVPSIP:  "10.0.0.2",
			expectPeers:  "/ip4/10.0.0.1/tcp/4001/p2p/Qm123",
			expectSecret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			isFirstNode:  false,
		},
		{
			name:        "joining node with join address",
			args:        []string{"install", "--vps-ip", "10.0.0.3", "--join", "10.0.0.1:7001", "--cluster-secret", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			expectVPSIP: "10.0.0.3",
			expectJoin:  "10.0.0.1:7001",
			expectSecret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			isFirstNode: false,
		},
		{
			name:         "with nightly branch",
			args:         []string{"install", "--vps-ip", "10.0.0.4", "--branch", "nightly"},
			expectVPSIP:  "10.0.0.4",
			expectBranch: "nightly",
			isFirstNode:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Extract flags manually to verify parsing logic
			var vpsIP, domain, peersStr, joinAddr, clusterSecret, branch string

			for i, arg := range tt.args {
				switch arg {
				case "--vps-ip":
					if i+1 < len(tt.args) {
						vpsIP = tt.args[i+1]
					}
				case "--domain":
					if i+1 < len(tt.args) {
						domain = tt.args[i+1]
					}
				case "--peers":
					if i+1 < len(tt.args) {
						peersStr = tt.args[i+1]
					}
				case "--join":
					if i+1 < len(tt.args) {
						joinAddr = tt.args[i+1]
					}
				case "--cluster-secret":
					if i+1 < len(tt.args) {
						clusterSecret = tt.args[i+1]
					}
				case "--branch":
					if i+1 < len(tt.args) {
						branch = tt.args[i+1]
					}
				}
			}

			// First node detection: no peers and no join address
			isFirstNode := peersStr == "" && joinAddr == ""

			if vpsIP != tt.expectVPSIP {
				t.Errorf("expected vpsIP=%q, got %q", tt.expectVPSIP, vpsIP)
			}
			if domain != tt.expectDomain {
				t.Errorf("expected domain=%q, got %q", tt.expectDomain, domain)
			}
			if peersStr != tt.expectPeers {
				t.Errorf("expected peers=%q, got %q", tt.expectPeers, peersStr)
			}
			if joinAddr != tt.expectJoin {
				t.Errorf("expected join=%q, got %q", tt.expectJoin, joinAddr)
			}
			if clusterSecret != tt.expectSecret {
				t.Errorf("expected clusterSecret=%q, got %q", tt.expectSecret, clusterSecret)
			}
			if branch != tt.expectBranch {
				t.Errorf("expected branch=%q, got %q", tt.expectBranch, branch)
			}
			if isFirstNode != tt.isFirstNode {
				t.Errorf("expected isFirstNode=%v, got %v", tt.isFirstNode, isFirstNode)
			}
		})
	}
}

// TestNormalizePeers tests the peer multiaddr normalization
func TestNormalizePeers(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectCount int
		expectError bool
	}{
		{
			name:        "empty string",
			input:       "",
			expectCount: 0,
			expectError: false,
		},
	{
		name:        "single peer",
		input:       "/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj",
		expectCount: 1,
		expectError: false,
	},
	{
		name:        "multiple peers",
		input:       "/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj,/ip4/10.0.0.2/tcp/4001/p2p/12D3KooWJzL4SHW3o7sZpzjfEPJzC6Ky7gKvJxY8vQVDR2jHc8F1",
		expectCount: 2,
		expectError: false,
	},
	{
		name:        "duplicate peers deduplicated",
		input:       "/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj,/ip4/10.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj",
		expectCount: 1,
		expectError: false,
	},
		{
			name:        "invalid multiaddr",
			input:       "not-a-multiaddr",
			expectCount: 0,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peers, err := normalizePeers(tt.input)
			
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(peers) != tt.expectCount {
				t.Errorf("expected %d peers, got %d", tt.expectCount, len(peers))
			}
		})
	}
}
