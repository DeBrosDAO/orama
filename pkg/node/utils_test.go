package node

import (
	"testing"
	"time"
)

func TestCalculateNextBackoff_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		current time.Duration
		want    time.Duration
	}{
		{
			name:    "1s becomes 1.5s",
			current: 1 * time.Second,
			want:    1500 * time.Millisecond,
		},
		{
			name:    "10s becomes 15s",
			current: 10 * time.Second,
			want:    15 * time.Second,
		},
		{
			name:    "7min becomes 10min (capped, not 10.5min)",
			current: 7 * time.Minute,
			want:    10 * time.Minute,
		},
		{
			name:    "10min stays at 10min (already at cap)",
			current: 10 * time.Minute,
			want:    10 * time.Minute,
		},
		{
			name:    "20s becomes 30s",
			current: 20 * time.Second,
			want:    30 * time.Second,
		},
		{
			name:    "5min becomes 7.5min",
			current: 5 * time.Minute,
			want:    7*time.Minute + 30*time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateNextBackoff(tt.current)
			if got != tt.want {
				t.Fatalf("calculateNextBackoff(%v) = %v, want %v", tt.current, got, tt.want)
			}
		})
	}
}

func TestAddJitter_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		input   time.Duration
		minWant time.Duration
		maxWant time.Duration
	}{
		{
			name:    "10s stays within plus/minus 20%",
			input:   10 * time.Second,
			minWant: 8 * time.Second,
			maxWant: 12 * time.Second,
		},
		{
			name:    "1s stays within plus/minus 20%",
			input:   1 * time.Second,
			minWant: 800 * time.Millisecond,
			maxWant: 1200 * time.Millisecond,
		},
		{
			name:    "very small input is clamped to at least 1s",
			input:   100 * time.Millisecond,
			minWant: 1 * time.Second,
			maxWant: 1 * time.Second, // will be checked as >=
		},
		{
			name:    "1 minute stays within plus/minus 20%",
			input:   1 * time.Minute,
			minWant: 48 * time.Second,
			maxWant: 72 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run multiple iterations because jitter is random
			for i := 0; i < 100; i++ {
				got := addJitter(tt.input)
				if got < tt.minWant {
					t.Fatalf("addJitter(%v) = %v, below minimum %v (iteration %d)", tt.input, got, tt.minWant, i)
				}
				if got > tt.maxWant {
					t.Fatalf("addJitter(%v) = %v, above maximum %v (iteration %d)", tt.input, got, tt.maxWant, i)
				}
			}
		})
	}
}

func TestAddJitter_MinimumIsOneSecond(t *testing.T) {
	// Even with zero or negative input, result should be at least 1 second
	inputs := []time.Duration{0, -1 * time.Second, 50 * time.Millisecond}
	for _, input := range inputs {
		for i := 0; i < 50; i++ {
			got := addJitter(input)
			if got < time.Second {
				t.Fatalf("addJitter(%v) = %v, want >= 1s", input, got)
			}
		}
	}
}

func TestExtractIPFromMultiaddr_TableDriven(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "IPv4 address",
			input: "/ip4/192.168.1.1/tcp/4001",
			want:  "192.168.1.1",
		},
		{
			name:  "IPv6 loopback address",
			input: "/ip6/::1/tcp/4001",
			want:  "::1",
		},
		{
			name:  "IPv4 with different port",
			input: "/ip4/10.0.0.5/tcp/8080",
			want:  "10.0.0.5",
		},
		{
			name:  "IPv4 loopback",
			input: "/ip4/127.0.0.1/tcp/4001",
			want:  "127.0.0.1",
		},
		{
			name:  "invalid multiaddr returns empty",
			input: "not-a-multiaddr",
			want:  "",
		},
		{
			name:  "empty string returns empty",
			input: "",
			want:  "",
		},
		{
			name:  "IPv4 with p2p component",
			input: "/ip4/203.0.113.50/tcp/4001/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
			want:  "203.0.113.50",
		},
		{
			name:  "IPv6 full address",
			input: "/ip6/2001:db8::1/tcp/4001",
			want:  "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractIPFromMultiaddr(tt.input)
			if got != tt.want {
				t.Fatalf("extractIPFromMultiaddr(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
