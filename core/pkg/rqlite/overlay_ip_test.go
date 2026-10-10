package rqlite

import "testing"

// Raft, the HTTP API and every other inter-node endpoint are reached over the
// WireGuard mesh; the public interface has those ports closed by UFW. Selection
// used to prefer a PUBLIC address and fall back to the overlay, so rewriting a
// peer's advertised raft address replaced a reachable endpoint with one that
// could never be dialled — a node made undiallable by the act of discovering it.
func TestSelectOverlayIP(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		want       string
	}{
		{"nothing known", nil, ""},
		{"overlay only", []string{"10.0.0.4"}, "10.0.0.4"},
		{
			name:       "a public address must never win over the overlay",
			candidates: []string{"51.83.128.181", "10.0.0.4"},
			want:       "10.0.0.4",
		},
		{
			name:       "overlay first is still the overlay",
			candidates: []string{"10.0.0.7", "203.0.113.9"},
			want:       "10.0.0.7",
		},
		{
			// Refusing to rewrite leaves whatever the peer advertised, which
			// is at worst stale. A public IP is worse: confidently wrong.
			name:       "no overlay address means no rewrite",
			candidates: []string{"203.0.113.9", "198.51.100.7"},
			want:       "",
		},
		{
			name:       "private but not the mesh is not the overlay",
			candidates: []string{"192.168.1.10", "172.16.0.4"},
			want:       "",
		},
		{"loopback is skipped", []string{"127.0.0.1", "10.0.0.9"}, "10.0.0.9"},
		{"unspecified is skipped", []string{"0.0.0.0", "10.0.0.9"}, "10.0.0.9"},
		{"garbage is skipped", []string{"not-an-ip", "10.0.0.9"}, "10.0.0.9"},
		{"the edge of the range", []string{"10.0.0.255"}, "10.0.0.255"},
		{"just outside the range", []string{"10.0.1.1"}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectOverlayIP(tc.candidates); got != tc.want {
				t.Fatalf("selectOverlayIP(%v) = %q, want %q", tc.candidates, got, tc.want)
			}
		})
	}
}

func TestIsOverlayIP(t *testing.T) {
	tests := map[string]bool{
		"10.0.0.1":      true,
		"10.0.0.254":    true,
		"10.0.1.1":      false,
		"10.1.0.1":      false,
		"192.168.0.1":   false,
		"51.83.128.181": false,
		"127.0.0.1":     false,
		"":              false,
		"::1":           false,
	}

	for ip, want := range tests {
		t.Run(ip, func(t *testing.T) {
			if got := isOverlayIP(ip); got != want {
				t.Fatalf("isOverlayIP(%q) = %v, want %v", ip, got, want)
			}
		})
	}
}
