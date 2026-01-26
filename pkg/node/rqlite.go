package node

import (
	"context"
	"fmt"

	database "github.com/DeBrosOfficial/network/pkg/rqlite"
)

// startRQLite initializes and starts the RQLite database
func (n *Node) startRQLite(ctx context.Context) error {
	n.logger.Info("Starting RQLite database")

	// Determine node identifier for log filename - use node ID for unique filenames
	nodeID := n.config.Node.ID
	if nodeID == "" {
		// Default to "node" if ID is not set
		nodeID = "node"
	}

	// Create RQLite manager
	n.rqliteManager = database.NewRQLiteManager(&n.config.Database, &n.config.Discovery, n.config.Node.DataDir, n.logger.Logger)
	n.rqliteManager.SetNodeType(nodeID)

	// Initialize cluster discovery service if LibP2P host is available
	if n.host != nil && n.discoveryManager != nil {
		// Create cluster discovery service (all nodes are unified)
		n.clusterDiscovery = database.NewClusterDiscoveryService(
			n.host,
			n.discoveryManager,
			n.rqliteManager,
			n.config.Node.ID,
			"node", // Unified node type
			n.config.Discovery.RaftAdvAddress,
			n.config.Discovery.HttpAdvAddress,
			n.config.Node.DataDir,
			n.logger.Logger,
		)

		// Set discovery service on RQLite manager BEFORE starting RQLite
		// This is critical for pre-start cluster discovery during recovery
		n.rqliteManager.SetDiscoveryService(n.clusterDiscovery)

		// Start cluster discovery (but don't trigger initial sync yet)
		if err := n.clusterDiscovery.Start(ctx); err != nil {
			return fmt.Errorf("failed to start cluster discovery: %w", err)
		}

		// Publish initial metadata (with log_index=0) so peers can discover us during recovery
		// The metadata will be updated with actual log index after RQLite starts
		n.clusterDiscovery.UpdateOwnMetadata()

		n.logger.Info("Cluster discovery service started (waiting for RQLite)")
	}

	// Start RQLite FIRST before updating metadata
	if err := n.rqliteManager.Start(ctx); err != nil {
		return err
	}

	// NOW update metadata after RQLite is running
	if n.clusterDiscovery != nil {
		n.clusterDiscovery.UpdateOwnMetadata()
		n.clusterDiscovery.TriggerSync() // Do initial cluster sync now that RQLite is ready
		n.logger.Info("RQLite metadata published and cluster synced")
	}

	// Create adapter for sql.DB compatibility
	adapter, err := database.NewRQLiteAdapter(n.rqliteManager)
	if err != nil {
		return fmt.Errorf("failed to create RQLite adapter: %w", err)
	}
	n.rqliteAdapter = adapter

	return nil
}

