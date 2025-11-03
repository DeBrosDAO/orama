package config

import (
	"testing"
	"time"
)

// validConfigForType returns a valid config for the given node type
func validConfigForType(nodeType string) *Config {
	validPeer := "/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj"
	cfg := &Config{
		Node: NodeConfig{
			Type:            nodeType,
			ID:              "test-node-id",
			ListenAddresses: []string{"/ip4/0.0.0.0/tcp/4001"},
			DataDir:         ".",
			MaxConnections:  50,
		},
		Database: DatabaseConfig{
			DataDir:           ".",
			ReplicationFactor: 3,
			ShardCount:        16,
			MaxDatabaseSize:   1024,
			BackupInterval:    1 * time.Hour,
			RQLitePort:        5001,
			RQLiteRaftPort:    7001,
			MinClusterSize:    1,
		},
		Discovery: DiscoveryConfig{
			BootstrapPeers:    []string{validPeer},
			DiscoveryInterval: 15 * time.Second,
			BootstrapPort:     4001,
			HttpAdvAddress:    "127.0.0.1:5001",
			RaftAdvAddress:    "127.0.0.1:7001",
			NodeNamespace:     "default",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "console",
		},
	}

	// Set rqlite_join_address based on node type
	if nodeType == "node" {
		cfg.Database.RQLiteJoinAddress = "localhost:5001"
		// Node type requires bootstrap peers
		cfg.Discovery.BootstrapPeers = []string{validPeer}
	} else {
		// Bootstrap type: empty join address and peers optional
		cfg.Database.RQLiteJoinAddress = ""
		cfg.Discovery.BootstrapPeers = []string{}
	}

	return cfg
}

func TestValidateNodeType(t *testing.T) {
	tests := []struct {
		name        string
		nodeType    string
		shouldError bool
	}{
		{"bootstrap", "bootstrap", false},
		{"node", "node", false},
		{"invalid", "invalid-type", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("bootstrap") // Start with valid bootstrap
			if tt.nodeType == "node" {
				cfg = validConfigForType("node")
			} else {
				cfg.Node.Type = tt.nodeType
			}
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateListenAddresses(t *testing.T) {
	tests := []struct {
		name        string
		addresses   []string
		shouldError bool
	}{
		{"valid single", []string{"/ip4/0.0.0.0/tcp/4001"}, false},
		{"valid ipv6", []string{"/ip6/::/tcp/4001"}, false},
		{"invalid port", []string{"/ip4/0.0.0.0/tcp/99999"}, true},
		{"invalid port zero", []string{"/ip4/0.0.0.0/tcp/0"}, true},
		{"invalid multiaddr", []string{"invalid"}, true},
		{"empty", []string{}, true},
		{"duplicate", []string{"/ip4/0.0.0.0/tcp/4001", "/ip4/0.0.0.0/tcp/4001"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Node.ListenAddresses = tt.addresses
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateReplicationFactor(t *testing.T) {
	tests := []struct {
		name        string
		replication int
		shouldError bool
	}{
		{"valid 1", 1, false},
		{"valid 3", 3, false},
		{"valid even", 2, false}, // warn but not error
		{"invalid zero", 0, true},
		{"invalid negative", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Database.ReplicationFactor = tt.replication
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateRQLitePorts(t *testing.T) {
	tests := []struct {
		name        string
		httpPort    int
		raftPort    int
		shouldError bool
	}{
		{"valid different", 5001, 7001, false},
		{"invalid same", 5001, 5001, true},
		{"invalid http port zero", 0, 7001, true},
		{"invalid raft port zero", 5001, 0, true},
		{"invalid http port too high", 99999, 7001, true},
		{"invalid raft port too high", 5001, 99999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Database.RQLitePort = tt.httpPort
			cfg.Database.RQLiteRaftPort = tt.raftPort
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateRQLiteJoinAddress(t *testing.T) {
	tests := []struct {
		name        string
		nodeType    string
		joinAddr    string
		shouldError bool
	}{
		{"node with join", "node", "localhost:5001", false},
		{"node without join", "node", "", true},
		{"bootstrap with join", "bootstrap", "localhost:5001", true},
		{"bootstrap without join", "bootstrap", "", false},
		{"invalid join format", "node", "localhost", true},
		{"invalid join port", "node", "localhost:99999", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType(tt.nodeType)
			cfg.Database.RQLiteJoinAddress = tt.joinAddr
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateBootstrapPeers(t *testing.T) {
	validPeer := "/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj"
	tests := []struct {
		name        string
		nodeType    string
		peers       []string
		shouldError bool
	}{
		{"node with peer", "node", []string{validPeer}, false},
		{"node without peer", "node", []string{}, true},
		{"bootstrap with peer", "bootstrap", []string{validPeer}, false},
		{"bootstrap without peer", "bootstrap", []string{}, false},
		{"invalid multiaddr", "node", []string{"invalid"}, true},
		{"missing p2p", "node", []string{"/ip4/127.0.0.1/tcp/4001"}, true},
		{"duplicate peer", "node", []string{validPeer, validPeer}, true},
		{"invalid port", "node", []string{"/ip4/127.0.0.1/tcp/99999/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType(tt.nodeType)
			cfg.Discovery.BootstrapPeers = tt.peers
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateLoggingLevel(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		shouldError bool
	}{
		{"debug", "debug", false},
		{"info", "info", false},
		{"warn", "warn", false},
		{"error", "error", false},
		{"invalid", "verbose", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Logging.Level = tt.level
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateLoggingFormat(t *testing.T) {
	tests := []struct {
		name        string
		format      string
		shouldError bool
	}{
		{"json", "json", false},
		{"console", "console", false},
		{"invalid", "text", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Logging.Format = tt.format
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateMaxConnections(t *testing.T) {
	tests := []struct {
		name        string
		maxConn     int
		shouldError bool
	}{
		{"valid 50", 50, false},
		{"valid 1", 1, false},
		{"invalid zero", 0, true},
		{"invalid negative", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Node.MaxConnections = tt.maxConn
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateDiscoveryInterval(t *testing.T) {
	tests := []struct {
		name        string
		interval    time.Duration
		shouldError bool
	}{
		{"valid 15s", 15 * time.Second, false},
		{"valid 1s", 1 * time.Second, false},
		{"invalid zero", 0, true},
		{"invalid negative", -5 * time.Second, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Discovery.DiscoveryInterval = tt.interval
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateBootstrapPort(t *testing.T) {
	tests := []struct {
		name        string
		port        int
		shouldError bool
	}{
		{"valid 4001", 4001, false},
		{"valid 4002", 4002, false},
		{"invalid zero", 0, true},
		{"invalid too high", 99999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForType("node")
			cfg.Discovery.BootstrapPort = tt.port
			errs := cfg.Validate()
			if tt.shouldError && len(errs) == 0 {
				t.Errorf("expected error, got none")
			}
			if !tt.shouldError && len(errs) > 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestValidateCompleteConfig(t *testing.T) {
	// Test a complete valid config
	validCfg := &Config{
		Node: NodeConfig{
			Type:            "node",
			ID:              "node1",
			ListenAddresses: []string{"/ip4/0.0.0.0/tcp/4002"},
			DataDir:         ".",
			MaxConnections:  50,
		},
		Database: DatabaseConfig{
			DataDir:           ".",
			ReplicationFactor: 3,
			ShardCount:        16,
			MaxDatabaseSize:   1073741824,
			BackupInterval:    24 * time.Hour,
			RQLitePort:        5002,
			RQLiteRaftPort:    7002,
			RQLiteJoinAddress: "127.0.0.1:7001",
			MinClusterSize:    1,
		},
		Discovery: DiscoveryConfig{
			BootstrapPeers: []string{
				"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj",
			},
			DiscoveryInterval: 15 * time.Second,
			BootstrapPort:     4001,
			HttpAdvAddress:    "127.0.0.1:5001",
			RaftAdvAddress:    "127.0.0.1:7001",
			NodeNamespace:     "default",
		},
		Security: SecurityConfig{
			EnableTLS: false,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "console",
		},
	}

	errs := validCfg.Validate()
	if len(errs) > 0 {
		t.Errorf("valid config should not have errors: %v", errs)
	}
}
