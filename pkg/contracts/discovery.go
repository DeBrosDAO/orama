package contracts

import (
	"context"
	"time"
)

// PeerDiscovery handles peer discovery and connection management.
// Provides mechanisms for finding and connecting to network peers
// without relying on a DHT (Distributed Hash Table).
type PeerDiscovery interface {
	// Start begins periodic peer discovery with the given configuration.
	// Runs discovery in the background until Stop is called.
	Start(config DiscoveryConfig) error

	// Stop halts the peer discovery process and cleans up resources.
	Stop()

	// StartProtocolHandler registers the peer exchange protocol handler.
	// Must be called to enable incoming peer exchange requests.
	StartProtocolHandler()

	// TriggerPeerExchange manually triggers peer exchange with all connected peers.
	// Useful for bootstrapping or refreshing peer metadata.
	// Returns the number of peers from which metadata was collected.
	TriggerPeerExchange(ctx context.Context) int
}

// DiscoveryConfig contains configuration for peer discovery.
type DiscoveryConfig struct {
	// DiscoveryInterval is how often to run peer discovery.
	DiscoveryInterval time.Duration

	// MaxConnections is the maximum number of new connections per discovery round.
	MaxConnections int
}
