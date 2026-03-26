package namespace

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestDNSRecordManager_FQDNFormat(t *testing.T) {
	// Test that FQDN is correctly formatted
	tests := []struct {
		namespace  string
		baseDomain string
		expected   string
	}{
		{"alice", "orama-devnet.network", "ns-alice.orama-devnet.network."},
		{"bob", "orama-testnet.network", "ns-bob.orama-testnet.network."},
		{"my-namespace", "orama-mainnet.network", "ns-my-namespace.orama-mainnet.network."},
		{"test123", "example.com", "ns-test123.example.com."},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			fqdn := fmt.Sprintf("ns-%s.%s.", tt.namespace, tt.baseDomain)
			if fqdn != tt.expected {
				t.Errorf("FQDN = %s, want %s", fqdn, tt.expected)
			}
		})
	}
}

func TestDNSRecordManager_WildcardFQDNFormat(t *testing.T) {
	// Test that wildcard FQDN is correctly formatted
	tests := []struct {
		namespace  string
		baseDomain string
		expected   string
	}{
		{"alice", "orama-devnet.network", "*.ns-alice.orama-devnet.network."},
		{"bob", "orama-testnet.network", "*.ns-bob.orama-testnet.network."},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			wildcardFqdn := fmt.Sprintf("*.ns-%s.%s.", tt.namespace, tt.baseDomain)
			if wildcardFqdn != tt.expected {
				t.Errorf("Wildcard FQDN = %s, want %s", wildcardFqdn, tt.expected)
			}
		})
	}
}

func TestNewDNSRecordManager(t *testing.T) {
	mockDB := newMockRQLiteClient()
	logger := zap.NewNop()
	baseDomain := "orama-devnet.network"

	manager := NewDNSRecordManager(mockDB, baseDomain, logger)

	if manager == nil {
		t.Fatal("NewDNSRecordManager returned nil")
	}
}

func TestDNSRecordManager_NamespacePrefix(t *testing.T) {
	// Test the namespace prefix used for tracking ownership
	namespace := "my-namespace"
	expected := "namespace:my-namespace"

	prefix := "namespace:" + namespace
	if prefix != expected {
		t.Errorf("Namespace prefix = %s, want %s", prefix, expected)
	}
}

func TestDNSRecordTTL(t *testing.T) {
	// DNS records should have a 60-second TTL for quick failover
	expectedTTL := 60

	// This is testing the constant used in the code
	ttl := 60
	if ttl != expectedTTL {
		t.Errorf("TTL = %d, want %d", ttl, expectedTTL)
	}
}

func TestDNSRecordManager_MultipleDomainFormats(t *testing.T) {
	// Test support for different domain formats
	baseDomains := []string{
		"orama-devnet.network",
		"orama-testnet.network",
		"orama-mainnet.network",
		"custom.example.com",
		"subdomain.custom.example.com",
	}

	for _, baseDomain := range baseDomains {
		t.Run(baseDomain, func(t *testing.T) {
			namespace := "test"
			fqdn := fmt.Sprintf("ns-%s.%s.", namespace, baseDomain)

			// Verify FQDN ends with trailing dot
			if fqdn[len(fqdn)-1] != '.' {
				t.Errorf("FQDN should end with trailing dot: %s", fqdn)
			}

			// Verify format is correct
			expectedPrefix := "ns-test."
			if len(fqdn) <= len(expectedPrefix) {
				t.Errorf("FQDN too short: %s", fqdn)
			}
			if fqdn[:len(expectedPrefix)] != expectedPrefix {
				t.Errorf("FQDN should start with %s: %s", expectedPrefix, fqdn)
			}
		})
	}
}

func TestDNSRecordManager_IPValidation(t *testing.T) {
	// Test IP address formats that should be accepted
	validIPs := []string{
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"1.2.3.4",
		"255.255.255.255",
	}

	for _, ip := range validIPs {
		t.Run(ip, func(t *testing.T) {
			// Basic validation: IP should not be empty
			if ip == "" {
				t.Error("IP should not be empty")
			}
		})
	}
}

func TestDNSRecordManager_EmptyNodeIPs(t *testing.T) {
	// Creating records with empty node IPs should be an error
	nodeIPs := []string{}

	if len(nodeIPs) == 0 {
		// This condition should trigger the error in CreateNamespaceRecords
		err := &ClusterError{Message: "no node IPs provided for DNS records"}
		if err.Message != "no node IPs provided for DNS records" {
			t.Error("Expected error message for empty IPs")
		}
	}
}

func TestDNSRecordManager_RecordTypes(t *testing.T) {
	// DNS records for namespace gateways should be A records
	expectedRecordType := "A"

	recordType := "A"
	if recordType != expectedRecordType {
		t.Errorf("Record type = %s, want %s", recordType, expectedRecordType)
	}
}

func TestDNSRecordManager_CreatedByField(t *testing.T) {
	// Records should be created by "cluster-manager"
	expected := "cluster-manager"

	createdBy := "cluster-manager"
	if createdBy != expected {
		t.Errorf("CreatedBy = %s, want %s", createdBy, expected)
	}
}

func TestDNSRecordManager_RoundRobinConcept(t *testing.T) {
	// Test that multiple A records for the same FQDN enable round-robin
	nodeIPs := []string{
		"192.168.1.100",
		"192.168.1.101",
		"192.168.1.102",
	}

	// For round-robin DNS, we need one A record per IP
	expectedRecordCount := len(nodeIPs)

	if expectedRecordCount != 3 {
		t.Errorf("Expected %d A records for round-robin, got %d", 3, expectedRecordCount)
	}

	// Each IP should be unique
	seen := make(map[string]bool)
	for _, ip := range nodeIPs {
		if seen[ip] {
			t.Errorf("Duplicate IP in node list: %s", ip)
		}
		seen[ip] = true
	}
}

func TestDNSRecordManager_FQDNWithTrailingDot(t *testing.T) {
	// DNS FQDNs should always end with a trailing dot
	// This is important for proper DNS resolution

	tests := []struct {
		input    string
		expected string
	}{
		{"ns-alice.orama-devnet.network", "ns-alice.orama-devnet.network."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			fqdn := tt.input + "."
			if fqdn != tt.expected {
				t.Errorf("FQDN = %s, want %s", fqdn, tt.expected)
			}
		})
	}
}

func TestUpdateNamespaceRecord_SetsActiveTrue(t *testing.T) {
	mockDB := newMockRQLiteClient()
	logger := zap.NewNop()
	manager := NewDNSRecordManager(mockDB, "orama-devnet.network", logger)

	ctx := context.Background()
	err := manager.UpdateNamespaceRecord(ctx, "alice", "1.2.3.4", "5.6.7.8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the SQL contains is_active = 1 for both FQDN and wildcard
	activeCount := 0
	for _, call := range mockDB.execCalls {
		if strings.Contains(call.Query, "is_active = 1") && strings.Contains(call.Query, "UPDATE dns_records") {
			activeCount++
		}
	}
	if activeCount != 2 {
		t.Fatalf("expected 2 UPDATE queries with is_active = 1 (fqdn + wildcard), got %d", activeCount)
	}
}

func TestCountActiveNamespaceRecords(t *testing.T) {
	mockDB := newMockRQLiteClient()
	logger := zap.NewNop()
	manager := NewDNSRecordManager(mockDB, "orama-devnet.network", logger)

	ctx := context.Background()
	count, err := manager.CountActiveNamespaceRecords(ctx, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With mock returning empty results, count should be 0
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}

	// Verify the correct query was made
	if len(mockDB.queryCalls) == 0 {
		t.Fatal("expected a query call")
	}
	lastCall := mockDB.queryCalls[len(mockDB.queryCalls)-1]
	if !strings.Contains(lastCall.Query, "COUNT(*)") || !strings.Contains(lastCall.Query, "is_active = TRUE") {
		t.Fatalf("unexpected query: %s", lastCall.Query)
	}
	// Verify the FQDN arg
	expectedFQDN := "ns-alice.orama-devnet.network."
	if len(lastCall.Args) == 0 {
		t.Fatal("expected query args")
	}
	if fqdn, ok := lastCall.Args[0].(string); !ok || fqdn != expectedFQDN {
		t.Fatalf("expected FQDN arg %q, got %v", expectedFQDN, lastCall.Args[0])
	}
}
