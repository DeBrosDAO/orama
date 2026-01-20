package config

import "time"

// DiscoveryConfig contains peer discovery configuration
type DiscoveryConfig struct {
	BootstrapPeers    []string      `yaml:"bootstrap_peers"`    // Peer addresses to connect to
	DiscoveryInterval time.Duration `yaml:"discovery_interval"` // Discovery announcement interval
	BootstrapPort     int           `yaml:"bootstrap_port"`     // Default port for peer discovery
	HttpAdvAddress    string        `yaml:"http_adv_address"`   // HTTP advertisement address
	RaftAdvAddress    string        `yaml:"raft_adv_address"`   // Raft advertisement
	NodeNamespace     string        `yaml:"node_namespace"`     // Namespace for node identifiers
}
