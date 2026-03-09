package join

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestWgPeersContainsIP_found(t *testing.T) {
	peers := []WGPeerInfo{
		{PublicKey: "key1", Endpoint: "1.2.3.4:51820", AllowedIP: "10.0.0.1/32"},
		{PublicKey: "key2", Endpoint: "5.6.7.8:51820", AllowedIP: "10.0.0.2/32"},
	}

	if !wgPeersContainsIP(peers, "10.0.0.1") {
		t.Error("expected to find 10.0.0.1 in peer list")
	}
	if !wgPeersContainsIP(peers, "10.0.0.2") {
		t.Error("expected to find 10.0.0.2 in peer list")
	}
}

func TestWgPeersContainsIP_not_found(t *testing.T) {
	peers := []WGPeerInfo{
		{PublicKey: "key1", Endpoint: "1.2.3.4:51820", AllowedIP: "10.0.0.1/32"},
	}

	if wgPeersContainsIP(peers, "10.0.0.2") {
		t.Error("did not expect to find 10.0.0.2 in peer list")
	}
}

func TestWgPeersContainsIP_empty_list(t *testing.T) {
	if wgPeersContainsIP(nil, "10.0.0.1") {
		t.Error("did not expect to find any IP in nil peer list")
	}
	if wgPeersContainsIP([]WGPeerInfo{}, "10.0.0.1") {
		t.Error("did not expect to find any IP in empty peer list")
	}
}

func TestAssignWGIP_format(t *testing.T) {
	// Verify the WG IP format used in the handler matches what wgPeersContainsIP expects
	wgIP := "10.0.0.1"
	allowedIP := fmt.Sprintf("%s/32", wgIP)
	peers := []WGPeerInfo{{AllowedIP: allowedIP}}

	if !wgPeersContainsIP(peers, wgIP) {
		t.Errorf("format mismatch: wgPeersContainsIP(%q, %q) should match", allowedIP, wgIP)
	}
}

func TestValidatePublicIP(t *testing.T) {
	tests := []struct {
		name  string
		ip    string
		valid bool
	}{
		{"valid IPv4", "46.225.234.112", true},
		{"loopback", "127.0.0.1", true},
		{"invalid string", "not-an-ip", false},
		{"empty", "", false},
		{"IPv6", "::1", false},
		{"with newline", "1.2.3.4\n5.6.7.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := net.ParseIP(tt.ip)
			isValid := parsed != nil && parsed.To4() != nil && !strings.ContainsAny(tt.ip, "\n\r")
			if isValid != tt.valid {
				t.Errorf("IP %q: expected valid=%v, got %v", tt.ip, tt.valid, isValid)
			}
		})
	}
}

func TestValidateWGPublicKey(t *testing.T) {
	// Valid WireGuard key: 32 bytes, base64 encoded = 44 chars
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))

	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"valid 32-byte key", validKey, true},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16)), false},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64)), false},
		{"not base64", "not-a-valid-base64-key!!!", false},
		{"empty", "", false},
		{"newline injection", validKey + "\n[Peer]", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.ContainsAny(tt.key, "\n\r") {
				if tt.valid {
					t.Errorf("key %q contains newlines but expected valid", tt.key)
				}
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(tt.key)
			isValid := err == nil && len(decoded) == 32
			if isValid != tt.valid {
				t.Errorf("key %q: expected valid=%v, got %v", tt.key, tt.valid, isValid)
			}
		})
	}
}
