package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// The cluster gateway of each node joins the node's pubsub service to the
// services on the other nodes. Each service is a libp2p host of its own on the
// node's WireGuard address and no configuration names another node's, so
// without this a message published through one node's gateway reached only the
// subscribers on that node. The gateway registers its node's service in the
// cluster registry, the one database every node shares, and has the service
// connect to every other registered one. GossipSub does the rest: a connection
// is all the mesh needs.
const (
	// pubsubMeshInterval is how often a gateway refreshes its registration and
	// connects to the peers registered since.
	pubsubMeshInterval = 15 * time.Second

	// pubsubMeshPeerTTL is how long a registration is believed without a
	// refresh: several intervals, so a slow write does not drop a live node,
	// short enough that a node that left is not dialled for long.
	pubsubMeshPeerTTL = 2 * time.Minute

	// pubsubMeshRetention is how long a registration is kept at all. A service
	// whose identity changed leaves its old row behind.
	pubsubMeshRetention = 24 * time.Hour

	// pubsubMeshCallTimeout bounds one reconcile: the registry reads and
	// writes, and the service dialling its peers.
	pubsubMeshCallTimeout = 30 * time.Second
)

// pubsubMeshService is the node's pubsub service as the mesh needs it
// (pubsub.HTTPClient).
type pubsubMeshService interface {
	MeshSelf(ctx context.Context) (pubsub.MeshSelf, error)
	MeshConnect(ctx context.Context, addrs, allow []string) (pubsub.MeshConnectResult, error)
}

// pubsubMeshServiceOf is the pubsub service behind c, or nil when c does not
// reach one over the service's API.
func pubsubMeshServiceOf(c client.NetworkClient) pubsubMeshService {
	concrete, ok := c.(*client.Client)
	if !ok {
		return nil
	}
	service, _ := concrete.PubSubAdapter().(pubsubMeshService)
	return service
}

// PubsubMesh keeps this node's pubsub service connected to the others'.
type PubsubMesh struct {
	service pubsubMeshService
	db      *sql.DB
	nodeID  string
	logger  *zap.Logger
	now     func() time.Time
}

// NewPubsubMesh returns the mesh of this node's pubsub service. db is the
// cluster registry; nodeID names this node in it.
func NewPubsubMesh(service pubsubMeshService, db *sql.DB, nodeID string, logger *zap.Logger) *PubsubMesh {
	return &PubsubMesh{service: service, db: db, nodeID: nodeID, logger: logger, now: time.Now}
}

// Run registers the service and connects its peers now and then every
// pubsubMeshInterval until ctx ends. A reconcile that fails — the table not
// yet created, the service not up, the registry between leaders — is logged
// and retried at the next tick; nothing ends the mesh but ctx.
func (m *PubsubMesh) Run(ctx context.Context) error {
	m.reconcileLogged(ctx)
	ticker := time.NewTicker(pubsubMeshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.reconcileLogged(ctx)
		}
	}
}

func (m *PubsubMesh) reconcileLogged(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, pubsubMeshCallTimeout)
	defer cancel()
	if err := m.reconcile(ctx); err != nil && ctx.Err() == nil {
		m.logger.Warn("pubsub mesh: reconcile failed, will retry", zap.Error(err))
	}
}

func (m *PubsubMesh) initTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _pubsub_mesh_peers (
			peer_id   TEXT PRIMARY KEY,
			node_id   TEXT NOT NULL,
			multiaddr TEXT NOT NULL,
			last_seen INTEGER NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create the pubsub mesh registry table: %w", err)
	}
	return nil
}

// reconcile registers this node's service, forgets registrations nobody
// refreshes and has the service connect to every live peer.
func (m *PubsubMesh) reconcile(ctx context.Context) error {
	// CREATE TABLE IF NOT EXISTS is idempotent, so it is part of every pass:
	// run once at boot, one failure (a leader election mid-upgrade) ended the
	// mesh for the life of the gateway.
	if err := m.initTable(ctx); err != nil {
		return err
	}
	self, err := m.service.MeshSelf(ctx)
	if err != nil {
		return fmt.Errorf("ask the pubsub service for its address (check that orama-namespace-pubsub@index is running): %w", err)
	}
	now := m.now()
	if _, err := m.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO _pubsub_mesh_peers (peer_id, node_id, multiaddr, last_seen) VALUES (?, ?, ?, ?)`,
		self.PeerID, m.nodeID, self.Addrs[0], now.Unix()); err != nil {
		return fmt.Errorf("register pubsub service %s: %w", self.PeerID, err)
	}
	if _, err := m.db.ExecContext(ctx,
		`DELETE FROM _pubsub_mesh_peers WHERE last_seen < ?`, now.Add(-pubsubMeshRetention).Unix()); err != nil {
		return fmt.Errorf("forget stale pubsub registrations: %w", err)
	}
	peers, err := m.registeredPeers(ctx, self.PeerID, now)
	if err != nil {
		return err
	}
	// An empty registry is still a round: it clears the peers the service
	// admitted from the last one.
	addrs := ringSuccessors(peers, self.PeerID, pubsub.MaxMeshPeers)
	allow := ringPredecessors(peers, self.PeerID, pubsub.MaxMeshPeers)
	result, err := m.service.MeshConnect(ctx, addrs, allow)
	if err != nil {
		return fmt.Errorf("have the pubsub service connect to %d peers: %w", len(addrs), err)
	}
	for _, f := range result.Failed {
		m.logger.Warn("pubsub mesh: peer unreachable, will retry while it stays registered",
			zap.String("peer", f.Addr), zap.String("error", f.Error))
	}
	return nil
}

// registeredPeer is one live registration.
type registeredPeer struct {
	id   string
	addr string
}

// The live registrations excluding this service are split in two, each at
// most pubsub.MaxMeshPeers long: the peers that follow this one in peer-id
// order (wrapping) are dialled, the ones that precede it are allowlisted to
// connect in. If A dials B, B is within A's first MaxMeshPeers successors, so A
// is within B's first MaxMeshPeers predecessors: whoever dials a node is
// always on its allowlist, at any fleet size. The service refuses a longer
// list, and GossipSub needs a connected graph, not every pair.
//
// ringPredecessors returns up to limit addresses of the peers (sorted by id)
// that precede self, nearest first, wrapping around.
func ringPredecessors(peers []registeredPeer, self string, limit int) []string {
	start := sort.Search(len(peers), func(i int) bool { return peers[i].id >= self }) - 1
	out := make([]string, 0, min(limit, len(peers)))
	for i := 0; i < len(peers) && len(out) < limit; i++ {
		out = append(out, peers[((start-i)%len(peers)+len(peers))%len(peers)].addr)
	}
	return out
}

// ringSuccessors returns up to limit addresses of the peers (sorted by id)
// that follow self, wrapping around.
func ringSuccessors(peers []registeredPeer, self string, limit int) []string {
	start := sort.Search(len(peers), func(i int) bool { return peers[i].id > self })
	out := make([]string, 0, min(limit, len(peers)))
	for i := 0; i < len(peers) && len(out) < limit; i++ {
		out = append(out, peers[(start+i)%len(peers)].addr)
	}
	return out
}

func (m *PubsubMesh) registeredPeers(ctx context.Context, excluding string, now time.Time) ([]registeredPeer, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT peer_id, multiaddr FROM _pubsub_mesh_peers WHERE peer_id != ? AND last_seen >= ? ORDER BY peer_id`,
		excluding, now.Add(-pubsubMeshPeerTTL).Unix())
	if err != nil {
		return nil, fmt.Errorf("list registered pubsub services: %w", err)
	}
	defer rows.Close()
	var peers []registeredPeer
	for rows.Next() {
		var p registeredPeer
		if err := rows.Scan(&p.id, &p.addr); err != nil {
			return nil, fmt.Errorf("read a registered pubsub service: %w", err)
		}
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list registered pubsub services: %w", err)
	}
	return peers, nil
}
