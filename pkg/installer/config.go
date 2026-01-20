// Package installer provides an interactive TUI installer for Orama Network
package installer

// InstallerConfig holds the configuration gathered from the TUI
type InstallerConfig struct {
	VpsIP          string
	Domain         string
	PeerDomain     string   // Domain of existing node to join
	PeerIP         string   // Resolved IP of peer domain (for Raft join)
	JoinAddress    string   // Auto-populated: {PeerIP}:7002 (direct RQLite TLS)
	Peers          []string // Auto-populated: /dns4/{PeerDomain}/tcp/4001/p2p/{PeerID}
	ClusterSecret  string
	SwarmKeyHex    string   // 64-hex IPFS swarm key (for joining private network)
	IPFSPeerID     string   // IPFS peer ID (auto-discovered from peer domain)
	IPFSSwarmAddrs []string // IPFS swarm addresses (auto-discovered from peer domain)
	// IPFS Cluster peer info for cluster discovery
	IPFSClusterPeerID string   // IPFS Cluster peer ID (auto-discovered from peer domain)
	IPFSClusterAddrs  []string // IPFS Cluster addresses (auto-discovered from peer domain)
	Branch            string
	IsFirstNode       bool
	NoPull            bool
}

// Step represents a step in the installation wizard
type Step int

const (
	StepWelcome Step = iota
	StepNodeType
	StepVpsIP
	StepDomain
	StepPeerDomain // Domain of existing node to join (replaces StepJoinAddress)
	StepClusterSecret
	StepSwarmKey // 64-hex swarm key for IPFS private network
	StepBranch
	StepNoPull
	StepConfirm
	StepInstalling
	StepDone
)
