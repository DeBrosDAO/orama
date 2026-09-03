package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/discovery"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
	"github.com/DeBrosOfficial/network/pkg/node/lifecycle"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	database "github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/host"
	"go.uber.org/zap"
)

// Node represents a network node with RQLite database
type Node struct {
	config *config.Config
	logger *logging.ColoredLogger

	// host is assigned once, by the libp2p component, under depsMu. Goroutines
	// the supervisor launched afterwards read it directly — their start orders
	// them after the write — but Stop reads it through hostRef.
	host host.Host

	// Lifecycle state machine (joining → active ⇄ degraded ⇄ maintenance)
	lifecycle *lifecycle.Manager

	rqliteManager    *database.RQLiteManager
	rqliteAdapter    *database.RQLiteAdapter
	clusterDiscovery *database.ClusterDiscoveryService

	// Peer discovery
	peerDiscoveryCancel context.CancelFunc

	// libp2pStarted is set only once startLibP2P has completed every step, so a
	// retry after a partial failure starts over instead of assuming the host is
	// usable.
	libp2pStarted bool

	// PubSub
	pubsub *pubsub.ClientAdapter

	// Discovery
	discoveryManager *discovery.Manager

	// depsMu guards every field the boot supervisor assigns after Start has
	// returned. Reads from goroutines the supervisor itself launched are
	// ordered by that launch, but the monitoring loop and Stop both read these
	// while a component may still be converging, so the assignments and those
	// reads take the lock.
	depsMu sync.RWMutex

	// stopping is set the moment Stop begins. Components check it so that a
	// reconcile still in flight cannot re-create state that teardown has
	// already released, and applyBootState checks it so the supervisor cannot
	// pull the node back out of maintenance on its way down.
	stopping atomic.Bool

	// IPFS Cluster config manager
	clusterConfigManager *ipfs.ClusterConfigManager

	// clusterCfgMu serialises writes to the IPFS cluster service.json. The
	// config component and the monitoring loop both repair it, and neither
	// ClusterConfigManager nor the file write is safe against the other.
	clusterCfgMu sync.Mutex

	// Boot supervisor: converges the components declared in components.go.
	bootSupervisor *boot.Supervisor
	bootDone       chan struct{}

	// Long-lived loops started by components the supervisor may reconcile more
	// than once. Each loop must start exactly once, and from the supervisor's
	// run context so it outlives any single attempt.
	dnsHeartbeatOnce  sync.Once
	monitoringOnce    sync.Once
	wgSyncOnce        sync.Once
	ipfsSwarmSyncOnce sync.Once

	// wgSyncMu serialises wg0.conf writes between a supervisor retry and the
	// periodic sync loop.
	wgSyncMu sync.Mutex
}

// NewNode creates a new network node
func NewNode(cfg *config.Config) (*Node, error) {
	// Create colored logger
	logger, err := logging.NewColoredLogger(logging.ComponentNode, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}

	n := &Node{
		config:    cfg,
		logger:    logger,
		lifecycle: lifecycle.NewManager(),
	}
	n.rqliteManager = n.newRQLiteManager()
	return n, nil
}

// Start begins converging the node's services and returns.
//
// It no longer runs start-up as a sequence that aborts the process on the
// first error. A node that boots without a raft quorum used to exit inside the
// RQLite step, be restarted by systemd, and exit again — never reaching
// CoreDNS, the gateway, the edge or the tenants, none of which need a quorum
// to serve. Now the components converge independently: the ones this machine
// can satisfy alone come up, the ones that need the cluster keep retrying, and
// the node announces itself as degraded until they succeed.
//
// The only errors Start returns are declaration errors — a malformed component
// graph — because those are bugs, not conditions that retrying could fix.
func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("Starting network node", zap.String("data_dir", n.config.Node.DataDir))

	sup := boot.New(n.logger.Logger, boot.Options{})
	n.registerComponents(sup)
	sup.OnChange(n.applyBootState)
	if err := sup.Err(); err != nil {
		return err
	}

	n.bootSupervisor = sup
	n.bootDone = make(chan struct{})

	go func() {
		defer close(n.bootDone)
		if err := sup.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			n.logger.ComponentError(logging.ComponentNode, "Boot supervisor stopped", zap.Error(err))
		}
	}()

	n.logger.ComponentInfo(logging.ComponentNode, "Node supervision started",
		zap.String("lifecycle", string(n.lifecycle.State())))
	return nil
}

// hostRef returns the libp2p host under the lock, for callers that are not
// ordered after the component that assigns it.
func (n *Node) hostRef() host.Host {
	n.depsMu.RLock()
	defer n.depsMu.RUnlock()
	return n.host
}

// getRQLiteAdapter returns the sql.DB adapter, or nil before rqlite-local has
// built it.
func (n *Node) getRQLiteAdapter() *database.RQLiteAdapter {
	n.depsMu.RLock()
	defer n.depsMu.RUnlock()
	return n.rqliteAdapter
}

// BootStatus reports the current state of every start-up component.
func (n *Node) BootStatus() boot.Snapshot {
	if n.bootSupervisor == nil {
		return boot.Snapshot{}
	}
	return n.bootSupervisor.Snapshot()
}

// bootShutdownGrace is how long Stop waits for the boot supervisor to leave its
// current attempt before tearing services down regardless.
//
// It must stay well below the unit's TimeoutStopSec, or systemd would SIGKILL
// the process at the moment teardown begins — including the raft leadership
// transfer, which is the one part of shutdown a cluster notices.
const bootShutdownGrace = 10 * time.Second

// Stop stops the node and all its services
func (n *Node) Stop() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Stopping network node")

	// Tell any reconcile still in flight that the node is going away, before
	// anything else. A component that gets past this check can still finish its
	// current attempt, but it will not start a new one and cannot re-create
	// what the teardown below releases.
	n.stopping.Store(true)

	// Announce maintenance FIRST, before waiting on anything. It touches only
	// the lifecycle manager and the discovery service, both of which are safe
	// to use concurrently, and during a rolling restart the whole point is that
	// peers learn we are going away promptly rather than up to a grace period
	// later.
	if n.lifecycle.IsAvailable() {
		if err := n.lifecycle.EnterMaintenance(5 * time.Minute); err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Failed to enter maintenance on shutdown", zap.Error(err))
		}
		if cd := n.getClusterDiscovery(); cd != nil {
			cd.UpdateOwnMetadata()
		}
	}

	// The supervisor writes the fields torn down below, so wait for its
	// goroutine to leave before touching any of them. It exits on the same
	// context cancellation that brought us here; the bound is a backstop for a
	// component attempt that ignores cancellation.
	if n.bootDone != nil {
		select {
		case <-n.bootDone:
		case <-time.After(bootShutdownGrace):
			n.logger.ComponentWarn(logging.ComponentNode,
				"Boot supervisor did not stop within the shutdown grace period; tearing down anyway",
				zap.Duration("grace", bootShutdownGrace),
				zap.Strings("not_converged", n.BootStatus().NotReady()))
		}
	}

	// Stop cluster discovery
	if cd := n.getClusterDiscovery(); cd != nil {
		cd.Stop()
	}

	// Stop peer reconnection loop
	if n.peerDiscoveryCancel != nil {
		n.peerDiscoveryCancel()
	}

	// Stop peer discovery
	n.stopPeerDiscovery()

	// Stop LibP2P host
	if h := n.hostRef(); h != nil {
		h.Close()
	}

	// Stop RQLite
	if adapter := n.getRQLiteAdapter(); adapter != nil {
		adapter.Close()
	}
	if n.rqliteManager != nil {
		_ = n.rqliteManager.Stop()
	}

	n.logger.ComponentInfo(logging.ComponentNode, "Network node stopped")
	return nil
}
