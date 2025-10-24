package config

import (
	"time"

	"github.com/multiformats/go-multiaddr"
)

// Config represents the main configuration for a network node
type Config struct {
	Node      NodeConfig      `yaml:"node"`
	Database  DatabaseConfig  `yaml:"database"`
	Discovery DiscoveryConfig `yaml:"discovery"`
	Security  SecurityConfig  `yaml:"security"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// NodeConfig contains node-specific configuration
type NodeConfig struct {
	ID              string   `yaml:"id"`               // Auto-generated if empty
	Type            string   `yaml:"type"`             // "bootstrap" or "node"
	ListenAddresses []string `yaml:"listen_addresses"` // LibP2P listen addresses
	DataDir         string   `yaml:"data_dir"`         // Data directory
	MaxConnections  int      `yaml:"max_connections"`  // Maximum peer connections
}

// DatabaseConfig contains database-related configuration
type DatabaseConfig struct {
	DataDir           string        `yaml:"data_dir"`
	ReplicationFactor int           `yaml:"replication_factor"`
	ShardCount        int           `yaml:"shard_count"`
	MaxDatabaseSize   int64         `yaml:"max_database_size"` // In bytes
	BackupInterval    time.Duration `yaml:"backup_interval"`

	// RQLite-specific configuration
	RQLitePort        int    `yaml:"rqlite_port"`         // RQLite HTTP API port
	RQLiteRaftPort    int    `yaml:"rqlite_raft_port"`    // RQLite Raft consensus port
	RQLiteJoinAddress string `yaml:"rqlite_join_address"` // Address to join RQLite cluster
}

// DiscoveryConfig contains peer discovery configuration
type DiscoveryConfig struct {
	BootstrapPeers    []string      `yaml:"bootstrap_peers"`    // Bootstrap peer addresses
	DiscoveryInterval time.Duration `yaml:"discovery_interval"` // Discovery announcement interval
	BootstrapPort     int           `yaml:"bootstrap_port"`     // Default port for bootstrap nodes
	HttpAdvAddress    string        `yaml:"http_adv_address"`   // HTTP advertisement address
	RaftAdvAddress    string        `yaml:"raft_adv_address"`   // Raft advertisement
	NodeNamespace     string        `yaml:"node_namespace"`     // Namespace for node identifiers
}

// SecurityConfig contains security-related configuration
type SecurityConfig struct {
	EnableTLS       bool   `yaml:"enable_tls"`
	PrivateKeyFile  string `yaml:"private_key_file"`
	CertificateFile string `yaml:"certificate_file"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level      string `yaml:"level"`       // debug, info, warn, error
	Format     string `yaml:"format"`      // json, console
	OutputFile string `yaml:"output_file"` // Empty for stdout
}

// ClientConfig represents configuration for network clients
type ClientConfig struct {
	AppName        string        `yaml:"app_name"`
	DatabaseName   string        `yaml:"database_name"`
	BootstrapPeers []string      `yaml:"bootstrap_peers"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	RetryAttempts  int           `yaml:"retry_attempts"`
}

// ParseMultiaddrs converts string addresses to multiaddr objects
func (c *Config) ParseMultiaddrs() ([]multiaddr.Multiaddr, error) {
	var addrs []multiaddr.Multiaddr
	for _, addr := range c.Node.ListenAddresses {
		ma, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, ma)
	}
	return addrs, nil
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Node: NodeConfig{
			Type: "node",
			ListenAddresses: []string{
				"/ip4/0.0.0.0/tcp/4001", // TCP only - compatible with Anyone proxy/SOCKS5
			},
			DataDir:        "./data",
			MaxConnections: 50,
		},
		Database: DatabaseConfig{
			DataDir:           "./data/db",
			ReplicationFactor: 3,
			ShardCount:        16,
			MaxDatabaseSize:   1024 * 1024 * 1024, // 1GB
			BackupInterval:    time.Hour * 24,     // Daily backups

			// RQLite-specific configuration
			RQLitePort:        5001,
			RQLiteRaftPort:    7001,
			RQLiteJoinAddress: "", // Empty for bootstrap node
		},
		Discovery: DiscoveryConfig{
			BootstrapPeers: []string{
				"/ip4/127.0.0.1/tcp/4001/p2p/12D3KooWHbcFcrGPXKUrHcxvd8MXEeUzRYyvY8fQcpEBxncSUwhj",
				// "/ip4/217.76.54.178/tcp/4001/p2p/12D3KooWKZnirPwNT4URtNSWK45f6vLkEs4xyUZ792F8Uj1oYnm1",
				// "/ip4/51.83.128.181/tcp/4001/p2p/12D3KooWBn2Zf1R8v9pEfmz7hDZ5b3oADxfejA3zJBYzKRCzgvhR",
				// "/ip4/155.133.27.199/tcp/4001/p2p/12D3KooWC69SBzM5QUgrLrfLWUykE8au32X5LwT7zwv9bixrQPm1",
				// "/ip4/217.76.56.2/tcp/4001/p2p/12D3KooWEiqJHvznxqJ5p2y8mUs6Ky6dfU1xTYFQbyKRCABfcZz4",
			},
			BootstrapPort:     4001,             // Default LibP2P port
			DiscoveryInterval: time.Second * 15, // Back to 15 seconds for testing
			HttpAdvAddress:    "",
			RaftAdvAddress:    "",
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
}
