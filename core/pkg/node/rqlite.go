package node

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/namespace"
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
			n.lifecycle,
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

	// Repair the WireGuard mesh in the window after rqlited is listening but
	// before it blocks waiting for a leader.
	//
	// Raft talks to its peers over the mesh, so a node that has lost its peers
	// can never reach a quorum — and the steady-state peer sync runs later in
	// Node.Start, which this call never returns to. That ordering is what turned
	// a routine restart into a multi-day outage. Repairing here, off the local
	// replica, lets a partitioned node rebuild its own transport and then
	// converge normally.
	n.rqliteManager.SetOnProcessStarted(n.bootstrapWireGuardMesh)

	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return fmt.Errorf("expand data dir: %w", err)
	}
	sup := namespace.NewIndexSupervisor(filepath.Dir(dataDir), n.logger.Logger)
	extra := indexRQLiteExtraArgs(n.config.Database)
	requireExisting := namespace.HasExistingRaft(sup.CoreRQLiteDir())
	if err := sup.EnsureRQLite(ctx, nodeID, n.config.Discovery.HttpAdvAddress, n.config.Discovery.RaftAdvAddress, n.config.Database.RQLiteJoinAddress, extra, requireExisting); err != nil {
		return err
	}

	if err := n.rqliteManager.Start(ctx); err != nil {
		return err
	}

	bindAddr, _, _ := net.SplitHostPort(n.config.Discovery.HttpAdvAddress)
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	if err := sup.EnsureOlric(ctx, nodeID, bindAddr, nil); err != nil {
		return fmt.Errorf("index olric: %w", err)
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

func indexRQLiteExtraArgs(db config.DatabaseConfig) string {
	election := db.RaftElectionTimeout
	if election == 0 {
		election = 5 * time.Second
	}
	heartbeat := db.RaftHeartbeatTimeout
	if heartbeat == 0 {
		heartbeat = 2 * time.Second
	}
	apply := db.RaftApplyTimeout
	if apply == 0 {
		apply = 30 * time.Second
	}
	lease := db.RaftLeaderLeaseTimeout
	if lease == 0 {
		lease = 2 * time.Second
	}
	args := fmt.Sprintf("-raft-election-timeout %s -raft-timeout %s -raft-apply-timeout %s -raft-leader-lease-timeout %s",
		election, heartbeat, apply, lease)
	if db.RQLiteAuthFile != "" {
		args += " -auth " + db.RQLiteAuthFile
	}
	return args
}
