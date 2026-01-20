package config

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/config/validate"
	"github.com/multiformats/go-multiaddr"
)

// Config represents the main configuration for a network node
type Config struct {
	Node        NodeConfig        `yaml:"node"`
	Database    DatabaseConfig    `yaml:"database"`
	Discovery   DiscoveryConfig   `yaml:"discovery"`
	Security    SecurityConfig    `yaml:"security"`
	Logging     LoggingConfig     `yaml:"logging"`
	HTTPGateway HTTPGatewayConfig `yaml:"http_gateway"`
}

// ValidationError represents a single validation error with context.
// This is exported from the validate subpackage for backward compatibility.
type ValidationError = validate.ValidationError

// ValidateSwarmKey validates that a swarm key is 64 hex characters.
// This is exported from the validate subpackage for backward compatibility.
func ValidateSwarmKey(key string) error {
	return validate.ValidateSwarmKey(key)
}

// Validate performs comprehensive validation of the entire config.
// It aggregates all errors and returns them, allowing the caller to print all issues at once.
func (c *Config) Validate() []error {
	var errs []error

	// Validate node config
	errs = append(errs, validate.ValidateNode(validate.NodeConfig{
		ID:              c.Node.ID,
		ListenAddresses: c.Node.ListenAddresses,
		DataDir:         c.Node.DataDir,
		MaxConnections:  c.Node.MaxConnections,
	})...)

	// Validate database config
	errs = append(errs, validate.ValidateDatabase(validate.DatabaseConfig{
		DataDir:             c.Database.DataDir,
		ReplicationFactor:   c.Database.ReplicationFactor,
		ShardCount:          c.Database.ShardCount,
		MaxDatabaseSize:     c.Database.MaxDatabaseSize,
		RQLitePort:          c.Database.RQLitePort,
		RQLiteRaftPort:      c.Database.RQLiteRaftPort,
		RQLiteJoinAddress:   c.Database.RQLiteJoinAddress,
		ClusterSyncInterval: c.Database.ClusterSyncInterval,
		PeerInactivityLimit: c.Database.PeerInactivityLimit,
		MinClusterSize:      c.Database.MinClusterSize,
	})...)

	// Validate discovery config
	errs = append(errs, validate.ValidateDiscovery(validate.DiscoveryConfig{
		BootstrapPeers:    c.Discovery.BootstrapPeers,
		DiscoveryInterval: c.Discovery.DiscoveryInterval,
		BootstrapPort:     c.Discovery.BootstrapPort,
		HttpAdvAddress:    c.Discovery.HttpAdvAddress,
		RaftAdvAddress:    c.Discovery.RaftAdvAddress,
	})...)

	// Validate security config
	errs = append(errs, validate.ValidateSecurity(validate.SecurityConfig{
		EnableTLS:       c.Security.EnableTLS,
		PrivateKeyFile:  c.Security.PrivateKeyFile,
		CertificateFile: c.Security.CertificateFile,
	})...)

	// Validate logging config
	errs = append(errs, validate.ValidateLogging(validate.LoggingConfig{
		Level:      c.Logging.Level,
		Format:     c.Logging.Format,
		OutputFile: c.Logging.OutputFile,
	})...)

	return errs
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
			RQLiteJoinAddress: "", // Empty for first node (creates cluster)

			// Dynamic discovery (always enabled)
			ClusterSyncInterval: 30 * time.Second,
			PeerInactivityLimit: 24 * time.Hour,
			MinClusterSize:      1,

			// Olric cache configuration
			OlricHTTPPort:       3320,
			OlricMemberlistPort: 3322,

			// IPFS storage configuration
			IPFS: IPFSConfig{
				ClusterAPIURL:     "", // Empty = disabled
				APIURL:            "http://localhost:5001",
				Timeout:           60 * time.Second,
				ReplicationFactor: 3,
				EnableEncryption:  true,
			},
		},
		Discovery: DiscoveryConfig{
			BootstrapPeers:    []string{},
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
		HTTPGateway: HTTPGatewayConfig{
			Enabled:           true,
			ListenAddr:        ":8080",
			NodeName:          "default",
			Routes:            make(map[string]RouteConfig),
			ClientNamespace:   "default",
			RQLiteDSN:         "http://localhost:5001",
			OlricServers:      []string{"localhost:3320"},
			OlricTimeout:      10 * time.Second,
			IPFSClusterAPIURL: "http://localhost:9094",
			IPFSAPIURL:        "http://localhost:5001",
			IPFSTimeout:       60 * time.Second,
		},
	}
}
