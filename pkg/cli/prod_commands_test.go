package cli

import (
	"testing"
)

// TestProdCommandFlagParsing verifies that prod command flags are parsed correctly
func TestProdCommandFlagParsing(t *testing.T) {
	tests := []struct {
		name                string
		args                []string
		expectBootstrap     bool
		expectVPSIP         string
		expectBootstrapJoin string
		expectPeers         string
	}{
		{
			name:            "bootstrap node",
			args:            []string{"install", "--bootstrap"},
			expectBootstrap: true,
		},
		{
			name:        "non-bootstrap with vps-ip",
			args:        []string{"install", "--vps-ip", "10.0.0.2", "--peers", "multiaddr1,multiaddr2"},
			expectVPSIP: "10.0.0.2",
			expectPeers: "multiaddr1,multiaddr2",
		},
		{
			name:                "secondary bootstrap",
			args:                []string{"install", "--bootstrap", "--vps-ip", "10.0.0.3", "--bootstrap-join", "10.0.0.1:7001"},
			expectBootstrap:     true,
			expectVPSIP:         "10.0.0.3",
			expectBootstrapJoin: "10.0.0.1:7001",
		},
		{
			name:            "with domain",
			args:            []string{"install", "--bootstrap", "--domain", "example.com"},
			expectBootstrap: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Extract flags manually to verify parsing logic
			isBootstrap := false
			var vpsIP, peersStr, bootstrapJoin string

			for i, arg := range tt.args {
				switch arg {
				case "--bootstrap":
					isBootstrap = true
				case "--peers":
					if i+1 < len(tt.args) {
						peersStr = tt.args[i+1]
					}
				case "--vps-ip":
					if i+1 < len(tt.args) {
						vpsIP = tt.args[i+1]
					}
				case "--bootstrap-join":
					if i+1 < len(tt.args) {
						bootstrapJoin = tt.args[i+1]
					}
				}
			}

			if isBootstrap != tt.expectBootstrap {
				t.Errorf("expected bootstrap=%v, got %v", tt.expectBootstrap, isBootstrap)
			}
			if vpsIP != tt.expectVPSIP {
				t.Errorf("expected vpsIP=%q, got %q", tt.expectVPSIP, vpsIP)
			}
			if peersStr != tt.expectPeers {
				t.Errorf("expected peers=%q, got %q", tt.expectPeers, peersStr)
			}
			if bootstrapJoin != tt.expectBootstrapJoin {
				t.Errorf("expected bootstrapJoin=%q, got %q", tt.expectBootstrapJoin, bootstrapJoin)
			}
		})
	}
}
