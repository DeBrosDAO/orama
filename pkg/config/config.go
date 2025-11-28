package config

import (
	"time"

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

// NodeConfig contains node-specific configuration
type NodeConfig struct {
	ID              string   `yaml:"id"`               // Auto-generated if empty
	ListenAddresses []string `yaml:"listen_addresses"` // LibP2P listen addresses
	DataDir         string   `yaml:"data_dir"`         // Data directory
	MaxConnections  int      `yaml:"max_connections"`  // Maximum peer connections
	Domain          string   `yaml:"domain"`           // Domain for this node (e.g., node-1.orama.network)
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

	// RQLite node-to-node TLS encryption (for inter-node Raft communication)
	// See: https://rqlite.io/docs/guides/security/#encrypting-node-to-node-communication
	NodeCert     string `yaml:"node_cert"`      // Path to X.509 certificate for node-to-node communication
	NodeKey      string `yaml:"node_key"`       // Path to X.509 private key for node-to-node communication
	NodeCACert   string `yaml:"node_ca_cert"`   // Path to CA certificate (optional, uses system CA if not set)
	NodeNoVerify bool   `yaml:"node_no_verify"` // Skip certificate verification (for testing/self-signed certs)

	// Dynamic discovery configuration (always enabled)
	ClusterSyncInterval time.Duration `yaml:"cluster_sync_interval"` // default: 30s
	PeerInactivityLimit time.Duration `yaml:"peer_inactivity_limit"` // default: 24h
	MinClusterSize      int           `yaml:"min_cluster_size"`      // default: 1

	// Olric cache configuration
	OlricHTTPPort       int `yaml:"olric_http_port"`       // Olric HTTP API port (default: 3320)
	OlricMemberlistPort int `yaml:"olric_memberlist_port"` // Olric memberlist port (default: 3322)

	// IPFS storage configuration
	IPFS IPFSConfig `yaml:"ipfs"`
}

// IPFSConfig contains IPFS storage configuration
type IPFSConfig struct {
	// ClusterAPIURL is the IPFS Cluster HTTP API URL (e.g., "http://localhost:9094")
	// If empty, IPFS storage is disabled for this node
	ClusterAPIURL string `yaml:"cluster_api_url"`

	// APIURL is the IPFS HTTP API URL for content retrieval (e.g., "http://localhost:5001")
	// If empty, defaults to "http://localhost:5001"
	APIURL string `yaml:"api_url"`

	// Timeout for IPFS operations
	// If zero, defaults to 60 seconds
	Timeout time.Duration `yaml:"timeout"`

	// ReplicationFactor is the replication factor for pinned content
	// If zero, defaults to 3
	ReplicationFactor int `yaml:"replication_factor"`

	// EnableEncryption enables client-side encryption before upload
	// Defaults to true
	EnableEncryption bool `yaml:"enable_encryption"`
}

// DiscoveryConfig contains peer discovery configuration
type DiscoveryConfig struct {
	BootstrapPeers    []string      `yaml:"bootstrap_peers"`    // Peer addresses to connect to
	DiscoveryInterval time.Duration `yaml:"discovery_interval"` // Discovery announcement interval
	BootstrapPort     int           `yaml:"bootstrap_port"`     // Default port for peer discovery
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

// HTTPGatewayConfig contains HTTP reverse proxy gateway configuration
type HTTPGatewayConfig struct {
	Enabled    bool                   `yaml:"enabled"`     // Enable HTTP gateway
	ListenAddr string                 `yaml:"listen_addr"` // Address to listen on (e.g., ":8080")
	NodeName   string                 `yaml:"node_name"`   // Node name for routing
	Routes     map[string]RouteConfig `yaml:"routes"`      // Service routes
	HTTPS      HTTPSConfig            `yaml:"https"`       // HTTPS/TLS configuration
	SNI        SNIConfig              `yaml:"sni"`         // SNI-based TCP routing configuration

	// Full gateway configuration (for API, auth, pubsub)
	ClientNamespace   string        `yaml:"client_namespace"`    // Namespace for network client
	RQLiteDSN         string        `yaml:"rqlite_dsn"`          // RQLite database DSN
	OlricServers      []string      `yaml:"olric_servers"`       // List of Olric server addresses
	OlricTimeout      time.Duration `yaml:"olric_timeout"`       // Timeout for Olric operations
	IPFSClusterAPIURL string        `yaml:"ipfs_cluster_api_url"` // IPFS Cluster API URL
	IPFSAPIURL        string        `yaml:"ipfs_api_url"`        // IPFS API URL
	IPFSTimeout       time.Duration `yaml:"ipfs_timeout"`        // Timeout for IPFS operations
}

// HTTPSConfig contains HTTPS/TLS configuration for the gateway
type HTTPSConfig struct {
	Enabled        bool   `yaml:"enabled"`         // Enable HTTPS (port 443)
	Domain         string `yaml:"domain"`          // Primary domain (e.g., node-123.orama.network)
	AutoCert       bool   `yaml:"auto_cert"`       // Use Let's Encrypt for automatic certificate
	UseSelfSigned  bool   `yaml:"use_self_signed"` // Use self-signed certificates (pre-generated)
	CertFile       string `yaml:"cert_file"`       // Path to certificate file (if not using auto_cert)
	KeyFile        string `yaml:"key_file"`        // Path to key file (if not using auto_cert)
	CacheDir       string `yaml:"cache_dir"`       // Directory for Let's Encrypt certificate cache
	HTTPPort       int    `yaml:"http_port"`       // HTTP port for ACME challenge (default: 80)
	HTTPSPort      int    `yaml:"https_port"`      // HTTPS port (default: 443)
	Email          string `yaml:"email"`           // Email for Let's Encrypt account
}

// SNIConfig contains SNI-based TCP routing configuration for port 7001
type SNIConfig struct {
	Enabled    bool              `yaml:"enabled"`     // Enable SNI-based TCP routing
	ListenAddr string            `yaml:"listen_addr"` // Address to listen on (e.g., ":7001")
	Routes     map[string]string `yaml:"routes"`      // SNI hostname -> backend address mapping
	CertFile   string            `yaml:"cert_file"`   // Path to certificate file
	KeyFile    string            `yaml:"key_file"`    // Path to key file
}

// RouteConfig defines a single reverse proxy route
type RouteConfig struct {
	PathPrefix string        `yaml:"path_prefix"` // URL path prefix (e.g., "/rqlite/http")
	BackendURL string        `yaml:"backend_url"` // Backend service URL
	Timeout    time.Duration `yaml:"timeout"`     // Request timeout
	WebSocket  bool          `yaml:"websocket"`   // Support WebSocket upgrades
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
