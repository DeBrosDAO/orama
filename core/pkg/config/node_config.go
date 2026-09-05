package config

// NodeConfig contains node-specific configuration
type NodeConfig struct {
	ID              string   `yaml:"id"`                        // Auto-generated if empty
	ListenAddresses []string `yaml:"listen_addresses"`          // LibP2P listen addresses
	DataDir         string   `yaml:"data_dir"`                  // Data directory
	MaxConnections  int      `yaml:"max_connections"`           // Maximum peer connections
	Domain          string   `yaml:"domain"`                    // Domain for this node (e.g., node-1.orama.network)
	SSHUser         string   `yaml:"ssh_user,omitempty"`        // SSH user for remote management
	Environment     string   `yaml:"environment,omitempty"`     // Environment name (devnet, testnet, etc.)
	OperatorWallet  string   `yaml:"operator_wallet,omitempty"` // Operator wallet address
}
