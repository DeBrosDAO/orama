package config

import "time"

// HTTPGatewayConfig contains HTTP reverse proxy gateway configuration
type HTTPGatewayConfig struct {
	Enabled    bool                   `yaml:"enabled"`     // Enable HTTP gateway
	ListenAddr string                 `yaml:"listen_addr"` // Address to listen on (e.g., ":8080")
	NodeName   string                 `yaml:"node_name"`   // Node name for routing
	Routes     map[string]RouteConfig `yaml:"routes"`      // Service routes
	HTTPS      HTTPSConfig            `yaml:"https"`       // HTTPS/TLS configuration
	SNI        SNIConfig              `yaml:"sni"`         // SNI-based TCP routing configuration

	// Full gateway configuration (for API, auth, pubsub)
	ClientNamespace   string        `yaml:"client_namespace"`     // Namespace for network client
	RQLiteDSN         string        `yaml:"rqlite_dsn"`           // RQLite database DSN
	OlricServers      []string      `yaml:"olric_servers"`        // List of Olric server addresses
	OlricTimeout      time.Duration `yaml:"olric_timeout"`        // Timeout for Olric operations
	IPFSClusterAPIURL string        `yaml:"ipfs_cluster_api_url"` // IPFS Cluster API URL
	IPFSAPIURL        string        `yaml:"ipfs_api_url"`         // IPFS API URL
	IPFSTimeout       time.Duration `yaml:"ipfs_timeout"`         // Timeout for IPFS operations
	BaseDomain        string        `yaml:"base_domain"`          // Base domain for deployments (e.g., "dbrs.space", defaults to "orama.network")
}

// HTTPSConfig contains HTTPS/TLS configuration for the gateway
type HTTPSConfig struct {
	Enabled       bool   `yaml:"enabled"`         // Enable HTTPS (port 443)
	Domain        string `yaml:"domain"`          // Primary domain (e.g., node-123.orama.network)
	AutoCert      bool   `yaml:"auto_cert"`       // Use Let's Encrypt for automatic certificate
	UseSelfSigned bool   `yaml:"use_self_signed"` // Use self-signed certificates (pre-generated)
	CertFile      string `yaml:"cert_file"`       // Path to certificate file (if not using auto_cert)
	KeyFile       string `yaml:"key_file"`        // Path to key file (if not using auto_cert)
	CacheDir      string `yaml:"cache_dir"`       // Directory for Let's Encrypt certificate cache
	HTTPPort      int    `yaml:"http_port"`       // HTTP port for ACME challenge (default: 80)
	HTTPSPort     int    `yaml:"https_port"`      // HTTPS port (default: 443)
	Email         string `yaml:"email"`           // Email for Let's Encrypt account
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
