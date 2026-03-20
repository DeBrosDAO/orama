package ipfs

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
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
