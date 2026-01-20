package installers

import (
	"io"
)

// Installer defines the interface for service installers
type Installer interface {
	// Install downloads and installs the service binary
	Install() error

	// Configure initializes configuration for the service
	Configure() error

	// IsInstalled checks if the service is already installed
	IsInstalled() bool
}

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
	Addrs  []string // Cluster multiaddresses (e.g., /ip4/x.x.x.x/tcp/9098)
}
