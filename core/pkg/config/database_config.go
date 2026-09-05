package config

import "time"

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

	// RQLite HTTP Basic Auth credentials, used by every client this node opens:
	// the SQL DSN and the admin API (AdminClient).
	//
	// Setting these is always safe: rqlite ignores credentials it does not
	// require, so a node can send them long before any node enforces them.
	RQLiteUsername string `yaml:"rqlite_username"`
	RQLitePassword string `yaml:"rqlite_password"`

	// RQLiteAuthFile is the rqlite auth JSON. It supplies the credentials the
	// admin client sends, and is the file rqlited is pointed at when
	// RQLiteEnforceAuth is set.
	RQLiteAuthFile string `yaml:"rqlite_auth_file"`

	// RQLiteEnforceAuth starts rqlited with `-auth`, making it reject
	// unauthenticated requests.
	//
	// Separate from RQLiteAuthFile on purpose. The two used to be one setting,
	// so the only way to give clients credentials was to simultaneously start
	// refusing everyone who had none — including every peer still running the
	// previous release, whose /join, /status and /remove calls would 401 in the
	// middle of a rolling upgrade and look exactly like raft breaking.
	//
	// The rollout is therefore two passes: first every node ships credentials
	// (RQLiteAuthFile, enforcement off), then enforcement is switched on.
	RQLiteEnforceAuth bool `yaml:"rqlite_enforce_auth"`

	// Raft tuning (passed through to rqlited CLI flags).
	// Higher defaults than rqlited's 1s suit WireGuard latency.
	RaftElectionTimeout    time.Duration `yaml:"raft_election_timeout"`     // default: 5s
	RaftHeartbeatTimeout   time.Duration `yaml:"raft_heartbeat_timeout"`    // default: 2s
	RaftApplyTimeout       time.Duration `yaml:"raft_apply_timeout"`        // default: 30s
	RaftLeaderLeaseTimeout time.Duration `yaml:"raft_leader_lease_timeout"` // default: 2s (must be <= heartbeat timeout)

	// Dynamic discovery configuration (always enabled)
	ClusterSyncInterval time.Duration `yaml:"cluster_sync_interval"` // default: 30s
	PeerInactivityLimit time.Duration `yaml:"peer_inactivity_limit"` // default: 24h
	MinClusterSize      int           `yaml:"min_cluster_size"`      // default: 1

	// Olric cache configuration
	OlricHTTPPort       int `yaml:"olric_http_port"`       // Olric HTTP API port (default: 10102)
	OlricMemberlistPort int `yaml:"olric_memberlist_port"` // Olric memberlist port (default: 3322)

	// IPFS storage configuration
	IPFS IPFSConfig `yaml:"ipfs"`
}

// IPFSConfig contains IPFS storage configuration
type IPFSConfig struct {
	// ClusterAPIURL is the IPFS Cluster HTTP API URL (e.g., "http://localhost:9094")
	// If empty, IPFS storage is disabled for this node
	ClusterAPIURL string `yaml:"cluster_api_url"`

	// APIURL is the IPFS HTTP API URL for content retrieval (e.g., "http://localhost:10107")
	// If empty, defaults to "http://localhost:10107"
	APIURL string `yaml:"api_url"`

	// Timeout for IPFS operations
	// If zero, defaults to 60 seconds
	Timeout time.Duration `yaml:"timeout"`

	// ReplicationFactor is the replication factor for pinned content
	// If zero, defaults to 3
	ReplicationFactor int `yaml:"replication_factor"`

	// EnableEncryption is accepted in node.yaml for DecodeStrict compatibility.
	// No code path encrypts IPFS uploads; the value is ignored.
	EnableEncryption bool `yaml:"enable_encryption"`
}
