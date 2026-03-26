package deployments

import (
	"testing"
)

func TestGetShortNodeID(t *testing.T) {
	tests := []struct {
		name   string
		peerID string
		want   string
	}{
		{
			name:   "full peer ID extracts chars 8-14",
			peerID: "12D3KooWGqyuQR8Nxyz1234567890abcdef",
			want:   "node-GqyuQR",
		},
		{
			name:   "another full peer ID",
			peerID: "12D3KooWAbCdEf9Hxyz1234567890abcdef",
			want:   "node-AbCdEf",
		},
		{
			name:   "short ID under 20 chars returned as-is",
			peerID: "node-GqyuQR",
			want:   "node-GqyuQR",
		},
		{
			name:   "already short arbitrary string",
			peerID: "short",
			want:   "short",
		},
		{
			name:   "exactly 20 chars gets prefix extraction",
			peerID: "12345678901234567890",
			want:   "node-901234",
		},
		{
			name:   "string of length 14 returned as-is (under 20)",
			peerID: "12D3KooWAbCdEf",
			want:   "12D3KooWAbCdEf",
		},
		{
			name:   "empty string returned as-is (under 20)",
			peerID: "",
			want:   "",
		},
		{
			name:   "19 chars returned as-is",
			peerID: "1234567890123456789",
			want:   "1234567890123456789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetShortNodeID(tt.peerID)
			if got != tt.want {
				t.Fatalf("GetShortNodeID(%q) = %q, want %q", tt.peerID, got, tt.want)
			}
		})
	}
}

func TestGenerateRandomSuffix_Length(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{name: "length 6", length: 6},
		{name: "length 1", length: 1},
		{name: "length 10", length: 10},
		{name: "length 20", length: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateRandomSuffix(tt.length)
			if len(got) != tt.length {
				t.Fatalf("generateRandomSuffix(%d) returned string of length %d, want %d", tt.length, len(got), tt.length)
			}
		})
	}
}

func TestGenerateRandomSuffix_AllowedCharacters(t *testing.T) {
	allowed := "abcdefghijklmnopqrstuvwxyz0123456789"
	allowedSet := make(map[rune]bool, len(allowed))
	for _, c := range allowed {
		allowedSet[c] = true
	}

	// Generate many suffixes and check every character
	for i := 0; i < 100; i++ {
		suffix := generateRandomSuffix(subdomainSuffixLength)
		for j, c := range suffix {
			if !allowedSet[c] {
				t.Fatalf("generateRandomSuffix() returned disallowed character %q at position %d in %q", c, j, suffix)
			}
		}
	}
}

func TestGenerateRandomSuffix_Uniqueness(t *testing.T) {
	// Two calls should produce different values (with overwhelming probability)
	a := generateRandomSuffix(subdomainSuffixLength)
	b := generateRandomSuffix(subdomainSuffixLength)

	// Run a few more attempts in case of a rare collision
	different := a != b
	if !different {
		for i := 0; i < 10; i++ {
			c := generateRandomSuffix(subdomainSuffixLength)
			if c != a {
				different = true
				break
			}
		}
	}

	if !different {
		t.Fatalf("generateRandomSuffix() produced the same value %q in multiple calls, expected uniqueness", a)
	}
}
