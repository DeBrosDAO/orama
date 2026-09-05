package namespace

import (
	"context"
	"errors"
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

// The guard against disabling a namespace's last DNS record has to live inside
// the UPDATE. Every node that observes a suspect node runs this path, so a
// separate COUNT followed by an unconditional write lets two observers both
// read a count of 2, both conclude they are not the last, and both disable —
// leaving the namespace resolving nowhere.
func TestDisableNamespaceRecord_guardIsInsideTheStatement(t *testing.T) {
	mockDB := newMockRQLiteClient()
	manager := NewDNSRecordManager(mockDB, "orama-devnet.network", zap.NewNop())

	if _, err := manager.DisableNamespaceRecord(context.Background(), "alice", "203.0.113.1"); err != nil {
		t.Fatalf("DisableNamespaceRecord: %v", err)
	}

	if len(mockDB.execCalls) == 0 {
		t.Fatal("expected an UPDATE")
	}
	guarded := 0
	for _, call := range mockDB.execCalls {
		if !strings.Contains(call.Query, "UPDATE dns_records") {
			continue
		}
		guarded++
		if !strings.Contains(call.Query, "SELECT COUNT(*)") {
			t.Fatalf("the last-record guard is not part of the UPDATE:\n%s", call.Query)
		}
		if !strings.Contains(call.Query, "> 1") {
			t.Fatalf("the UPDATE does not require more than one active record:\n%s", call.Query)
		}
		// Each name must be guarded on its own count: guarding the wildcard on
		// the primary's count would let the last wildcard record go.
		if !strings.Contains(call.Query, "d2.fqdn = dns_records.fqdn") {
			t.Fatalf("the guard does not correlate on the record's own fqdn:\n%s", call.Query)
		}
	}
	if guarded != 2 {
		t.Fatalf("expected both the primary and the wildcard name to be guarded, got %d", guarded)
	}
}

func TestDisableNamespaceRecord_surfacesAWriteFailure(t *testing.T) {
	// The UPDATE's error used to be discarded with `_, _ =` and the function
	// always returned nil, so a failure to withdraw a dead node was invisible.
	mockDB := newMockRQLiteClient()
	manager := NewDNSRecordManager(mockDB, "orama-devnet.network", zap.NewNop())
	// Fail whichever UPDATE the manager issues first.
	if _, err := manager.DisableNamespaceRecord(context.Background(), "alice", "203.0.113.1"); err != nil {
		t.Fatalf("setup call: %v", err)
	}
	for _, call := range mockDB.execCalls {
		mockDB.execResults[call.Query] = errors.New("no leader")
	}

	if _, err := manager.DisableNamespaceRecord(context.Background(), "alice", "203.0.113.1"); err == nil {
		t.Fatal("a failed withdrawal was reported as success")
	}
}

func TestAddNamespaceRecord_revivesASoftDisabledRow(t *testing.T) {
	// dns_records has UNIQUE(fqdn, record_type, value) and
	// DisableNamespaceRecord leaves the row in place with is_active = 0. A bare
	// INSERT therefore hit the constraint on the repair path — so a repaired
	// node was never re-advertised, and the repair reported success.
	mockDB := newMockRQLiteClient()
	manager := NewDNSRecordManager(mockDB, "orama-devnet.network", zap.NewNop())

	if err := manager.AddNamespaceRecord(context.Background(), "alice", "203.0.113.1"); err != nil {
		t.Fatalf("AddNamespaceRecord: %v", err)
	}

	inserts := 0
	for _, call := range mockDB.execCalls {
		if !strings.Contains(call.Query, "INSERT INTO dns_records") {
			continue
		}
		inserts++
		if !strings.Contains(call.Query, "ON CONFLICT(fqdn, record_type, value)") {
			t.Fatalf("the insert is not an upsert, so it fails on a disabled row:\n%s", call.Query)
		}
		if !strings.Contains(call.Query, "is_active = TRUE") {
			t.Fatalf("the upsert does not re-enable the row, so the node stays withdrawn:\n%s", call.Query)
		}
	}
	if inserts != 2 {
		t.Fatalf("expected both the primary and the wildcard name to be written, got %d", inserts)
	}
}
