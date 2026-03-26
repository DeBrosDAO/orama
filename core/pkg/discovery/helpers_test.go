package discovery

import (
	"testing"

	"github.com/multiformats/go-multiaddr"
)

func mustMultiaddr(t *testing.T, s string) multiaddr.Multiaddr {
	t.Helper()
	ma, err := multiaddr.NewMultiaddr(s)
	if err != nil {
		t.Fatalf("failed to parse multiaddr %q: %v", s, err)
	}
	return ma
}

func TestFilterLibp2pAddrs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		wantLen  int
		wantAll  bool // if true, expect all input addrs returned
	}{
		{
			name:    "only port 4001 addresses are all returned",
			input:   []string{"/ip4/192.168.1.1/tcp/4001", "/ip4/10.0.0.1/tcp/4001"},
			wantLen: 2,
			wantAll: true,
		},
		{
			name:    "mixed ports return only 4001",
			input:   []string{"/ip4/192.168.1.1/tcp/4001", "/ip4/10.0.0.1/tcp/9096", "/ip4/172.16.0.1/tcp/4101"},
			wantLen: 1,
			wantAll: false,
		},
		{
			name:    "empty list returns empty result",
			input:   []string{},
			wantLen: 0,
			wantAll: true,
		},
		{
			name:    "no port 4001 returns empty result",
			input:   []string{"/ip4/192.168.1.1/tcp/9096", "/ip4/10.0.0.1/tcp/4101", "/ip4/172.16.0.1/tcp/8080"},
			wantLen: 0,
			wantAll: false,
		},
		{
			name:    "addresses without TCP protocol are skipped",
			input:   []string{"/ip4/192.168.1.1/udp/4001", "/ip4/10.0.0.1/tcp/4001"},
			wantLen: 1,
			wantAll: false,
		},
		{
			name:    "multiple port 4001 with different IPs",
			input:   []string{"/ip4/1.2.3.4/tcp/4001", "/ip6/::1/tcp/4001", "/ip4/5.6.7.8/tcp/4001"},
			wantLen: 3,
			wantAll: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs := make([]multiaddr.Multiaddr, 0, len(tt.input))
			for _, s := range tt.input {
				addrs = append(addrs, mustMultiaddr(t, s))
			}

			got := filterLibp2pAddrs(addrs)

			if len(got) != tt.wantLen {
				t.Fatalf("filterLibp2pAddrs() returned %d addrs, want %d", len(got), tt.wantLen)
			}

			if tt.wantAll && len(got) != len(addrs) {
				t.Fatalf("expected all %d addrs returned, got %d", len(addrs), len(got))
			}

			// Verify every returned address is actually port 4001
			for _, addr := range got {
				port, err := addr.ValueForProtocol(multiaddr.P_TCP)
				if err != nil {
					t.Fatalf("returned addr %s has no TCP protocol: %v", addr, err)
				}
				if port != "4001" {
					t.Fatalf("returned addr %s has port %s, want 4001", addr, port)
				}
			}
		})
	}
}

func TestFilterLibp2pAddrs_NilSlice(t *testing.T) {
	got := filterLibp2pAddrs(nil)
	if len(got) != 0 {
		t.Fatalf("filterLibp2pAddrs(nil) returned %d addrs, want 0", len(got))
	}
}

func TestHasLibp2pAddr(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  bool
	}{
		{
			name:  "has port 4001",
			input: []string{"/ip4/192.168.1.1/tcp/4001"},
			want:  true,
		},
		{
			name:  "has port 4001 among others",
			input: []string{"/ip4/10.0.0.1/tcp/9096", "/ip4/192.168.1.1/tcp/4001", "/ip4/172.16.0.1/tcp/4101"},
			want:  true,
		},
		{
			name:  "has other ports but not 4001",
			input: []string{"/ip4/192.168.1.1/tcp/9096", "/ip4/10.0.0.1/tcp/4101", "/ip4/172.16.0.1/tcp/8080"},
			want:  false,
		},
		{
			name:  "empty list",
			input: []string{},
			want:  false,
		},
		{
			name:  "UDP port 4001 does not count",
			input: []string{"/ip4/192.168.1.1/udp/4001"},
			want:  false,
		},
		{
			name:  "IPv6 with port 4001",
			input: []string{"/ip6/::1/tcp/4001"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addrs := make([]multiaddr.Multiaddr, 0, len(tt.input))
			for _, s := range tt.input {
				addrs = append(addrs, mustMultiaddr(t, s))
			}

			got := hasLibp2pAddr(addrs)
			if got != tt.want {
				t.Fatalf("hasLibp2pAddr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasLibp2pAddr_NilSlice(t *testing.T) {
	got := hasLibp2pAddr(nil)
	if got != false {
		t.Fatalf("hasLibp2pAddr(nil) = %v, want false", got)
	}
}
