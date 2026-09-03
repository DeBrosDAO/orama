package membership

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Interval is how often the reconciler runs.
const Interval = 60 * time.Second

// DiscoveryReader is the libp2p view of who is currently reachable. Kept as an
// interface so the reconciler can be driven without a running host.
type DiscoveryReader interface {
	// LivePeerIDs returns the peer ids discovery can currently see.
	LivePeerIDs() map[string]struct{}
}

// Reconciler keeps the membership stores in step with each other.
//
// It runs on one node at a time — the raft leader — because these are
// cluster-wide writes and having every node make them concurrently is how the
// stores diverged in the first place.
type Reconciler struct {
	db        *sql.DB
	discovery DiscoveryReader
	isLeader  func() bool
	logger    *zap.Logger
}

// NewReconciler builds a reconciler. A nil logger is replaced with a no-op.
func NewReconciler(db *sql.DB, discovery DiscoveryReader, isLeader func() bool, logger *zap.Logger) *Reconciler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Reconciler{
		db:        db,
		discovery: discovery,
		isLeader:  isLeader,
		logger:    logger.With(zap.String("component", "membership-reconciler")),
	}
}

// Run reconciles every Interval until ctx is done.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.logger.Warn("Membership reconcile failed", zap.Error(err))
			}
		}
	}
}

// Reconcile gathers the evidence, builds a plan and applies it.
//
// It applies the whole plan rather than one change per cycle: unlike a raft
// configuration change, deleting a stale row commits nothing and cannot cost
// the cluster its quorum, and leaving half a departed node's rows behind for
// another minute has no benefit.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if r.isLeader == nil || !r.isLeader() {
		return nil
	}
	if r.db == nil {
		return fmt.Errorf("membership reconcile needs a database handle")
	}

	evidence, err := r.gather(ctx)
	if err != nil {
		return err
	}

	plan := BuildPlan(evidence)

	if len(plan.OrphanWireGuardPeers) > 0 {
		// Reported every cycle on purpose: an orphan is either a node mid-join
		// (which resolves itself within a cycle or two) or a row nothing owns,
		// and the second needs a human. Deleting them automatically would sever
		// the mesh the first time the join handler and dns_nodes disagreed.
		r.logger.Warn("WireGuard peers with no matching node record",
			zap.Strings("rows", plan.OrphanWireGuardPeers))
	}

	if plan.Empty() {
		return nil
	}

	// Confirmations first. They only ever protect rows, so doing them before
	// the deletions means a node that appeared between the read and now is
	// latched rather than raced against.
	for _, nodeID := range plan.ConfirmWireGuardPeers {
		if _, err := rqlite.SafeExecContext(r.db, ctx,
			`UPDATE wireguard_peers SET confirmed_at = CURRENT_TIMESTAMP
			  WHERE node_id = ? AND confirmed_at IS NULL`, nodeID); err != nil {
			return fmt.Errorf("confirm wireguard peer %s: %w", nodeID, err)
		}
		r.logger.Info("Confirmed the WireGuard peer of a node that came up", zap.String("node_id", nodeID))
	}

	for _, nodeID := range plan.DropUnconfirmedWireGuardPeers {
		// The confirmed_at IS NULL predicate makes the delete safe against the
		// evidence read being stale: if the node came up in the meantime and
		// something confirmed the row, this deletes nothing.
		if _, err := rqlite.SafeExecContext(r.db, ctx,
			`DELETE FROM wireguard_peers WHERE node_id = ? AND confirmed_at IS NULL`, nodeID); err != nil {
			return fmt.Errorf("drop unconfirmed wireguard peer %s: %w", nodeID, err)
		}
		r.logger.Info("Removed the WireGuard peer left behind by a join that never completed",
			zap.String("node_id", nodeID))
	}

	for _, nodeID := range plan.DropWireGuardPeers {
		if _, err := rqlite.SafeExecContext(r.db, ctx,
			`DELETE FROM wireguard_peers WHERE node_id = ?`, nodeID); err != nil {
			return fmt.Errorf("drop wireguard peer %s: %w", nodeID, err)
		}
		r.logger.Info("Removed the WireGuard peer of a departed node", zap.String("node_id", nodeID))
	}

	for _, peerID := range plan.DropDNSNodes {
		if _, err := rqlite.SafeExecContext(r.db, ctx,
			`DELETE FROM dns_nodes WHERE id = ?`, peerID); err != nil {
			return fmt.Errorf("drop dns node %s: %w", peerID, err)
		}
		r.logger.Info("Removed the node record of a departed node", zap.String("peer_id", peerID))
	}

	return nil
}

// gather reads the current state of every store the plan is built from.
func (r *Reconciler) gather(ctx context.Context) (Evidence, error) {
	e := Evidence{
		Tombstoned: map[string]time.Time{},
		Discovered: map[string]struct{}{},
		Now:        time.Now().UTC(),
	}

	nodes, err := r.readNodes(ctx)
	if err != nil {
		return e, err
	}
	e.Nodes = nodes

	rows, err := r.readWireGuardRows(ctx)
	if err != nil {
		return e, err
	}
	e.WireGuardRows = rows

	tombstoned, err := r.readTombstones(ctx)
	if err != nil {
		return e, err
	}
	e.Tombstoned = tombstoned

	if r.discovery != nil {
		e.Discovered = r.discovery.LivePeerIDs()
	}

	return e, nil
}

func (r *Reconciler) readNodes(ctx context.Context) ([]Node, error) {
	rows, err := rqlite.SafeQueryContext(r.db, ctx,
		`SELECT id, COALESCE(internal_ip, ''), status, COALESCE(last_seen, '') FROM dns_nodes`)
	if err != nil {
		return nil, fmt.Errorf("read dns_nodes: %w", err)
	}
	defer rows.Close()

	var out []Node
	for rows.Next() {
		var n Node
		var lastSeen string
		if err := rows.Scan(&n.PeerID, &n.InternalIP, &n.Status, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan dns_nodes: %w", err)
		}
		n.LastSeen = parseTimestamp(lastSeen)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dns_nodes: %w", err)
	}
	return out, nil
}

func (r *Reconciler) readWireGuardRows(ctx context.Context) ([]WireGuardRow, error) {
	rows, err := rqlite.SafeQueryContext(r.db, ctx,
		`SELECT node_id, wg_ip, public_key, COALESCE(created_at, ''), COALESCE(confirmed_at, '')
		   FROM wireguard_peers`)
	if err != nil {
		return nil, fmt.Errorf("read wireguard_peers: %w", err)
	}
	defer rows.Close()

	var out []WireGuardRow
	for rows.Next() {
		var w WireGuardRow
		var createdAt, confirmedAt string
		if err := rows.Scan(&w.NodeID, &w.WGIP, &w.PublicKey, &createdAt, &confirmedAt); err != nil {
			return nil, fmt.Errorf("scan wireguard_peers: %w", err)
		}
		w.CreatedAt = parseTimestamp(createdAt)
		w.ConfirmedAt = parseTimestamp(confirmedAt)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wireguard_peers: %w", err)
	}
	return out, nil
}

func (r *Reconciler) readTombstones(ctx context.Context) (map[string]time.Time, error) {
	out := map[string]time.Time{}

	// Tombstones are keyed by raft node id, which is the raft advertise
	// address; the membership plan is keyed by peer id. dns_nodes.internal_ip
	// is what bridges them, and the tombstone carries the peer id when the
	// evicting node was able to resolve it.
	rows, err := rqlite.SafeQueryContext(r.db, ctx,
		`SELECT COALESCE(t.peer_id, ''), COALESCE(n.id, ''), t.evicted_at
		   FROM raft_evicted_nodes t
		   LEFT JOIN dns_nodes n
		     ON n.internal_ip = substr(t.raft_addr, 1, instr(t.raft_addr, ':') - 1)`)
	if err != nil {
		return nil, fmt.Errorf("read raft_evicted_nodes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var peerID, joinedPeerID, evictedAt string
		if err := rows.Scan(&peerID, &joinedPeerID, &evictedAt); err != nil {
			return nil, fmt.Errorf("scan raft_evicted_nodes: %w", err)
		}
		if peerID == "" {
			peerID = joinedPeerID
		}
		if peerID == "" {
			// A tombstone whose node has already left dns_nodes and that
			// carried no peer id has nothing left to key on. Nothing to do.
			continue
		}
		out[peerID] = parseTimestamp(evictedAt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate raft_evicted_nodes: %w", err)
	}
	return out, nil
}

// timestampLayouts are the shapes SQLite hands back for a TIMESTAMP column,
// depending on whether it was written by CURRENT_TIMESTAMP or by a driver that
// round-trips a Go time.
var timestampLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	time.RFC3339,
}

// parseTimestamp reads a stored timestamp as UTC. An unparseable or empty value
// returns the zero time, which the plan treats as "no evidence of liveness"
// rather than as recent — the safe direction is to fall through to the other
// checks, not to protect a row for ever because its timestamp was malformed.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
