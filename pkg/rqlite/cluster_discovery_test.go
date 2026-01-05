package rqlite

import (
	"testing"
	"github.com/DeBrosOfficial/network/pkg/discovery"
)

func TestShouldReplaceHost(t *testing.T) {
	tests := []struct {
		host     string
		expected bool
	}{
		{"", true},
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"1.1.1.1", false},
		{"8.8.8.8", false},
		{"example.com", false},
	}

	for _, tt := range tests {
		if got := shouldReplaceHost(tt.host); got != tt.expected {
			t.Errorf("shouldReplaceHost(%s) = %v; want %v", tt.host, got, tt.expected)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"127.0.0.1", false},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"1.1.1.1", true},
		{"8.8.8.8", true},
		{"2001:4860:4860::8888", true},
	}

	for _, tt := range tests {
		if got := isPublicIP(tt.ip); got != tt.expected {
			t.Errorf("isPublicIP(%s) = %v; want %v", tt.ip, got, tt.expected)
		}
	}
}

func TestReplaceAddressHost(t *testing.T) {
	tests := []struct {
		address  string
		newHost  string
		expected string
		replaced bool
	}{
		{"localhost:4001", "1.1.1.1", "1.1.1.1:4001", true},
		{"127.0.0.1:4001", "1.1.1.1", "1.1.1.1:4001", true},
		{"8.8.8.8:4001", "1.1.1.1", "8.8.8.8:4001", false}, // Don't replace public IP
		{"invalid", "1.1.1.1", "invalid", false},
	}

	for _, tt := range tests {
		got, replaced := replaceAddressHost(tt.address, tt.newHost)
		if got != tt.expected || replaced != tt.replaced {
			t.Errorf("replaceAddressHost(%s, %s) = %s, %v; want %s, %v", tt.address, tt.newHost, got, replaced, tt.expected, tt.replaced)
		}
	}
}

func TestRewriteAdvertisedAddresses(t *testing.T) {
	meta := &discovery.RQLiteNodeMetadata{
		NodeID:      "localhost:4001",
		RaftAddress: "localhost:4001",
		HTTPAddress: "localhost:4002",
	}

	changed, originalNodeID := rewriteAdvertisedAddresses(meta, "1.1.1.1", true)

	if !changed {
		t.Error("expected changed to be true")
	}
	if originalNodeID != "localhost:4001" {
		t.Errorf("expected originalNodeID localhost:4001, got %s", originalNodeID)
	}
	if meta.RaftAddress != "1.1.1.1:4001" {
		t.Errorf("expected RaftAddress 1.1.1.1:4001, got %s", meta.RaftAddress)
	}
	if meta.HTTPAddress != "1.1.1.1:4002" {
		t.Errorf("expected HTTPAddress 1.1.1.1:4002, got %s", meta.HTTPAddress)
	}
	if meta.NodeID != "1.1.1.1:4001" {
		t.Errorf("expected NodeID 1.1.1.1:4001, got %s", meta.NodeID)
	}
}

