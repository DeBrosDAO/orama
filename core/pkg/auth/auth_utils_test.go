package auth

import (
	"encoding/hex"
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

func TestValidateWalletAddress(t *testing.T) {
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
			got := ValidateWalletAddress(tt.address)
			if got != tt.want {
				t.Errorf("ValidateWalletAddress(%q) = %v, want %v", tt.address, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FormatWalletAddress
// ---------------------------------------------------------------------------

func TestFormatWalletAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    string
	}{
		{
			name:    "already lowercase with 0x",
			address: "0xaabbccddee1122334455aabbccddee1122334455",
			want:    "0xaabbccddee1122334455aabbccddee1122334455",
		},
		{
			name:    "uppercase gets lowercased",
			address: "0xAABBCCDDEE1122334455AABBCCDDEE1122334455",
			want:    "0xaabbccddee1122334455aabbccddee1122334455",
		},
		{
			name:    "without 0x prefix gets it added",
			address: "aabbccddee1122334455aabbccddee1122334455",
			want:    "0xaabbccddee1122334455aabbccddee1122334455",
		},
		{
			name:    "0X uppercase prefix gets normalized",
			address: "0XAABBCCDDEE1122334455AABBCCDDEE1122334455",
			want:    "0xaabbccddee1122334455aabbccddee1122334455",
		},
		{
			name:    "mixed case gets normalized",
			address: "0xAaBbCcDdEe1122334455AaBbCcDdEe1122334455",
			want:    "0xaabbccddee1122334455aabbccddee1122334455",
		},
		{
			name:    "empty string gets 0x prefix",
			address: "",
			want:    "0x",
		},
		{
			name:    "just 0x stays as 0x",
			address: "0x",
			want:    "0x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatWalletAddress(tt.address)
			if got != tt.want {
				t.Errorf("FormatWalletAddress(%q) = %q, want %q", tt.address, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GenerateRandomString
// ---------------------------------------------------------------------------

func TestGenerateRandomString(t *testing.T) {
	t.Run("returns correct length", func(t *testing.T) {
		lengths := []int{8, 16, 32, 64}
		for _, l := range lengths {
			s, err := GenerateRandomString(l)
			if err != nil {
				t.Fatalf("GenerateRandomString(%d) returned error: %v", l, err)
			}
			if len(s) != l {
				t.Errorf("GenerateRandomString(%d) returned string of length %d, want %d", l, len(s), l)
			}
		}
	})

	t.Run("two calls produce different values", func(t *testing.T) {
		s1, err := GenerateRandomString(32)
		if err != nil {
			t.Fatalf("first call returned error: %v", err)
		}
		s2, err := GenerateRandomString(32)
		if err != nil {
			t.Fatalf("second call returned error: %v", err)
		}
		if s1 == s2 {
			t.Errorf("two calls to GenerateRandomString(32) produced the same value: %q", s1)
		}
	})

	t.Run("returns hex characters only", func(t *testing.T) {
		s, err := GenerateRandomString(32)
		if err != nil {
			t.Fatalf("GenerateRandomString(32) returned error: %v", err)
		}
		// hex.DecodeString requires even-length input; pad if needed
		toDecode := s
		if len(toDecode)%2 != 0 {
			toDecode = toDecode + "0"
		}
		if _, err := hex.DecodeString(toDecode); err != nil {
			t.Errorf("GenerateRandomString(32) returned non-hex string: %q, err: %v", s, err)
		}
	})

	t.Run("length zero returns empty string", func(t *testing.T) {
		s, err := GenerateRandomString(0)
		if err != nil {
			t.Fatalf("GenerateRandomString(0) returned error: %v", err)
		}
		if s != "" {
			t.Errorf("GenerateRandomString(0) = %q, want empty string", s)
		}
	})

	t.Run("length one returns single hex char", func(t *testing.T) {
		s, err := GenerateRandomString(1)
		if err != nil {
			t.Fatalf("GenerateRandomString(1) returned error: %v", err)
		}
		if len(s) != 1 {
			t.Errorf("GenerateRandomString(1) returned string of length %d, want 1", len(s))
		}
		// Must be a valid hex character
		const hexChars = "0123456789abcdef"
		if !strings.Contains(hexChars, s) {
			t.Errorf("GenerateRandomString(1) = %q, not a valid hex character", s)
		}
	})
}
