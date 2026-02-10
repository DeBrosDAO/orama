// Package health provides peer-to-peer node failure detection using a
// ring-based monitoring topology. Each node probes a small, deterministic
// subset of peers (the next K nodes in a sorted ring) so total probe
// traffic is O(N) instead of O(N²).
package health

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Default tuning constants.
const (
	DefaultProbeInterval  = 10 * time.Second
	DefaultProbeTimeout   = 3 * time.Second
	DefaultNeighbors      = 3
	DefaultSuspectAfter   = 3  // consecutive misses → suspect
	DefaultDeadAfter      = 12 // consecutive misses → dead
	DefaultQuorumWindow   = 5 * time.Minute
	DefaultMinQuorum      = 2  // out of K observers must agree
)

// Config holds the configuration for a Monitor.
type Config struct {
	NodeID        string        // this node's ID (dns_nodes.id / peer ID)
	DB            *sql.DB       // RQLite SQL connection
	Logger        *zap.Logger
	ProbeInterval time.Duration // how often to probe (default 10s)
	ProbeTimeout  time.Duration // per-probe HTTP timeout (default 3s)
	Neighbors     int           // K — how many ring neighbors to monitor (default 3)
}

// nodeInfo is a row from dns_nodes used for probing.
type nodeInfo struct {
	ID         string
	InternalIP string // WireGuard IP (or public IP fallback)
}

// peerState tracks the in-memory health state for a single monitored peer.
type peerState struct {
	missCount int
	status    string    // "healthy", "suspect", "dead"
	suspectAt time.Time // when first moved to suspect
	reportedDead bool   // whether we already wrote a "dead" event for this round
}

// Monitor implements ring-based failure detection.
type Monitor struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger

	mu    sync.Mutex
	peers map[string]*peerState // nodeID → state

	onDeadFn func(nodeID string) // callback when quorum confirms death
}

// NewMonitor creates a new health monitor.
func NewMonitor(cfg Config) *Monitor {
	if cfg.ProbeInterval == 0 {
		cfg.ProbeInterval = DefaultProbeInterval
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = DefaultProbeTimeout
	}
	if cfg.Neighbors == 0 {
		cfg.Neighbors = DefaultNeighbors
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}

	return &Monitor{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.ProbeTimeout,
		},
		logger: cfg.Logger.With(zap.String("component", "health-monitor")),
		peers:  make(map[string]*peerState),
	}
}

// OnNodeDead registers a callback invoked when a node is confirmed dead by
// quorum. The callback runs synchronously in the monitor goroutine.
func (m *Monitor) OnNodeDead(fn func(nodeID string)) {
	m.onDeadFn = fn
}

// Start runs the monitor loop until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) {
	m.logger.Info("Starting node health monitor",
		zap.String("node_id", m.cfg.NodeID),
		zap.Duration("probe_interval", m.cfg.ProbeInterval),
		zap.Int("neighbors", m.cfg.Neighbors),
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
			ok := m.probe(ctx, node)
			m.updateState(ctx, node.ID, ok)
		}(n)
	}
	wg.Wait()

	// Clean up state for nodes no longer in our neighbor set
	m.pruneStaleState(neighbors)
}

// probe sends an HTTP ping to a single node. Returns true if healthy.
func (m *Monitor) probe(ctx context.Context, node nodeInfo) bool {
	url := fmt.Sprintf("http://%s:6001/v1/internal/ping", node.InternalIP)
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
func (m *Monitor) updateState(ctx context.Context, nodeID string, healthy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ps, exists := m.peers[nodeID]
	if !exists {
		ps = &peerState{status: "healthy"}
		m.peers[nodeID] = ps
	}

	if healthy {
		// Recovered
		if ps.status != "healthy" {
			m.logger.Info("Node recovered", zap.String("target", nodeID))
			m.writeEvent(ctx, nodeID, "recovered")
		}
		ps.missCount = 0
		ps.status = "healthy"
		ps.reportedDead = false
		return
	}

	// Miss
	ps.missCount++

	switch {
	case ps.missCount >= DefaultDeadAfter && !ps.reportedDead:
		if ps.status != "dead" {
			m.logger.Error("Node declared DEAD",
				zap.String("target", nodeID),
				zap.Int("misses", ps.missCount),
			)
		}
		ps.status = "dead"
		ps.reportedDead = true
		m.writeEvent(ctx, nodeID, "dead")
		m.checkQuorum(ctx, nodeID)

	case ps.missCount >= DefaultSuspectAfter && ps.status == "healthy":
		ps.status = "suspect"
		ps.suspectAt = time.Now()
		m.logger.Warn("Node SUSPECT",
			zap.String("target", nodeID),
			zap.Int("misses", ps.missCount),
		)
		m.writeEvent(ctx, nodeID, "suspect")
	}
}

// writeEvent inserts a health event into node_health_events. Must be called
// with m.mu held.
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
// target is dead, then fires the onDead callback. Must be called with m.mu held.
func (m *Monitor) checkQuorum(ctx context.Context, targetID string) {
	if m.cfg.DB == nil || m.onDeadFn == nil {
		return
	}

	cutoff := time.Now().Add(-DefaultQuorumWindow).Format("2006-01-02 15:04:05")
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
	// Release the lock before calling the callback to avoid deadlocks.
	m.mu.Unlock()
	m.onDeadFn(targetID)
	m.mu.Lock()
}

// getRingNeighbors queries dns_nodes for active nodes, sorts them, and
// returns the K nodes after this node in the ring.
func (m *Monitor) getRingNeighbors(ctx context.Context) ([]nodeInfo, error) {
	if m.cfg.DB == nil {
		return nil, fmt.Errorf("database not available")
	}

	cutoff := time.Now().Add(-2 * time.Minute).Format("2006-01-02 15:04:05")
	query := `SELECT id, COALESCE(internal_ip, ip_address) AS internal_ip FROM dns_nodes WHERE status = 'active' AND last_seen > ? ORDER BY id`

	rows, err := m.cfg.DB.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query dns_nodes: %w", err)
	}
	defer rows.Close()

	var nodes []nodeInfo
	for rows.Next() {
		var n nodeInfo
		if err := rows.Scan(&n.ID, &n.InternalIP); err != nil {
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
