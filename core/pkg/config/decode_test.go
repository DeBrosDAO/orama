package config

import (
	"strings"
	"testing"
)

func TestDecodeStrictValidYAML(t *testing.T) {
	yamlInput := `
node:
  id: "test-node"
  listen_addresses:
    - "/ip4/0.0.0.0/tcp/4001"
  data_dir: "./data"
  max_connections: 100
logging:
  level: "debug"
  format: "json"
`
	var cfg Config
	err := DecodeStrict(strings.NewReader(yamlInput), &cfg)
	if err != nil {
		t.Fatalf("expected no error for valid YAML, got: %v", err)
	}

	if cfg.Node.ID != "test-node" {
		t.Errorf("expected node ID 'test-node', got %q", cfg.Node.ID)
	}
	if len(cfg.Node.ListenAddresses) != 1 || cfg.Node.ListenAddresses[0] != "/ip4/0.0.0.0/tcp/4001" {
		t.Errorf("unexpected listen addresses: %v", cfg.Node.ListenAddresses)
	}
	if cfg.Node.DataDir != "./data" {
		t.Errorf("expected data_dir './data', got %q", cfg.Node.DataDir)
	}
	if cfg.Node.MaxConnections != 100 {
		t.Errorf("expected max_connections 100, got %d", cfg.Node.MaxConnections)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected logging level 'debug', got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("expected logging format 'json', got %q", cfg.Logging.Format)
	}
}

func TestDecodeStrictUnknownFieldsError(t *testing.T) {
	yamlInput := `
node:
  id: "test-node"
  data_dir: "./data"
  unknown_field: "should cause error"
`
	var cfg Config
	err := DecodeStrict(strings.NewReader(yamlInput), &cfg)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config") {
		t.Errorf("expected error to contain 'invalid config', got: %v", err)
	}
}

func TestDecodeStrictTopLevelUnknownField(t *testing.T) {
	yamlInput := `
node:
  id: "test-node"
bogus_section:
  key: "value"
`
	var cfg Config
	err := DecodeStrict(strings.NewReader(yamlInput), &cfg)
	if err == nil {
		t.Fatal("expected error for unknown top-level field, got nil")
	}
}

func TestDecodeStrictEmptyReader(t *testing.T) {
	var cfg Config
	err := DecodeStrict(strings.NewReader(""), &cfg)
	// An empty document produces an EOF error from the YAML decoder
	if err == nil {
		t.Fatal("expected error for empty reader, got nil")
	}
}

func TestDecodeStrictMalformedYAML(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "invalid indentation",
			input: "node:\n  id: \"test\"\n bad_indent: true",
		},
		{
			name:  "tab characters",
			input: "node:\n\tid: \"test\"",
		},
		{
			name:  "unclosed quote",
			input: "node:\n  id: \"unclosed",
		},
		{
			name:  "colon in unquoted value",
			input: "node:\n  id: bad: value: here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := DecodeStrict(strings.NewReader(tt.input), &cfg)
			if err == nil {
				t.Error("expected error for malformed YAML, got nil")
			}
		})
	}
}

func TestDecodeStrictPartialConfig(t *testing.T) {
	// Only set some fields; others should remain at zero values
	yamlInput := `
logging:
  level: "warn"
  format: "console"
`
	var cfg Config
	err := DecodeStrict(strings.NewReader(yamlInput), &cfg)
	if err != nil {
		t.Fatalf("expected no error for partial config, got: %v", err)
	}

	if cfg.Logging.Level != "warn" {
		t.Errorf("expected logging level 'warn', got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "console" {
		t.Errorf("expected logging format 'console', got %q", cfg.Logging.Format)
	}
	// Unset fields should be zero values
	if cfg.Node.ID != "" {
		t.Errorf("expected empty node ID, got %q", cfg.Node.ID)
	}
	if cfg.Node.MaxConnections != 0 {
		t.Errorf("expected zero max_connections, got %d", cfg.Node.MaxConnections)
	}
}

func TestDecodeStrictDatabaseConfig(t *testing.T) {
	yamlInput := `
database:
  data_dir: "./db"
  replication_factor: 5
  shard_count: 32
  max_database_size: 2147483648
  rqlite_port: 6001
  rqlite_raft_port: 8001
  rqlite_join_address: "10.0.0.1:6001"
  min_cluster_size: 3
`
	var cfg Config
	err := DecodeStrict(strings.NewReader(yamlInput), &cfg)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Database.DataDir != "./db" {
		t.Errorf("expected data_dir './db', got %q", cfg.Database.DataDir)
	}
	if cfg.Database.ReplicationFactor != 5 {
		t.Errorf("expected replication_factor 5, got %d", cfg.Database.ReplicationFactor)
	}
	if cfg.Database.ShardCount != 32 {
		t.Errorf("expected shard_count 32, got %d", cfg.Database.ShardCount)
	}
	if cfg.Database.MaxDatabaseSize != 2147483648 {
		t.Errorf("expected max_database_size 2147483648, got %d", cfg.Database.MaxDatabaseSize)
	}
	if cfg.Database.RQLitePort != 6001 {
		t.Errorf("expected rqlite_port 6001, got %d", cfg.Database.RQLitePort)
	}
	if cfg.Database.RQLiteRaftPort != 8001 {
		t.Errorf("expected rqlite_raft_port 8001, got %d", cfg.Database.RQLiteRaftPort)
	}
	if cfg.Database.RQLiteJoinAddress != "10.0.0.1:6001" {
		t.Errorf("expected rqlite_join_address '10.0.0.1:6001', got %q", cfg.Database.RQLiteJoinAddress)
	}
	if cfg.Database.MinClusterSize != 3 {
		t.Errorf("expected min_cluster_size 3, got %d", cfg.Database.MinClusterSize)
	}
}

func TestDecodeStrictNonStructTarget(t *testing.T) {
	// DecodeStrict should also work with simpler types
	yamlInput := `
key1: value1
key2: value2
`
	var result map[string]string
	err := DecodeStrict(strings.NewReader(yamlInput), &result)
	if err != nil {
		t.Fatalf("expected no error decoding to map, got: %v", err)
	}
	if result["key1"] != "value1" {
		t.Errorf("expected key1='value1', got %q", result["key1"])
	}
	if result["key2"] != "value2" {
		t.Errorf("expected key2='value2', got %q", result["key2"])
	}
}

// TestDecodeStrict_secretsEncryptionKey is the regression guard for the
// v0.122.42 boot crash: Phase 4 config generation writes
// `secrets_encryption_key` into node.yaml under the http_gateway section,
// but HTTPGatewayConfig had no matching field. With KnownFields(true)
// strict decoding, the unknown field made DecodeStrict fail and
// orama-node crash-looped (exit 1) on every start. The field must parse.
func TestDecodeStrict_secretsEncryptionKey(t *testing.T) {
	yamlInput := `
node:
  id: "test-node"
  data_dir: "./data"
http_gateway:
  enabled: true
  client_namespace: "default"
  rqlite_dsn: "http://localhost:5001"
  secrets_encryption_key: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
`
	var cfg Config
	if err := DecodeStrict(strings.NewReader(yamlInput), &cfg); err != nil {
		t.Fatalf("node.yaml with secrets_encryption_key must parse (v0.122.42 regression), got: %v", err)
	}
	want := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if cfg.HTTPGateway.SecretsEncryptionKey != want {
		t.Errorf("SecretsEncryptionKey = %q, want %q", cfg.HTTPGateway.SecretsEncryptionKey, want)
	}
}
