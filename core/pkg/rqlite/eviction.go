package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"
)

// RaftMember is the subset of a /nodes entry a membership decision needs.
type RaftMember struct {
	ID string

	// Addr is the member's raft address. It is separate from ID because the two
	// are separate things: the id is identity, the address is where to reach
	// it. They happen to be equal on a node that predates stable raft ids, and
	// conflating them is what let an address change mint a second member.
	Addr string

	Voter     bool
	Reachable bool
}

// raftMember is the internal alias kept so existing call sites read naturally.
type raftMember = RaftMember

// evictionRefusal explains why a removal was not attempted. An empty string
// means it may proceed.
type evictionRefusal string

// SafeToRemoveVoter reports why EVICTING target — removing a member believed
// dead — would be unsafe, or "" if it may proceed.
//
// Use SafeToRemoveMember for a planned removal of a member that is still up.
// This one additionally refuses a reachable target, which is right for an
// eviction and wrong for a decommission or an identity migration.
//
// Exported so the operator-facing commands apply the same arithmetic as the
// automatic eviction. Two implementations of a quorum-safety check is one too
// many: the version that is wrong is the one nobody reads.
func SafeToRemoveVoter(members []RaftMember, target string) string {
	return string(safeToEvict(members, target))
}

// SafeToRemoveMember reports why removing target from the raft configuration
// would cost the cluster its quorum, or "" if it may proceed.
//
// This is the arithmetic alone, without eviction's "the target is answering, so
// this is not an eviction" veto. A decommission and an identity migration both
// remove a member that is up ON PURPOSE, and both were refused outright by the
// eviction rule — the migration could not execute a single step, because a node
// being migrated is by definition reachable.
//
// The target is counted as leaving whether or not it answers, which is the
// conservative direction: a reachable voter that is about to go still stops
// counting toward the quorum that has to survive without it.
func SafeToRemoveMember(members []RaftMember, target string) string {
	return string(quorumSurvivesRemoval(members, target))
}

// safeToEvict reports whether removing target from the raft configuration
// leaves a cluster that can still elect a leader and commit writes.
//
// The invariant is stated over the configuration AFTER the removal: the voters
// that can actually be reached must still meet the new quorum. That is a
// weaker-looking rule than it sounds, because removing an unreachable voter can
// never make things worse — quorum is floor(n/2)+1, which is monotonic in n, so
// dropping a member that was not answering either leaves the threshold where it
// was or lowers it while the reachable count stays the same. The check is here
// anyway, because the alternative to asserting it is discovering the exception.
//
// What it does refuse is removing a member that is answering. That is not
// eviction, it is a voter-set change, and it belongs to reconcileVoters where
// the node can be demoted in place instead of taken out of the configuration.
func safeToEvict(members []raftMember, target string) evictionRefusal {
	for _, m := range members {
		if m.ID == target && m.Reachable {
			return evictionRefusal(fmt.Sprintf(
				"node %s is reachable; a live member is demoted, not evicted", target))
		}
	}
	return quorumSurvivesRemoval(members, target)
}

// quorumSurvivesRemoval is the arithmetic both removal paths share.
//
// The invariant is stated over the configuration AFTER the removal: the voters
// that can actually be reached must still meet the new quorum. That is a
// weaker-looking rule than it sounds when the target is unreachable, because
// removing a voter that was not answering can never make things worse — quorum
// is floor(n/2)+1, which is monotonic in n, so dropping it either leaves the
// threshold where it was or lowers it while the reachable count stays the same.
// It bites when the target IS answering, which is the planned-removal case: the
// cluster has to survive losing a voter it currently has.
func quorumSurvivesRemoval(members []raftMember, target string) evictionRefusal {
	var (
		found          bool
		targetIsVoter  bool
		votersAfter    int
		reachableAfter int
	)

	for _, m := range members {
		if m.ID == target {
			found = true
			targetIsVoter = m.Voter
			continue
		}
		if !m.Voter {
			continue
		}
		votersAfter++
		if m.Reachable {
			reachableAfter++
		}
	}

	if !found {
		return evictionRefusal(fmt.Sprintf("node %s is not in the raft configuration", target))
	}
	if !targetIsVoter {
		// A non-voter costs the cluster nothing: it does not count toward
		// quorum, so there is no availability argument for removing it and no
		// reason to take the risk.
		return evictionRefusal(fmt.Sprintf("node %s is a non-voter; removing it gains no quorum headroom", target))
	}
	if votersAfter == 0 {
		return evictionRefusal("removing the last voter would leave no cluster to rejoin")
	}

	quorumAfter := votersAfter/2 + 1
	if reachableAfter < quorumAfter {
		return evictionRefusal(fmt.Sprintf(
			"after removal %d of %d voters would be reachable, short of the %d needed for quorum",
			reachableAfter, votersAfter, quorumAfter))
	}

	return ""
}

// Evidence thresholds. A member has to fail every one of them, independently,
// before it is removed: the raft view, this node's own observation over time,
// discovery, and the peer-consensus health monitor. One signal is a bad reason
// to change a raft configuration — every one of them has a failure mode where
// a healthy node looks dead.
const (
	// deadVoterTicks is how many consecutive reconciler ticks a voter must be
	// unreachable for. At the 2-minute tick this is ~20 minutes, comfortably
	// longer than a reboot, a rolling upgrade step or a WireGuard reconnect.
	deadVoterTicks = 10

	// deadVoterConfirmations is how many DISTINCT peers must have recorded the
	// node as dead for the health monitor to count as corroborating. Two
	// observers rules out one node with a broken route to one peer.
	deadVoterConfirmations = 2

	// deadVoterEvidenceWindow is how recent those observations must be.
	deadVoterEvidenceWindow = 30 * time.Minute

	// tombstoneTTL is how long a tombstone vetoes automatic re-adding.
	//
	// It has to expire, because the node it names is the one node that cannot
	// clear it: an evicted node is outside the raft configuration, so its local
	// rqlite has no leader and it cannot write to the cluster at all. Without a
	// TTL, a node evicted after a long partition is permanently removed with no
	// automatic path back — worse than the problem being solved.
	//
	// It is far longer than defaultInactivityLimit on purpose. A node that is
	// genuinely gone has dropped out of discovery within 2 hours, so by the
	// time its tombstone expires nothing is offering it for re-adding anyway;
	// the TTL only ever helps a node that came back.
	tombstoneTTL = 24 * time.Hour
)

// unreachableStreaks counts consecutive ticks per member. Kept on the
// reconciler rather than derived from raft, because raft has no memory of how
// long a member has been unreachable.
type unreachableStreaks map[string]int

// observe records this tick's reachability and returns the voters whose streak
// has reached deadVoterTicks. Members that answered have their streak cleared,
// and members that have left the configuration are forgotten.
//
// Non-voters are ignored entirely. They do not count toward quorum, so they are
// never evicted; counting them only produced a refusal log line every two
// minutes for the rest of the process's life.
func (s unreachableStreaks) observe(members []raftMember) []string {
	seen := make(map[string]struct{}, len(members))
	var expired []string

	for _, m := range members {
		if !m.Voter {
			delete(s, m.ID)
			continue
		}
		seen[m.ID] = struct{}{}
		if m.Reachable {
			delete(s, m.ID)
			continue
		}
		s[m.ID]++
		if s[m.ID] >= deadVoterTicks {
			expired = append(expired, m.ID)
		}
	}

	for id := range s {
		if _, present := seen[id]; !present {
			delete(s, id)
		}
	}
	return expired
}

// secondsAgoModifier renders d as a SQLite datetime modifier.
//
// The format matters: SQLite takes "-N seconds", and anything it does not
// understand — Go's "1m0s", for instance — makes datetime() return NULL rather
// than error, so the comparison silently matches nothing.
func secondsAgoModifier(d time.Duration) string {
	return fmt.Sprintf("-%d seconds", int(d.Seconds()))
}

// peerIDForRaftAddress maps a raft address to the libp2p peer id the health
// monitor records its observations under.
//
// The two identifier spaces are different and it is not obvious: a raft node id
// in this codebase IS the raft advertise address (10.0.0.4:10101), while
// node_health_events.observer_id and target_id are libp2p peer ids, because the
// monitor reads its node list from dns_nodes where the primary key is
// GetPeerID(). Querying the health table with a raft address matches nothing —
// silently, for ever — which makes a corroboration gate that can never be
// satisfied and an eviction path that never fires.
//
// dns_nodes.internal_ip is the node's WireGuard address, which is exactly the
// host part of its raft address, so the join goes through that.
func peerIDForRaftAddress(ctx context.Context, db *sql.DB, raftAddr string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("no database handle to resolve %s", raftAddr)
	}
	host, _, err := net.SplitHostPort(raftAddr)
	if err != nil {
		return "", fmt.Errorf("raft address %q has no host part: %w", raftAddr, err)
	}

	rows, err := SafeQueryContext(db, ctx,
		`SELECT id FROM dns_nodes WHERE COALESCE(internal_ip, ip_address) = ?`, host)
	if err != nil {
		return "", fmt.Errorf("resolve peer id for %s: %w", host, err)
	}
	defer rows.Close()

	var peerID string
	if rows.Next() {
		if err := rows.Scan(&peerID); err != nil {
			return "", fmt.Errorf("scan peer id for %s: %w", host, err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate peer id for %s: %w", host, err)
	}
	if peerID == "" {
		return "", fmt.Errorf("no dns_nodes row for %s; cannot corroborate its death", host)
	}
	return peerID, nil
}

// confirmedDeadByPeers reports whether the health monitor's peers agree the
// node is dead: at least deadVoterConfirmations distinct observers recorded
// 'dead' within deadVoterEvidenceWindow, with no later 'recovered'.
//
// nodeID here is the libp2p peer id, not the raft address — see
// peerIDForRaftAddress.
//
// This is the one piece of evidence that does not come from this node's own
// view. Without it, a leader that has lost its route to one peer — a WireGuard
// key rotation, a firewall change — would evict a node the rest of the cluster
// can still see perfectly well.
func confirmedDeadByPeers(ctx context.Context, db *sql.DB, nodeID string, window time.Duration) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("no database handle to read node_health_events")
	}

	const q = `
		SELECT COUNT(DISTINCT observer_id)
		FROM node_health_events
		WHERE target_id = ?
		  AND status = 'dead'
		  AND created_at > datetime('now', ?)
		  AND NOT EXISTS (
		      SELECT 1 FROM node_health_events r
		      WHERE r.target_id = node_health_events.target_id
		        AND r.status = 'recovered'
		        AND r.created_at > node_health_events.created_at
		  )`

	rows, err := SafeQueryContext(db, ctx, q, nodeID, secondsAgoModifier(window))
	if err != nil {
		return false, fmt.Errorf("read node_health_events: %w", err)
	}
	defer rows.Close()

	var observers int
	if rows.Next() {
		if err := rows.Scan(&observers); err != nil {
			return false, fmt.Errorf("scan observer count: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate observer count: %w", err)
	}

	return observers >= deadVoterConfirmations, nil
}

// tombstone records that a node was removed on purpose, so the paths that
// re-add absent members leave it alone.
func tombstoneNode(ctx context.Context, db *sql.DB, nodeID, raftAddr, peerID, reason, evictedBy string) error {
	if db == nil {
		return fmt.Errorf("no database handle to write raft_evicted_nodes")
	}
	_, err := SafeExecContext(db, ctx,
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_by)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(node_id) DO UPDATE SET
		    raft_addr = excluded.raft_addr,
		    peer_id = excluded.peer_id,
		    reason = excluded.reason,
		    evicted_by = excluded.evicted_by,
		    evicted_at = CURRENT_TIMESTAMP`,
		nodeID, raftAddr, peerID, reason, evictedBy)
	if err != nil {
		return fmt.Errorf("write tombstone for %s: %w", nodeID, err)
	}
	return nil
}

// clearTombstone removes the veto, so a node that legitimately comes back can
// be re-added automatically again. Called when a node joins explicitly.
func clearTombstone(ctx context.Context, db *sql.DB, nodeID string) error {
	if db == nil {
		return fmt.Errorf("no database handle to clear raft_evicted_nodes")
	}
	if _, err := SafeExecContext(db, ctx, `DELETE FROM raft_evicted_nodes WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("clear tombstone for %s: %w", nodeID, err)
	}
	return nil
}

// tombstonedNodes returns the node ids that must not be re-added automatically.
// Tombstones older than tombstoneTTL are ignored — see the constant.
func tombstonedNodes(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	if db == nil {
		return out, fmt.Errorf("no database handle to read raft_evicted_nodes")
	}

	rows, err := SafeQueryContext(db, ctx,
		`SELECT node_id FROM raft_evicted_nodes WHERE evicted_at > datetime('now', ?)`,
		secondsAgoModifier(tombstoneTTL))
	if err != nil {
		return out, fmt.Errorf("read raft_evicted_nodes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, fmt.Errorf("scan tombstone: %w", err)
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("iterate tombstones: %w", err)
	}
	return out, nil
}

// evictDeadVoters removes voters that are demonstrably gone from the raft
// configuration, so quorum arithmetic stops counting machines that no longer
// exist.
//
// Nothing did this. A VPS deleted without ceremony stayed a configured voter
// forever: with three voters the second such event is permanent quorum loss,
// and `recover-raft` was the only way out. The tenant path already had an
// equivalent (namespace.removeDeadNodeFromRaft); the core cluster had none.
//
// Four independent things must agree before a member is removed, because each
// of them alone has a failure mode where a healthy node looks dead:
//
//   - raft says the member is an unreachable voter,
//   - this node has seen it unreachable on deadVoterTicks consecutive ticks,
//   - discovery no longer knows the peer at all,
//   - and at least deadVoterConfirmations OTHER nodes recorded it dead.
//
// Only the leader evicts, only one member per tick, and only while the term has
// been stable — a configuration change issued during an election can be applied
// against a configuration that is already gone.
// Returns whether it changed the configuration, so the caller can hold to one
// membership change per tick across both passes.
func (r *RQLiteManager) evictDeadVoters(ctx context.Context, reconciler *voterReconciler, nodes RQLiteNodes, termStable bool) (bool, error) {
	members := make([]raftMember, 0, len(nodes))
	addrByID := make(map[string]string, len(nodes))
	for _, n := range nodes {
		members = append(members, raftMember{ID: n.ID, Addr: n.Addr, Voter: n.Voter, Reachable: n.Reachable})
		addrByID[n.ID] = n.Addr
	}

	reconciler.mu.Lock()
	expired := reconciler.unreachable.observe(members)
	reconciler.mu.Unlock()

	if len(expired) == 0 {
		return false, nil
	}

	if !termStable {
		r.logger.Info("Dead voters detected but the raft term is not yet stable; deferring",
			zap.Strings("candidates", expired))
		return false, nil
	}

	for _, candidate := range expired {
		if reconciler.evictionOnCooldown(candidate) {
			continue
		}
		if refusal := safeToEvict(members, candidate); refusal != "" {
			r.logger.Warn("Not evicting an unreachable voter",
				zap.String("node_id", candidate),
				zap.String("reason", string(refusal)))
			continue
		}

		// Both of the checks below are keyed on the raft ADDRESS, not the id.
		// They used to be given the candidate id, which is the same string only
		// on a node that predates stable raft ids: after the migration the
		// discovery lookup could never match and the peer-id resolution failed
		// to split a peer id as host:port, so eviction became a permanent
		// no-op — silently, and exactly on the clusters that had done the
		// safest thing available to them.
		candidateAddr := addrByID[candidate]
		if candidateAddr == "" {
			r.logger.Warn("Not evicting a voter whose raft address is unknown",
				zap.String("node_id", candidate))
			continue
		}

		if r.discoveryService != nil && r.discoveryService.knowsRaftAddress(candidateAddr) {
			r.logger.Debug("Unreachable voter is still known to discovery; not evicting",
				zap.String("node_id", candidate),
				zap.String("raft_addr", candidateAddr))
			continue
		}

		db, err := r.localSQLHandle()
		if err != nil {
			return false, fmt.Errorf("eviction needs a database handle: %w", err)
		}
		peerID, err := peerIDForRaftAddress(ctx, db, candidateAddr)
		if err != nil {
			r.logger.Debug("Cannot resolve the peer id for an unreachable voter; not evicting",
				zap.String("node_id", candidate), zap.Error(err))
			reconciler.holdOffEviction(candidate)
			continue
		}
		confirmed, err := confirmedDeadByPeers(ctx, db, peerID, deadVoterEvidenceWindow)
		if err != nil {
			return false, fmt.Errorf("corroborate %s: %w", candidate, err)
		}
		if !confirmed {
			r.logger.Info("Unreachable voter is not corroborated dead by peers; not evicting",
				zap.String("node_id", candidate),
				zap.String("peer_id", peerID),
				zap.Int("observers_required", deadVoterConfirmations))
			reconciler.holdOffEviction(candidate)
			continue
		}

		// Tombstone BEFORE removing. If the removal succeeds and the tombstone
		// does not, orphan recovery re-adds the node within five minutes and
		// the eviction was pointless; the other order merely leaves a tombstone
		// for a node still in the configuration, which is inert.
		if err := tombstoneNode(ctx, db, candidate, candidateAddr, peerID, "dead-voter", r.discoverConfig.RaftAdvAddress); err != nil {
			return false, fmt.Errorf("tombstone %s: %w", candidate, err)
		}

		if err := r.removeClusterNode(candidate); err != nil {
			// The tombstone stays. It is inert while the node is still in the
			// configuration, and it means the next attempt is not fighting
			// orphan recovery for the same node.
			reconciler.holdOffEviction(candidate)
			return false, fmt.Errorf("remove dead voter %s: %w", candidate, err)
		}

		reconciler.mu.Lock()
		delete(reconciler.unreachable, candidate)
		reconciler.mu.Unlock()

		r.logger.Warn("Evicted a dead voter from the raft configuration",
			zap.String("node_id", candidate),
			zap.Int("unreachable_ticks", deadVoterTicks))
		return true, nil
	}

	return false, nil
}

// localSQLHandle returns a database/sql handle on this node's rqlite.
func (r *RQLiteManager) localSQLHandle() (*sql.DB, error) {
	r.sqlOnce.Do(func() {
		r.sqlDB, r.sqlErr = sql.Open("rqlite",
			fmt.Sprintf("http://localhost:%d?disableClusterDiscovery=true", r.config.RQLitePort))
	})
	return r.sqlDB, r.sqlErr
}
