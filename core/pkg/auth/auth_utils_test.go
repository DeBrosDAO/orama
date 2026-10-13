package auth

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// extractDomainFromURL
// ---------------------------------------------------------------------------

func TestExtractDomainFromURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "https with domain only",
			input: "https://example.com",
			want:  "example.com",
		},
		{
			name:  "http with port and path",
			input: "http://example.com:8080/path",
			want:  "example.com",
		},
		{
			name:  "https with subdomain and path",
			input: "https://sub.domain.com/api/v1",
			want:  "sub.domain.com",
		},
		{
			name:  "no scheme bare domain",
			input: "example.com",
			want:  "example.com",
		},
		{
			name:  "https with IP and port",
			input: "https://192.168.1.1:443",
			want:  "192.168.1.1",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "bare domain no scheme",
			input: "gateway.orama.network",
			want:  "gateway.orama.network",
		},
		{
			name:  "https with query params",
			input: "https://example.com?foo=bar",
			want:  "example.com",
		},
		{
			name:  "https with path and query params",
			input: "https://example.com/page?q=1&r=2",
			want:  "example.com",
		},
		{
			name:  "bare domain with port",
			input: "example.com:9090",
			want:  "example.com",
		},
		{
			name:  "https with fragment",
			input: "https://example.com/page#section",
			want:  "example.com",
		},
		{
			name:  "https with user info",
			input: "https://user:pass@example.com/path",
			want:  "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomainFromURL(tt.input)
			if got != tt.want {
				t.Errorf("extractDomainFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ValidateWalletAddress
// ---------------------------------------------------------------------------

func TestValidateEVMWalletAddress(t *testing.T) {
	validHex40 := "aabbccddee1122334455aabbccddee1122334455"

	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{
			name:    "valid 40 char hex with 0x prefix",
			address: "0x" + validHex40,
			want:    true,
		},
		{
			name:    "valid 40 char hex without prefix",
			address: validHex40,
			want:    true,
		},
		{
			name:    "valid uppercase hex with 0x prefix",
			address: "0x" + strings.ToUpper(validHex40),
			want:    true,
		},
		{
			name:    "too short",
			address: "0xaabbccdd",
			want:    false,
		},
		{
			name:    "too long",
			address: "0x" + validHex40 + "ff",
			want:    false,
		},
		{
			name:    "non hex characters",
			address: "0x" + "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			want:    false,
		},
		{
			name:    "empty string",
			address: "",
			want:    false,
		},
		{
			name:    "just 0x prefix",
			address: "0x",
			want:    false,
		},
		{
			name:    "39 hex chars with 0x prefix",
			address: "0x" + validHex40[:39],
			want:    false,
		},
		{
			name:    "41 hex chars with 0x prefix",
			address: "0x" + validHex40 + "a",
			want:    false,
		},
		{
			name:    "mixed case hex is valid",
			address: "0xAaBbCcDdEe1122334455aAbBcCdDeE1122334455",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateEVMWalletAddress(tt.address)
			if got != tt.want {
				t.Errorf("validateEVMWalletAddress(%q) = %v, want %v", tt.address, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatWalletAddress
// ---------------------------------------------------------------------------
