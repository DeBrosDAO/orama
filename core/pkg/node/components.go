package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/node/boot"
)

// Component names. They appear in logs and in the lifecycle reasoning below,
// so they are declared once rather than spelled out at each use.
const (
	compDataDir           = "data-dir"
	compWireGuard         = "wireguard"
	compLibP2P            = "libp2p"
	compPeerInfo          = "peer-info"
	compIPFSClusterConfig = "ipfs-cluster-config"
	compClusterDiscovery  = "cluster-discovery"
	compStorage           = "storage"
	compRQLiteLocal       = "rqlite-local"
	compNameserver        = "nameserver"
	compPubsub            = "pubsub"
	compGateway           = "gateway"
	compEdgeServing       = "edge-serving"
	compEdgeAux           = "edge-aux"
	compMonitoring        = "monitoring"
	compRQLiteCluster     = "rqlite-cluster"
	compWireGuardSync     = "wireguard-sync"
	compIPFSSwarmSync     = "ipfs-swarm-sync"
	compDNSRegistration   = "dns-registration"
	compMembership        = "membership"
)

// Per-attempt budgets. Each one bounds a single Reconcile; the supervisor
// retries, so these cap how long one stuck attempt can stall the pass, not how
// long the node is willing to keep trying.
const (
	// gatewayStartTimeout bounds bringing the index gateway up. The other
	// local-service reconciles are NOT wrapped in a timeout: EnsureWireGuard,
	// EnsureIPFS*, EnsureCoreDNS, EnsurePubsub and the five EnsureX calls
	// behind the edge either take no context or ignore the one they are given,
	// so a wrapper would claim a bound it cannot enforce. Each is bounded
	// instead by the spawners' own 30s wait for the unit to report active.
	//
	// startRQLiteLocal is the exception worth knowing about: it is the longest
	// unwrapped reconcile, at roughly three minutes in the worst case (up to
	// two of them in waitForMinClusterSizeBeforeStart, plus the unit start, the
	// connect backoff and Olric). Because a pass is sequential, that is also
	// the longest anything ordered after it can be delayed. It is not wrapped
	// because it starts cluster discovery's long-lived goroutines from the
	// context it is given.
	gatewayStartTimeout = 3 * time.Minute

	// dnsWorkTimeout bounds the DNS registration writes. The heartbeat
	// goroutine they start is not covered by it; see startDNSRegistration.
	dnsWorkTimeout = 30 * time.Second
)

// bootComponents declares the node's start-up graph.
//
// The split into two tiers is the whole point. Local components need nothing
// but this machine: they come up on a node that is alone in the world, and a
// node whose local tier is up serves HTTPS, DNS, the gateway and its tenants.
// Cluster components need a raft quorum, so they hang off rqlite-cluster and
// nothing else waits behind them. A component belongs to the cluster tier
// exactly when it transitively depends on rqlite-cluster, so the graph itself
// is the record — there is no second list to keep in step with it.
//
// Order must be a valid dependency order — the supervisor refuses a forward
// reference — which also means a single pass converges the graph. Components
// that depend on nothing expensive come first, so a slow rqlite does not hold
// up peer monitoring behind it.
func (n *Node) bootComponents() []boot.Component {
	return []boot.Component{
		{
			Name:      compDataDir,
			Reconcile: n.ensureDataDir,
		},
		{
			Name:      compWireGuard,
			DependsOn: []string{compDataDir},
			Reconcile: n.startIndexWireGuard,
		},
		{
			Name:      compLibP2P,
			DependsOn: []string{compDataDir},
			Reconcile: func(context.Context) error { return n.startLibP2P() },
		},
		{
			Name:      compPeerInfo,
			DependsOn: []string{compLibP2P},
			Reconcile: func(context.Context) error { return n.writePeerInfo() },
		},
		{
			Name:      compMonitoring,
			DependsOn: []string{compLibP2P},
			Reconcile: func(ctx context.Context) error { n.startConnectionMonitoring(ctx); return nil },
		},
		{
			Name:      compPubsub,
			DependsOn: []string{compLibP2P},
			Reconcile: n.startIndexPubsub,
		},
		{
			Name:      compIPFSClusterConfig,
			DependsOn: []string{compDataDir},
			Reconcile: func(context.Context) error { return n.startIPFSClusterConfig() },
		},
		// Storage does NOT depend on ipfs-cluster-config: a node whose cluster
		// config cannot be written should still run its IPFS daemon. Order
		// still puts the config first, so the normal case is unchanged; the
		// difference is that a config failure now degrades the node visibly
		// instead of being logged once and forgotten.
		{
			Name:      compStorage,
			DependsOn: []string{compDataDir},
			Reconcile: n.startIndexStorage,
		},
		// Cluster discovery is separate from rqlite-local because the
		// goroutines it starts must live as long as the node does; folding it
		// in would tie them to that component's attempt.
		{
			Name:      compClusterDiscovery,
			DependsOn: []string{compLibP2P},
			Reconcile: n.startClusterDiscovery,
		},
		{
			Name:      compRQLiteLocal,
			DependsOn: []string{compWireGuard, compClusterDiscovery, compStorage},
			Reconcile: n.startRQLiteLocal,
			Health:    n.rqliteLocalHealthy,
		},
		{
			Name:      compNameserver,
			DependsOn: []string{compRQLiteLocal},
			Reconcile: n.startNameserver,
		},
		{
			Name:      compGateway,
			DependsOn: []string{compRQLiteLocal, compStorage, compPubsub},
			Reconcile: boot.WithTimeout(gatewayStartTimeout, n.startIndexGateway),
		},
		{
			Name:      compEdgeServing,
			DependsOn: []string{compGateway},
			Reconcile: n.startIndexEdgeServing,
		},
		{
			Name:      compEdgeAux,
			DependsOn: []string{compEdgeServing},
			Reconcile: n.startIndexEdgeAux,
		},
		// The mesh sync is outside the cluster tier on purpose, even though it
		// reads a cluster table. Raft runs OVER the mesh, so a node whose
		// interface has lost its peers can never reach a quorum — which is why
		// loadDesiredWireGuardPeers falls back to this node's own replica and
		// applies additively. Putting the sync behind the quorum gate would
		// make the repair conditional on the thing it repairs.
		{
			Name:      compWireGuardSync,
			DependsOn: []string{compWireGuard, compRQLiteLocal},
			Reconcile: n.startWireGuardSync,
		},
		// Swarm peering is outside it for a weaker reason: syncIPFSSwarmPeers
		// reads only the leader-routed handle, so without a quorum it logs and
		// returns. It sits here because starting its loop early costs nothing
		// and the loop picks the work up on its own once quorum returns — not
		// because it works without one.
		{
			Name:      compIPFSSwarmSync,
			DependsOn: []string{compStorage, compRQLiteLocal},
			Reconcile: n.startIPFSSwarmSync,
		},

		// Cluster tier. rqlite-cluster is the single component that waits for
		// consensus, so a quorum outage degrades exactly it and its dependents
		// and leaves everything above still serving.
		{
			Name:      compRQLiteCluster,
			DependsOn: []string{compRQLiteLocal},
			Reconcile: n.joinRQLiteCluster,
			Health:    n.rqliteLeaderReachable,
		},
		// One writer for the stores a node's existence is recorded in. Only the
		// raft leader acts, but every node runs the loop so leadership can move
		// without anything being restarted.
		{
			Name:      compMembership,
			DependsOn: []string{compRQLiteCluster},
			Reconcile: n.startMembershipReconciler,
		},
		// A dns_nodes row saying `active` is a promise: this node terminates
		// TLS, answers DNS and proxies tenants. So the registration depends on
		// the units that keep that promise, not only on the leader it needs in
		// order to write the row.
		//
		// Getting this wrong is a fail-open, and the old sequential start-up
		// got it right by accident: registerDNSNode ran last, so a node whose
		// Caddy failed exited before it could advertise itself, and peers
		// purged its records after 120s. With independent components, a node
		// that never brought up its gateway would otherwise register as active
		// and keep its own heartbeat fresh forever, and every consumer that
		// filters on `status = 'active' AND last_seen > ?` would route real
		// traffic to it.
		{
			Name:      compDNSRegistration,
			DependsOn: []string{compRQLiteCluster, compGateway, compNameserver, compEdgeServing},
			Reconcile: n.startDNSRegistration,
		},
	}
}

// registerComponents declares the node's start-up graph on sup.
func (n *Node) registerComponents(sup *boot.Supervisor) {
	for _, c := range n.bootComponents() {
		sup.Add(c)
	}
}

// ensureDataDir creates the node's data directory.
func (n *Node) ensureDataDir(context.Context) error {
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return fmt.Errorf("failed to expand data directory path: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	return nil
}

// startDNSRegistration registers this node in the DNS tables and starts the
// heartbeat that keeps the registration alive.
//
// The heartbeat is started once, from the supervisor's run context, so it
// outlives the bounded writes below and survives every retry of this component.
func (n *Node) startDNSRegistration(ctx context.Context) error {
	n.dnsHeartbeatOnce.Do(func() { n.startDNSHeartbeat(ctx) })

	workCtx, cancel := context.WithTimeout(ctx, dnsWorkTimeout)
	defer cancel()

	if err := n.registerDNSNode(workCtx); err != nil {
		return fmt.Errorf("failed to register DNS node: %w", err)
	}
	if err := n.ensureBaseDNSRecords(workCtx); err != nil {
		return fmt.Errorf("failed to ensure base DNS records: %w", err)
	}
	return nil
}

// errNodeStopping is returned by a reconcile that starts after Stop has begun.
// It stops a late attempt from re-creating state the teardown has released.
var errNodeStopping = errors.New("node is shutting down")
