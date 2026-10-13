package installers

import (
	"io"
)

// BaseInstaller provides common functionality for all installers
type BaseInstaller struct {
	arch      string
	logWriter io.Writer
}

// NewBaseInstaller creates a new base installer with common dependencies
func NewBaseInstaller(arch string, logWriter io.Writer) *BaseInstaller {
	return &BaseInstaller{
		arch:      arch,
		logWriter: logWriter,
	}
}

// IPFSPeerInfo holds IPFS peer information for configuring Peering.Peers
type IPFSPeerInfo struct {
	PeerID string
	Addrs  []string
}

// IPFSClusterPeerInfo contains IPFS Cluster peer information for cluster peer discovery
type IPFSClusterPeerInfo struct {
	PeerID string   // Cluster peer ID (different from IPFS peer ID)
	Addrs  []string // Cluster multiaddresses (/ip4/<wg-ip>/tcp/<constants.IPFSClusterSwarmPort>/p2p/<id>)
}
