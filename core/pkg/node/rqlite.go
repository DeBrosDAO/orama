package node

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/namespace"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
)

// newRQLiteManager builds the manager. It performs no I/O, so it belongs with
// the rest of the node's construction rather than inside a component: both the
// cluster-discovery and rqlite-local components need it, and neither should own
// creating it.
func (n *Node) newRQLiteManager() *database.RQLiteManager {
	mgr := database.NewRQLiteManager(&n.config.Database, &n.config.Discovery, n.config.Node.DataDir, n.logger.Logger)
	mgr.SetNodeType(n.nodeID())

	// Repair the WireGuard mesh in the window after rqlited is listening but
	// before anything waits for a leader.
	//
	// Raft talks to its peers over the mesh, so a node that has lost its peers
	// can never reach a quorum — and the steady-state peer sync needs the
	// adapter that rqlite start-up has not finished building. That ordering is
	// what turned a routine restart into a multi-day outage. Repairing here,
	// off the local replica, lets a partitioned node rebuild its own transport
	// and then converge normally.
	mgr.SetOnProcessStarted(n.bootstrapWireGuardMesh)
	return mgr
}

// nodeID is the identifier used for unit names and raft node IDs.
func (n *Node) nodeID() string {
	if n.config.Node.ID == "" {
		return "node"
	}
	return n.config.Node.ID
}

// startClusterDiscovery brings up the libp2p-backed peer exchange that RQLite
// uses to find its raft peers.
//
// It is its own component because the goroutines it starts must live as long as
// the node does. Folding it into rqlite-local would tie them to that
// component's per-attempt deadline, and peer discovery would silently stop
// minutes after boot.
func (n *Node) startClusterDiscovery(ctx context.Context) error {
	if n.getClusterDiscovery() != nil {
		return nil
	}
	if n.stopping.Load() {
		return errNodeStopping
	}
	if n.host == nil || n.discoveryManager == nil {
		return fmt.Errorf("libp2p host or discovery manager not available")
	}

	// Create cluster discovery service (all nodes are unified)
	svc := database.NewClusterDiscoveryService(
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
	n.rqliteManager.SetDiscoveryService(svc)

	if err := svc.Start(ctx); err != nil {
		n.rqliteManager.SetDiscoveryService(nil)
		return fmt.Errorf("failed to start cluster discovery: %w", err)
	}
	// Published under the lock: the monitoring loop reads it from its own
	// goroutine and may already be running.
	n.depsMu.Lock()
	n.clusterDiscovery = svc
	n.depsMu.Unlock()

	// Publish initial metadata (with log_index=0) so peers can discover us
	// during recovery. It is updated with the real log index once rqlite is
	// participating in raft.
	svc.UpdateOwnMetadata()

	n.logger.Info("Cluster discovery service started (waiting for RQLite)")
	return nil
}

// startRQLiteLocal brings up everything about the index RQLite that this node
// can do alone: the systemd unit, the local connection, the local Olric, and
// the sql.DB adapter.
//
// It deliberately does NOT wait for a raft leader. Everything ordered after
// rqlite in the old start-up sequence — CoreDNS, the gateway, the edge, the
// tenants — needs only the local replica to come up, so making them wait on
// other people's machines turned a peer outage into a local one.
//
// It is idempotent: the boot supervisor calls it again after any failure.
func (n *Node) startRQLiteLocal(ctx context.Context) error {
	if n.stopping.Load() {
		return errNodeStopping
	}
	n.logger.Info("Starting RQLite database")

	sup, _, err := n.indexSupervisor()
	if err != nil {
		return err
	}
	nodeID := n.nodeID()
	extra := indexRQLiteExtraArgs(n.config.Database)
	requireExisting := namespace.HasExistingRaft(sup.CoreRQLiteDir())
	// The libp2p peer id is this node's stable raft identity. The boot graph
	// orders rqlite-local after libp2p, so it is available here; an empty one
	// means libp2p is not up and rqlite keeps defaulting the id to the raft
	// advertise address, which is what it did before this existed.
	if err := sup.EnsureRQLite(ctx, nodeID, n.GetPeerID(), n.config.Discovery.HttpAdvAddress, n.config.Discovery.RaftAdvAddress, n.config.Database.RQLiteJoinAddress, extra, requireExisting); err != nil {
		return err
	}

	if err := n.rqliteManager.StartLocal(ctx); err != nil {
		return err
	}

	bindAddr, _, _ := net.SplitHostPort(n.config.Discovery.HttpAdvAddress)
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	if err := sup.EnsureOlric(ctx, nodeID, bindAddr, nil); err != nil {
		return fmt.Errorf("index olric: %w", err)
	}

	if n.getRQLiteAdapter() == nil {
		// sql.Open is lazy, so the adapter can be built before a leader exists.
		// The local-replica reads that the WireGuard sync depends on go through
		// it, and those are exactly the reads a quorum-less node needs.
		adapter, err := database.NewRQLiteAdapter(n.rqliteManager)
		if err != nil {
			return fmt.Errorf("failed to create RQLite adapter: %w", err)
		}
		n.depsMu.Lock()
		n.rqliteAdapter = adapter
		n.depsMu.Unlock()
	}

	return nil
}

// joinRQLiteCluster completes the half of RQLite start-up that needs the rest
// of the cluster. Returning an error here means "not yet", not "give up": the
// boot supervisor retries, and the node stays degraded until it succeeds.
func (n *Node) joinRQLiteCluster(ctx context.Context) error {
	if n.rqliteManager == nil {
		return fmt.Errorf("rqlite manager not initialized")
	}
	if err := n.rqliteManager.JoinCluster(ctx); err != nil {
		return err
	}

	if cd := n.getClusterDiscovery(); cd != nil {
		cd.UpdateOwnMetadata()
		cd.TriggerSync() // initial cluster sync now that RQLite is ready
		n.logger.Info("RQLite metadata published and cluster synced")
	}

	return nil
}

// rqliteLocalHealthy is the health signal for the local half of RQLite. A
// failure re-runs rqlite-local — which restarts the unit if it died and reopens
// the handle if it was closed — and blocks everything that reads the local
// replica until it is back.
func (n *Node) rqliteLocalHealthy(ctx context.Context) error {
	if n.rqliteManager == nil {
		return fmt.Errorf("rqlite manager not initialized")
	}
	return n.rqliteManager.LocalHealthy(ctx)
}

// rqliteLeaderReachable is the health signal for the cluster tier. When it
// fails the node goes back to degraded and every cluster-tier component is
// reconciled again once quorum returns.
func (n *Node) rqliteLeaderReachable(ctx context.Context) error {
	if n.rqliteManager == nil {
		return fmt.Errorf("rqlite manager not initialized")
	}
	return n.rqliteManager.LeaderReachable(ctx)
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
	// Enforcement is opt-in and independent of merely having credentials: see
	// DatabaseConfig.RQLiteEnforceAuth for why the two are separate.
	if db.RQLiteEnforceAuth {
		args += " -auth " + db.RQLiteAuthFile
	}
	return args
}
