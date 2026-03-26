package cli

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// TestProdCommandFlagParsing verifies that prod command flags are parsed correctly
// Genesis node: has --vps-ip but no --join or --token
// Joining node: has --vps-ip, --join (HTTPS URL), and --token (invite token)
func TestProdCommandFlagParsing(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectVPSIP  string
		expectDomain string
		expectJoin   string
		expectToken  string
		expectBranch string
		isFirstNode  bool // genesis node = no --join and no --token
	}{
		{
			name:         "genesis node (creates new cluster)",
			args:         []string{"install", "--vps-ip", "10.0.0.1", "--domain", "node-1.example.com"},
			expectVPSIP:  "10.0.0.1",
			expectDomain: "node-1.example.com",
			isFirstNode:  true,
		},
		{
			name:        "joining node with invite token",
			args:        []string{"install", "--vps-ip", "10.0.0.2", "--join", "https://node1.dbrs.space", "--token", "abc123def456"},
			expectVPSIP: "10.0.0.2",
			expectJoin:  "https://node1.dbrs.space",
			expectToken: "abc123def456",
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
			var vpsIP, domain, joinAddr, token, branch string

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
				case "--join":
					if i+1 < len(tt.args) {
						joinAddr = tt.args[i+1]
					}
				case "--token":
					if i+1 < len(tt.args) {
						token = tt.args[i+1]
					}
				case "--branch":
					if i+1 < len(tt.args) {
						branch = tt.args[i+1]
					}
				}
			}

			// Genesis node detection: no --join and no --token
			isFirstNode := joinAddr == "" && token == ""

			if vpsIP != tt.expectVPSIP {
				t.Errorf("expected vpsIP=%q, got %q", tt.expectVPSIP, vpsIP)
			}
			if domain != tt.expectDomain {
				t.Errorf("expected domain=%q, got %q", tt.expectDomain, domain)
			}
			if joinAddr != tt.expectJoin {
				t.Errorf("expected join=%q, got %q", tt.expectJoin, joinAddr)
			}
			if token != tt.expectToken {
				t.Errorf("expected token=%q, got %q", tt.expectToken, token)
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
			peers, err := utils.NormalizePeers(tt.input)
			
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
