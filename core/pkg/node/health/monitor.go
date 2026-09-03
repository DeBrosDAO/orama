// Package health provides peer-to-peer node failure detection using a
// ring-based monitoring topology. Each node probes a small, deterministic
// subset of peers (the next K nodes in a sorted ring) so total probe
// traffic is O(N) instead of O(N²).
package health

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Default tuning constants.
const (
	DefaultProbeInterval = 10 * time.Second
	DefaultProbeTimeout  = 3 * time.Second
	DefaultNeighbors     = 3
	DefaultSuspectAfter  = 3  // consecutive misses → suspect
	DefaultDeadAfter     = 12 // consecutive misses → dead
	DefaultQuorumWindow  = 5 * time.Minute
	DefaultMinQuorum     = 2 // out of K observers must agree

	// DefaultStartupGracePeriod prevents false dead declarations after
	// cluster-wide restart. During this period, no nodes are declared dead.
	DefaultStartupGracePeriod = 5 * time.Minute

	// ringMembershipWindow is how stale dns_nodes.last_seen may be before a peer
	// drops out of every ring. A peer outside every ring cannot be probed and so
	// can never reach the dead threshold.
	ringMembershipWindow = 2 * time.Minute

	// freshHeartbeatWindow is how recent a peer's heartbeat must be to stand in
	// for an HTTP probe. The heartbeat ticks every 30s
	// (pkg/node/dns_registration.go), so this is two ticks: long enough that a
	// single missed tick does not force a probe, short enough that a node which
	// stopped heartbeating is probed on the next round.
	freshHeartbeatWindow = 65 * time.Second

	// sqlTimeLayout matches rqlite's datetime('now'), which is always UTC.
	sqlTimeLayout = "2006-01-02 15:04:05"
)

// MetadataReader provides lifecycle metadata for peers. Implemented by
// ClusterDiscoveryService. The health monitor uses this to check maintenance
// status and LastSeen before falling through to HTTP probes.
type MetadataReader interface {
	GetPeerLifecycleState(nodeID string) (state string, lastSeen time.Time, found bool)
}

// Config holds the configuration for a Monitor.
type Config struct {
	NodeID        string  // this node's ID (dns_nodes.id / peer ID)
	DB            *sql.DB // RQLite SQL connection
	Logger        *zap.Logger
	ProbeInterval time.Duration // how often to probe (default 10s)
	ProbeTimeout  time.Duration // per-probe HTTP timeout (default 3s)
	Neighbors     int           // K — how many ring neighbors to monitor (default 3)

	// MetadataReader provides LibP2P lifecycle metadata for peers.
	// When set, the monitor checks peer maintenance state and LastSeen
	// before falling through to HTTP probes.
	MetadataReader MetadataReader

	// ProbePort is the TCP port carrying /v1/internal/ping on a peer. It is the
	// INDEX gateway's port: the ring monitors nodes, not namespaces, and only
	// the index gateway serves the internal ping on every node.
	//
	// It is a field rather than a constant because the port was hardcoded once
	// before, survived a port migration untouched, and turned the failure
	// detector into a fleet-wide false-eviction cascade — every probe failed on
	// a healthy cluster. A caller that forgets it now gets a loud zero-port
	// failure at construction instead of silent nonsense at runtime.
	ProbePort int

	// StartupGracePeriod prevents false dead declarations after cluster-wide
	// restart. During this period, nodes can be marked suspect but never dead.
	// Default: 5 minutes.
	StartupGracePeriod time.Duration
}

// nodeInfo is a row from dns_nodes used for probing.
type nodeInfo struct {
	ID         string
	InternalIP string // WireGuard IP (or public IP fallback)

	// HeartbeatFresh is true when this peer updated dns_nodes.last_seen within
	// freshHeartbeatWindow. Writing that row is a raft write, so it is proof
	// the peer was alive and had quorum — evidence that does not depend on the
	// HTTP path being reachable, computed in the same query that builds the
	// ring rather than in a second round trip.
	HeartbeatFresh bool
}

// peerState tracks the in-memory health state for a single monitored peer.
type peerState struct {
	missCount    int
	status       string    // "healthy", "suspect", "dead"
	suspectAt    time.Time // when first moved to suspect
	reportedDead bool      // whether we already wrote a "dead" event for this round
}

// Monitor implements ring-based failure detection.
type Monitor struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
	startTime  time.Time // when the monitor was created

	mu    sync.Mutex
	peers map[string]*peerState // nodeID → state

	onDeadFn      func(nodeID string) // callback when quorum confirms death
	onRecoveredFn func(nodeID string) // callback when node transitions from suspect/dead → healthy
	onSuspectFn   func(nodeID string) // callback when node transitions healthy → suspect
}

// NewMonitor creates a new health monitor.
//
// It returns an error rather than defaulting a missing ProbePort: the port was
// hardcoded once, survived a port migration untouched, and turned the failure
// detector into a fleet-wide false-eviction cascade. A silent default would
// reintroduce exactly that failure mode the next time the port moves.
func NewMonitor(cfg Config) (*Monitor, error) {
	if cfg.ProbeInterval == 0 {
		cfg.ProbeInterval = DefaultProbeInterval
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = DefaultProbeTimeout
	}
	if cfg.Neighbors == 0 {
		cfg.Neighbors = DefaultNeighbors
	}
	if cfg.StartupGracePeriod == 0 {
		cfg.StartupGracePeriod = DefaultStartupGracePeriod
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	if cfg.ProbePort <= 0 {
		return nil, fmt.Errorf("health.NewMonitor: ProbePort must be the index gateway port, got %d", cfg.ProbePort)
	}

	return &Monitor{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.ProbeTimeout,
		},
		logger:    cfg.Logger.With(zap.String("component", "health-monitor")),
		startTime: time.Now(),
		peers:     make(map[string]*peerState),
	}, nil
}

// OnNodeDead registers a callback invoked when a node is confirmed dead by
// quorum. The callback runs with the monitor lock released.
func (m *Monitor) OnNodeDead(fn func(nodeID string)) {
	m.onDeadFn = fn
}

// OnNodeRecovered registers a callback invoked when a previously suspect or dead
// node transitions back to healthy. The callback runs with the monitor lock released.
func (m *Monitor) OnNodeRecovered(fn func(nodeID string)) {
	m.onRecoveredFn = fn
}

// OnNodeSuspect registers a callback invoked when a node transitions from
// healthy to suspect (3 consecutive missed probes). The callback runs with
// the monitor lock released.
func (m *Monitor) OnNodeSuspect(fn func(nodeID string)) {
	m.onSuspectFn = fn
}

// Start runs the monitor loop until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	m.logger.Info("Starting node health monitor",
		zap.String("node_id", m.cfg.NodeID),
		zap.Duration("probe_interval", m.cfg.ProbeInterval),
		zap.Int("neighbors", m.cfg.Neighbors),
		zap.Duration("startup_grace", m.cfg.StartupGracePeriod),
	)

	ticker := time.NewTicker(m.cfg.ProbeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Health monitor stopped")
			return
		case <-ticker.C:
			m.probeRound(ctx)
		}
	}
}

// isInStartupGrace returns true if the startup grace period is still active.
func (m *Monitor) isInStartupGrace() bool {
	return time.Since(m.startTime) < m.cfg.StartupGracePeriod
}

// probeRound runs a single round of probing our ring neighbors.
func (m *Monitor) probeRound(ctx context.Context) {
	neighbors, err := m.getRingNeighbors(ctx)
	if err != nil {
		m.logger.Warn("Failed to get ring neighbors", zap.Error(err))
		return
	}
	if len(neighbors) == 0 {
		return
	}

	// Probe each neighbor concurrently
	var wg sync.WaitGroup
	for _, n := range neighbors {
		wg.Add(1)
		go func(node nodeInfo) {
			defer wg.Done()
			ok := m.probeNode(ctx, node)
			m.updateState(ctx, node.ID, ok)
		}(n)
	}
	wg.Wait()

	// Clean up state for nodes no longer in our neighbor set
	m.pruneStaleState(neighbors)
}

// probeNode checks a node's health. It first checks LibP2P metadata (if
// available) to avoid unnecessary HTTP probes, then falls through to HTTP.
func (m *Monitor) probeNode(ctx context.Context, node nodeInfo) bool {
	// A peer that wrote its heartbeat within the last freshHeartbeatWindow was
	// demonstrably alive and able to reach quorum. Believing that over an HTTP
	// probe means a node is never declared dead because one port was wrong, one
	// request was dropped, or its gateway was mid-restart while the node itself
	// was fine. A stale heartbeat proves nothing either way, so it falls through
	// to the probe rather than counting as a miss.
	if node.HeartbeatFresh {
		return true
	}

	if m.cfg.MetadataReader != nil {
		state, lastSeen, found := m.cfg.MetadataReader.GetPeerLifecycleState(node.ID)
		if found {
			// Maintenance node with recent LastSeen → count as healthy
			if state == "maintenance" && time.Since(lastSeen) < 2*time.Minute {
				return true
			}

			// Recently seen active node → count as healthy (no HTTP needed).
			// "degraded" is deliberately not short-circuited here: such a node
			// claims to be serving from its local replica, and the probe below
			// is what verifies the claim.
			if state == "active" && time.Since(lastSeen) < 30*time.Second {
				return true
			}
		}
	}

	// Fall through to HTTP probe
	return m.probe(ctx, node)
}

// probe sends an HTTP ping to a single node. Returns true if healthy.
func (m *Monitor) probe(ctx context.Context, node nodeInfo) bool {
	url := fmt.Sprintf("http://%s/v1/internal/ping",
		net.JoinHostPort(node.InternalIP, strconv.Itoa(m.cfg.ProbePort)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// updateState updates the in-memory state for a peer after a probe.
// Callbacks are invoked with the lock released to prevent deadlocks (C2 fix).
func (m *Monitor) updateState(ctx context.Context, nodeID string, healthy bool) {
	m.mu.Lock()

	ps, exists := m.peers[nodeID]
	if !exists {
		ps = &peerState{status: "healthy"}
		m.peers[nodeID] = ps
	}

	if healthy {
		wasUnhealthy := ps.status == "suspect" || ps.status == "dead"
		shouldCallback := wasUnhealthy && m.onRecoveredFn != nil
		prevStatus := ps.status

		// Update state BEFORE releasing lock (C2 fix)
		ps.missCount = 0
		ps.status = "healthy"
		ps.reportedDead = false
		m.mu.Unlock()

		if prevStatus != "healthy" {
			m.logger.Info("Node recovered", zap.String("target", nodeID),
				zap.String("previous_status", prevStatus))
			m.writeEvent(ctx, nodeID, "recovered")
		}

		// Fire recovery callback without holding the lock (C2 fix)
		if shouldCallback {
			m.onRecoveredFn(nodeID)
		}
		return
	}

	// Miss
	ps.missCount++

	switch {
	case ps.missCount >= DefaultDeadAfter && !ps.reportedDead:
		// During startup grace period, don't declare dead — only suspect
		if m.isInStartupGrace() {
			if ps.status != "suspect" {
				ps.status = "suspect"
				ps.suspectAt = time.Now()
				m.mu.Unlock()
				m.logger.Warn("Node SUSPECT (startup grace — deferring dead)",
					zap.String("target", nodeID),
					zap.Int("misses", ps.missCount),
				)
				m.writeEvent(ctx, nodeID, "suspect")
				return
			}
			m.mu.Unlock()
			return
		}

		if ps.status != "dead" {
			m.logger.Error("Node declared DEAD",
				zap.String("target", nodeID),
				zap.Int("misses", ps.missCount),
			)
		}
		ps.status = "dead"
		ps.reportedDead = true

		// Copy what we need before releasing lock
		shouldCheckQuorum := m.cfg.DB != nil && m.onDeadFn != nil
		m.mu.Unlock()

		m.writeEvent(ctx, nodeID, "dead")
		if shouldCheckQuorum {
			m.checkQuorum(ctx, nodeID)
		}
		return

	case ps.missCount >= DefaultSuspectAfter && ps.status == "healthy":
		ps.status = "suspect"
		ps.suspectAt = time.Now()
		shouldCallSuspect := m.onSuspectFn != nil
		m.mu.Unlock()

		m.logger.Warn("Node SUSPECT",
			zap.String("target", nodeID),
			zap.Int("misses", ps.missCount),
		)
		m.writeEvent(ctx, nodeID, "suspect")

		if shouldCallSuspect {
			m.onSuspectFn(nodeID)
		}
		return
	}

	m.mu.Unlock()
}

// writeEvent inserts a health event into node_health_events.
func (m *Monitor) writeEvent(ctx context.Context, targetID, status string) {
	if m.cfg.DB == nil {
		return
	}
	query := `INSERT INTO node_health_events (observer_id, target_id, status) VALUES (?, ?, ?)`
	if _, err := m.cfg.DB.ExecContext(ctx, query, m.cfg.NodeID, targetID, status); err != nil {
		m.logger.Warn("Failed to write health event",
			zap.String("target", targetID),
			zap.String("status", status),
			zap.Error(err),
		)
	}
}

// checkQuorum queries the events table to see if enough observers agree the
// target is dead, then fires the onDead callback. Called WITHOUT the lock held
// (C2 fix — previously called with lock held, causing deadlocks in callbacks).
func (m *Monitor) checkQuorum(ctx context.Context, targetID string) {
	if m.cfg.DB == nil || m.onDeadFn == nil {
		return
	}

	// Bugboard #282: compare in UTC — created_at is stored UTC.
	cutoff := time.Now().UTC().Add(-DefaultQuorumWindow).Format("2006-01-02 15:04:05")
	query := `SELECT COUNT(DISTINCT observer_id) FROM node_health_events WHERE target_id = ? AND status = 'dead' AND created_at > ?`

	var count int
	if err := m.cfg.DB.QueryRowContext(ctx, query, targetID, cutoff).Scan(&count); err != nil {
		m.logger.Warn("Failed to check quorum", zap.String("target", targetID), zap.Error(err))
		return
	}

	if count < DefaultMinQuorum {
		m.logger.Info("Dead event recorded, waiting for quorum",
			zap.String("target", targetID),
			zap.Int("observers", count),
			zap.Int("required", DefaultMinQuorum),
		)
		return
	}

	// Quorum reached. Only the lowest-ID observer triggers recovery to
	// prevent duplicate actions.
	var lowestObserver string
	lowestQuery := `SELECT MIN(observer_id) FROM node_health_events WHERE target_id = ? AND status = 'dead' AND created_at > ?`
	if err := m.cfg.DB.QueryRowContext(ctx, lowestQuery, targetID, cutoff).Scan(&lowestObserver); err != nil {
		m.logger.Warn("Failed to determine lowest observer", zap.Error(err))
		return
	}

	if lowestObserver != m.cfg.NodeID {
		m.logger.Info("Quorum reached but another node is responsible for recovery",
			zap.String("target", targetID),
			zap.String("responsible", lowestObserver),
		)
		return
	}

	m.logger.Error("CONFIRMED DEAD — triggering recovery",
		zap.String("target", targetID),
		zap.Int("observers", count),
	)
	m.onDeadFn(targetID)
}

// getRingNeighbors queries dns_nodes for active nodes, sorts them, and
// returns the K nodes after this node in the ring.
func (m *Monitor) getRingNeighbors(ctx context.Context) ([]nodeInfo, error) {
	if m.cfg.DB == nil {
		return nil, fmt.Errorf("database not available")
	}

	// Bugboard #282: compare in UTC — last_seen is stored UTC.
	cutoff := time.Now().UTC().Add(-2 * time.Minute).Format("2006-01-02 15:04:05")
	query := `SELECT id, COALESCE(internal_ip, ip_address) AS internal_ip FROM dns_nodes WHERE status = 'active' AND last_seen > ? ORDER BY id`

	rows, err := m.cfg.DB.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query dns_nodes: %w", err)
	}
	defer rows.Close()

	var nodes []nodeInfo
	for rows.Next() {
		var n nodeInfo
		if err := rows.Scan(&n.ID, &n.InternalIP, &n.HeartbeatFresh); err != nil {
			return nil, fmt.Errorf("scan dns_nodes: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows dns_nodes: %w", err)
	}

	return RingNeighbors(nodes, m.cfg.NodeID, m.cfg.Neighbors), nil
}

// RingNeighbors returns the K nodes after selfID in a sorted ring of nodes.
// Exported for testing.
func RingNeighbors(nodes []nodeInfo, selfID string, k int) []nodeInfo {
	if len(nodes) <= 1 {
		return nil
	}

	// Ensure sorted
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	// Find self
	selfIdx := -1
	for i, n := range nodes {
		if n.ID == selfID {
			selfIdx = i
			break
		}
	}
	if selfIdx == -1 {
		// We're not in the ring (e.g., not yet registered). Monitor nothing.
		return nil
	}

	// Collect next K nodes (wrapping)
	count := k
	if count > len(nodes)-1 {
		count = len(nodes) - 1 // can't monitor more peers than exist (excluding self)
	}

	neighbors := make([]nodeInfo, 0, count)
	for i := 1; i <= count; i++ {
		idx := (selfIdx + i) % len(nodes)
		neighbors = append(neighbors, nodes[idx])
	}
	return neighbors
}

// pruneStaleState removes in-memory state for nodes that are no longer our
// ring neighbors (e.g., they left the cluster or our position changed).
func (m *Monitor) pruneStaleState(currentNeighbors []nodeInfo) {
	active := make(map[string]bool, len(currentNeighbors))
	for _, n := range currentNeighbors {
		active[n.ID] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.peers {
		if !active[id] {
			delete(m.peers, id)
		}
	}
}
