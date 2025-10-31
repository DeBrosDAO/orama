package rqlite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// RQLiteManager manages an RQLite node instance
type RQLiteManager struct {
	config           *config.DatabaseConfig
	discoverConfig   *config.DiscoveryConfig
	dataDir          string
	logger           *zap.Logger
	cmd              *exec.Cmd
	connection       *gorqlite.Connection
	discoveryService *ClusterDiscoveryService
}

// waitForSQLAvailable waits until a simple query succeeds, indicating a leader is known and queries can be served.
func (r *RQLiteManager) waitForSQLAvailable(ctx context.Context) error {
	if r.connection == nil {
		r.logger.Error("No rqlite connection")
		return errors.New("no rqlite connection")
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			attempts++
			_, err := r.connection.QueryOne("SELECT 1")
			if err == nil {
				r.logger.Info("RQLite SQL is available")
				return nil
			}
			if attempts%5 == 0 { // log every ~5s to reduce noise
				r.logger.Debug("Waiting for RQLite SQL availability", zap.Error(err))
			}
		}
	}
}

// NewRQLiteManager creates a new RQLite manager
func NewRQLiteManager(cfg *config.DatabaseConfig, discoveryCfg *config.DiscoveryConfig, dataDir string, logger *zap.Logger) *RQLiteManager {
	return &RQLiteManager{
		config:         cfg,
		discoverConfig: discoveryCfg,
		dataDir:        dataDir,
		logger:         logger,
	}
}

// SetDiscoveryService sets the cluster discovery service for this RQLite manager
func (r *RQLiteManager) SetDiscoveryService(service *ClusterDiscoveryService) {
	r.discoveryService = service
}

// Start starts the RQLite node
func (r *RQLiteManager) Start(ctx context.Context) error {
	// Expand ~ in data directory path
	dataDir := os.ExpandEnv(r.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Create data directory
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	if err := os.MkdirAll(rqliteDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	if r.discoverConfig.HttpAdvAddress == "" {
		return fmt.Errorf("discovery config HttpAdvAddress is empty")
	}

	// CRITICAL: Check if we need to do pre-start cluster discovery to build peers.json
	// This handles the case where nodes have old cluster state and need coordinated recovery
	if needsClusterRecovery, err := r.checkNeedsClusterRecovery(rqliteDataDir); err != nil {
		return fmt.Errorf("failed to check cluster recovery status: %w", err)
	} else if needsClusterRecovery {
		r.logger.Info("Detected old cluster state requiring coordinated recovery")
		if err := r.performPreStartClusterDiscovery(ctx, rqliteDataDir); err != nil {
			return fmt.Errorf("pre-start cluster discovery failed: %w", err)
		}
	}

	// Build RQLite command
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLitePort),
		"-http-adv-addr", r.discoverConfig.HttpAdvAddress,
		"-raft-adv-addr", r.discoverConfig.RaftAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLiteRaftPort),
	}

	// Add join address if specified (for non-bootstrap or secondary bootstrap nodes)
	if r.config.RQLiteJoinAddress != "" {
		r.logger.Info("Joining RQLite cluster", zap.String("join_address", r.config.RQLiteJoinAddress))

		// Normalize join address to host:port for rqlited -join
		joinArg := r.config.RQLiteJoinAddress
		if strings.HasPrefix(joinArg, "http://") {
			joinArg = strings.TrimPrefix(joinArg, "http://")
		} else if strings.HasPrefix(joinArg, "https://") {
			joinArg = strings.TrimPrefix(joinArg, "https://")
		}

		// Wait for join target to become reachable to avoid forming a separate cluster (wait indefinitely)
		if err := r.waitForJoinTarget(ctx, r.config.RQLiteJoinAddress, 0); err != nil {
			r.logger.Warn("Join target did not become reachable within timeout; will still attempt to join",
				zap.String("join_address", r.config.RQLiteJoinAddress),
				zap.Error(err))
		}

		// Always add the join parameter in host:port form - let rqlited handle the rest
		// Add retry parameters to handle slow cluster startup (e.g., during recovery)
		args = append(args, "-join", joinArg, "-join-attempts", "30", "-join-interval", "10s")
	} else {
		r.logger.Info("No join address specified - starting as new cluster")
	}

	// Add data directory as positional argument
	args = append(args, rqliteDataDir)

	r.logger.Info("Starting RQLite node",
		zap.String("data_dir", rqliteDataDir),
		zap.Int("http_port", r.config.RQLitePort),
		zap.Int("raft_port", r.config.RQLiteRaftPort),
		zap.String("join_address", r.config.RQLiteJoinAddress),
		zap.Strings("full_args", args),
	)

	// Start RQLite process (not bound to ctx for graceful Stop handling)
	r.cmd = exec.Command("rqlited", args...)

	// Enable debug logging of RQLite process to help diagnose issues
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Wait for RQLite to be ready
	if err := r.waitForReady(ctx); err != nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("RQLite failed to become ready: %w", err)
	}

	// Create connection
	conn, err := gorqlite.Open(fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
	if err != nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("failed to connect to RQLite: %w", err)
	}
	r.connection = conn

	// Sanity check: verify rqlite's node ID matches our configured raft address
	if err := r.validateNodeID(); err != nil {
		r.logger.Warn("Node ID validation failed", zap.Error(err))
		// Don't fail startup, but log the mismatch for debugging
	}

	// Leadership/SQL readiness gating with dynamic discovery support
	if r.config.RQLiteJoinAddress == "" {
		// Bootstrap node logic with data safety checks
		r.logger.Info("Bootstrap node: checking if safe to lead")

		// SAFETY: Check if we can safely become leader
		canLead, err := r.canSafelyBecomeLeader()
		if !canLead && err != nil {
			r.logger.Warn("Not safe to become leader, attempting to join existing cluster",
				zap.Error(err))

			// Find node with highest log index and join it
			if r.discoveryService != nil {
				targetNode := r.discoveryService.GetNodeWithHighestLogIndex()
				if targetNode != nil {
					r.logger.Info("Joining node with higher data",
						zap.String("target_node", targetNode.NodeID),
						zap.String("raft_address", targetNode.RaftAddress),
						zap.Uint64("their_index", targetNode.RaftLogIndex))
					return r.joinExistingCluster(ctx, targetNode.RaftAddress)
				}
			}
		}

		// Safe to lead - attempt leadership
		leadershipErr := r.waitForLeadership(ctx)
		if leadershipErr == nil {
			r.logger.Info("Bootstrap node successfully established leadership")
		} else {
			r.logger.Warn("Initial leadership attempt failed, may need cluster recovery",
				zap.Error(leadershipErr))

			// Try recovery if we have peers.json from discovery
			if r.discoveryService != nil {
				peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
				if _, err := os.Stat(peersPath); err == nil {
					r.logger.Info("Attempting cluster recovery using peers.json",
						zap.String("peers_file", peersPath))

					if recoveryErr := r.recoverCluster(peersPath); recoveryErr == nil {
						r.logger.Info("Cluster recovery successful, retrying leadership")
						leadershipErr = r.waitForLeadership(ctx)
						if leadershipErr == nil {
							r.logger.Info("Bootstrap node established leadership after recovery")
						}
					} else {
						r.logger.Warn("Cluster recovery failed", zap.Error(recoveryErr))
					}
				}
			}

			// Final fallback: SQL availability
			if leadershipErr != nil {
				r.logger.Warn("Leadership failed, trying SQL availability")
				sqlCtx := ctx
				if _, hasDeadline := ctx.Deadline(); !hasDeadline {
					var cancel context.CancelFunc
					sqlCtx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
				}
				if err := r.waitForSQLAvailable(sqlCtx); err != nil {
					if r.cmd != nil && r.cmd.Process != nil {
						_ = r.cmd.Process.Kill()
					}
					return fmt.Errorf("RQLite SQL not available: %w", err)
				}
			}
		}
	} else {
		// Joining node logic
		r.logger.Info("Waiting for RQLite SQL availability (leader discovery)")
		sqlCtx := ctx
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			sqlCtx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
		}
		if err := r.waitForSQLAvailable(sqlCtx); err != nil {
			if r.cmd != nil && r.cmd.Process != nil {
				_ = r.cmd.Process.Kill()
			}
			return fmt.Errorf("RQLite SQL not available: %w", err)
		}
	}

	// After waitForLeadership / waitForSQLAvailable succeeds, before returning:
	migrationsDir := "migrations"

	if err := r.ApplyMigrations(ctx, migrationsDir); err != nil {
		r.logger.Error("Migrations failed", zap.Error(err), zap.String("dir", migrationsDir))
		return fmt.Errorf("apply migrations: %w", err)
	}

	r.logger.Info("RQLite node started successfully")
	return nil
}

// hasExistingState returns true if the rqlite data directory already contains files or subdirectories.
func (r *RQLiteManager) hasExistingState(rqliteDataDir string) bool {
	entries, err := os.ReadDir(rqliteDataDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		// Any existing file or directory indicates prior state
		if e.Name() == "." || e.Name() == ".." {
			continue
		}
		return true
	}
	return false
}

// waitForReady waits for RQLite to be ready to accept connections
func (r *RQLiteManager) waitForReady(ctx context.Context) error {
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := &http.Client{Timeout: 2 * time.Second}

	// Give joining nodes more time (120 seconds vs 30)
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("RQLite did not become ready within timeout")
}

// waitForLeadership waits for RQLite to establish leadership (for bootstrap nodes)
func (r *RQLiteManager) waitForLeadership(ctx context.Context) error {
	r.logger.Info("Waiting for RQLite to establish leadership...")

	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try a simple query to check if leadership is established
		if r.connection != nil {
			_, err := r.connection.QueryOne("SELECT 1")
			if err == nil {
				r.logger.Info("RQLite leadership established")
				return nil
			}
			r.logger.Debug("Waiting for leadership", zap.Error(err))
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("RQLite failed to establish leadership within timeout")
}

// GetConnection returns the RQLite connection
func (r *RQLiteManager) GetConnection() *gorqlite.Connection {
	return r.connection
}

// Stop stops the RQLite node
func (r *RQLiteManager) Stop() error {
	if r.connection != nil {
		r.connection.Close()
		r.connection = nil
	}

	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}

	r.logger.Info("Stopping RQLite node (graceful)")
	// Try SIGTERM first
	if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Fallback to Kill if signaling fails
		_ = r.cmd.Process.Kill()
		return nil
	}

	// Wait up to 5 seconds for graceful shutdown
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrClosed) {
			r.logger.Warn("RQLite process exited with error", zap.Error(err))
		}
	case <-time.After(5 * time.Second):
		r.logger.Warn("RQLite did not exit in time; killing")
		_ = r.cmd.Process.Kill()
	}

	return nil
}

// waitForJoinTarget waits until the join target's HTTP status becomes reachable, or until timeout
func (r *RQLiteManager) waitForJoinTarget(ctx context.Context, joinAddress string, timeout time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var lastErr error

	for {
		if err := r.testJoinAddress(joinAddress); err == nil {
			r.logger.Info("Join target is reachable, proceeding with cluster join")
			return nil
		} else {
			lastErr = err
			r.logger.Debug("Join target not yet reachable; waiting...", zap.String("join_address", joinAddress), zap.Error(err))
		}

		// Check context
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
	}

	return lastErr
}

// testJoinAddress tests if a join address is reachable
func (r *RQLiteManager) testJoinAddress(joinAddress string) error {
	// Determine the HTTP status URL to probe.
	// If joinAddress contains a scheme, use it directly. Otherwise treat joinAddress
	// as host:port (Raft) and probe the standard HTTP API port 5001 on that host.
	client := &http.Client{Timeout: 5 * time.Second}

	var statusURL string
	if strings.HasPrefix(joinAddress, "http://") || strings.HasPrefix(joinAddress, "https://") {
		statusURL = strings.TrimRight(joinAddress, "/") + "/status"
	} else {
		// Extract host from host:port
		host := joinAddress
		if idx := strings.Index(joinAddress, ":"); idx != -1 {
			host = joinAddress[:idx]
		}
		statusURL = fmt.Sprintf("http://%s:%d/status", host, 5001)
	}

	r.logger.Debug("Testing join target via HTTP", zap.String("url", statusURL))
	resp, err := client.Get(statusURL)
	if err != nil {
		return fmt.Errorf("failed to connect to leader HTTP at %s: %w", statusURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("leader HTTP at %s returned status %d", statusURL, resp.StatusCode)
	}

	r.logger.Info("Leader HTTP reachable", zap.String("status_url", statusURL))
	return nil
}

// canSafelyBecomeLeader checks if this node can safely become leader without causing data loss
func (r *RQLiteManager) canSafelyBecomeLeader() (bool, error) {
	// Get our current Raft log index
	ourLogIndex := r.getRaftLogIndex()

	// If no discovery service, assume it's safe (backward compatibility)
	if r.discoveryService == nil {
		r.logger.Debug("No discovery service, assuming safe to lead")
		return true, nil
	}

	// Query discovery service for other nodes
	otherNodes := r.discoveryService.GetActivePeers()

	if len(otherNodes) == 0 {
		// No other nodes - safe to bootstrap
		r.logger.Debug("No other nodes discovered, safe to lead",
			zap.Uint64("our_log_index", ourLogIndex))
		return true, nil
	}

	// Check if any other node has higher log index
	for _, peer := range otherNodes {
		if peer.RaftLogIndex > ourLogIndex {
			// Other node has more data - we should join them
			return false, fmt.Errorf(
				"node %s has higher log index (%d > %d), should join as follower",
				peer.NodeID, peer.RaftLogIndex, ourLogIndex)
		}
	}

	// We have most recent data or equal - safe to lead
	r.logger.Info("Safe to lead - we have most recent data",
		zap.Uint64("our_log_index", ourLogIndex),
		zap.Int("other_nodes_checked", len(otherNodes)))
	return true, nil
}

// joinExistingCluster attempts to join an existing cluster as a follower
func (r *RQLiteManager) joinExistingCluster(ctx context.Context, raftAddress string) error {
	r.logger.Info("Attempting to join existing cluster",
		zap.String("target_raft_address", raftAddress))

	// Wait for the target to be reachable
	if err := r.waitForJoinTarget(ctx, raftAddress, 2*time.Minute); err != nil {
		return fmt.Errorf("join target not reachable: %w", err)
	}

	// Wait for SQL availability (the target should have a leader)
	sqlCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		sqlCtx, cancel = context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
	}

	if err := r.waitForSQLAvailable(sqlCtx); err != nil {
		return fmt.Errorf("failed to join cluster - SQL not available: %w", err)
	}

	r.logger.Info("Successfully joined existing cluster")
	return nil
}

// exponentialBackoff calculates exponential backoff duration with jitter
func (r *RQLiteManager) exponentialBackoff(attempt int, baseDelay time.Duration, maxDelay time.Duration) time.Duration {
	// Calculate exponential backoff: baseDelay * 2^attempt
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter (±20%)
	jitter := time.Duration(float64(delay) * 0.2 * (2.0*float64(time.Now().UnixNano()%100)/100.0 - 1.0))
	return delay + jitter
}

// recoverCluster restarts RQLite using the recovery.db created from peers.json
func (r *RQLiteManager) recoverCluster(peersJSONPath string) error {
	r.logger.Info("Initiating cluster recovery by restarting RQLite",
		zap.String("peers_file", peersJSONPath))

	// Stop the current RQLite process
	r.logger.Info("Stopping RQLite for recovery")
	if err := r.Stop(); err != nil {
		r.logger.Warn("Error stopping RQLite", zap.Error(err))
	}

	// Wait for process to fully stop
	time.Sleep(2 * time.Second)

	// Restart RQLite - it will automatically detect peers.json and perform recovery
	r.logger.Info("Restarting RQLite (will auto-recover using peers.json)")

	// Build the same args as original Start() - expand ~ in data directory
	dataDir := os.ExpandEnv(r.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}
	rqliteDataDir := filepath.Join(dataDir, "rqlite")
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLitePort),
		"-http-adv-addr", r.discoverConfig.HttpAdvAddress,
		"-raft-adv-addr", r.discoverConfig.RaftAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLiteRaftPort),
		rqliteDataDir,
	}

	// Restart RQLite
	r.cmd = exec.Command("rqlited", args...)
	r.cmd.Stdout = os.Stdout
	r.cmd.Stderr = os.Stderr

	if err := r.cmd.Start(); err != nil {
		return fmt.Errorf("failed to restart RQLite: %w", err)
	}

	r.logger.Info("RQLite restarted, waiting for it to become ready")
	time.Sleep(3 * time.Second)

	// Recreate connection
	conn, err := gorqlite.Open(fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
	if err != nil {
		return fmt.Errorf("failed to reconnect to RQLite: %w", err)
	}
	r.connection = conn

	r.logger.Info("Cluster recovery completed, RQLite restarted with new configuration")
	return nil
}

// checkNeedsClusterRecovery checks if the node has old cluster state that requires coordinated recovery
// Returns true if there are snapshots but the raft log is empty (typical after a crash/restart)
func (r *RQLiteManager) checkNeedsClusterRecovery(rqliteDataDir string) (bool, error) {
	// Check for snapshots directory
	snapshotsDir := filepath.Join(rqliteDataDir, "rsnapshots")
	if _, err := os.Stat(snapshotsDir); os.IsNotExist(err) {
		// No snapshots = fresh start, no recovery needed
		return false, nil
	}

	// Check if snapshots directory has any snapshots
	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		return false, fmt.Errorf("failed to read snapshots directory: %w", err)
	}

	hasSnapshots := false
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".db") {
			hasSnapshots = true
			break
		}
	}

	if !hasSnapshots {
		// No snapshots = fresh start
		return false, nil
	}

	// Check raft log size - if it's the default empty size, we need recovery
	raftLogPath := filepath.Join(rqliteDataDir, "raft.db")
	if info, err := os.Stat(raftLogPath); err == nil {
		// Empty or default-sized log with snapshots means we need coordinated recovery
		if info.Size() <= 8*1024*1024 { // <= 8MB (default empty log size)
			r.logger.Info("Detected cluster recovery situation: snapshots exist but raft log is empty/default size",
				zap.String("snapshots_dir", snapshotsDir),
				zap.Int64("raft_log_size", info.Size()))
			return true, nil
		}
	}

	return false, nil
}

// performPreStartClusterDiscovery waits for peer discovery and builds a complete peers.json
// before starting RQLite. This ensures all nodes use the same cluster membership for recovery.
func (r *RQLiteManager) performPreStartClusterDiscovery(ctx context.Context, rqliteDataDir string) error {
	if r.discoveryService == nil {
		r.logger.Warn("No discovery service available, cannot perform pre-start cluster discovery")
		return fmt.Errorf("discovery service not available")
	}

	r.logger.Info("Waiting for peer discovery to find other cluster members...")

	// CRITICAL: First, actively trigger peer exchange to populate peerstore with RQLite metadata
	// The peerstore needs RQLite metadata from other nodes BEFORE we can collect it
	r.logger.Info("Triggering peer exchange to collect RQLite metadata from connected peers")
	if err := r.discoveryService.TriggerPeerExchange(ctx); err != nil {
		r.logger.Warn("Peer exchange failed, continuing anyway", zap.Error(err))
	}

	// Give peer exchange a moment to complete
	time.Sleep(1 * time.Second)

	// Now trigger cluster membership sync to populate knownPeers map from the peerstore
	r.logger.Info("Triggering initial cluster membership sync to populate peer list")
	r.discoveryService.TriggerSync()

	// Give the sync a moment to complete
	time.Sleep(2 * time.Second)

	// Wait for peer discovery - give it time to find peers (30 seconds should be enough)
	discoveryDeadline := time.Now().Add(30 * time.Second)
	var discoveredPeers int

	for time.Now().Before(discoveryDeadline) {
		// Check how many peers with RQLite metadata we've discovered
		allPeers := r.discoveryService.GetAllPeers()
		discoveredPeers = len(allPeers)

		r.logger.Info("Peer discovery progress",
			zap.Int("discovered_peers", discoveredPeers),
			zap.Duration("time_remaining", time.Until(discoveryDeadline)))

		// If we have at least our minimum cluster size, proceed
		if discoveredPeers >= r.config.MinClusterSize {
			r.logger.Info("Found minimum cluster size peers, proceeding with recovery",
				zap.Int("discovered_peers", discoveredPeers),
				zap.Int("min_cluster_size", r.config.MinClusterSize))
			break
		}

		// Wait a bit before checking again
		time.Sleep(2 * time.Second)
	}

	if discoveredPeers == 0 {
		r.logger.Warn("No peers discovered during pre-start discovery window, will attempt solo recovery")
		// Continue anyway - might be the only node left
	}

	// Trigger final sync to ensure peers.json is up to date with latest discovered peers
	r.logger.Info("Triggering final cluster membership sync to build complete peers.json")
	r.discoveryService.TriggerSync()

	// Wait a moment for the sync to complete
	time.Sleep(2 * time.Second)

	// Verify peers.json was created
	peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
	if _, err := os.Stat(peersPath); err != nil {
		return fmt.Errorf("peers.json was not created after discovery: %w", err)
	}

	r.logger.Info("Pre-start cluster discovery completed successfully",
		zap.String("peers_file", peersPath),
		zap.Int("peer_count", discoveredPeers))

	return nil
}

// validateNodeID checks that rqlite's reported node ID matches our configured raft address
func (r *RQLiteManager) validateNodeID() error {
	// Query /nodes endpoint to get our node ID
	// Retry a few times as the endpoint might not be ready immediately
	for i := 0; i < 5; i++ {
		nodes, err := r.getRQLiteNodes()
		if err != nil {
			// If endpoint is not ready yet, wait and retry
			if i < 4 {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return fmt.Errorf("failed to query nodes endpoint after retries: %w", err)
		}

		expectedID := r.discoverConfig.RaftAdvAddress
		if expectedID == "" {
			return fmt.Errorf("raft_adv_address not configured")
		}

		// Find our node in the cluster (match by address)
		for _, node := range nodes {
			if node.Address == expectedID {
				if node.ID != expectedID {
					r.logger.Error("CRITICAL: RQLite node ID mismatch",
						zap.String("configured_raft_address", expectedID),
						zap.String("rqlite_node_id", node.ID),
						zap.String("rqlite_node_address", node.Address),
						zap.String("explanation", "peers.json id field must match rqlite's node ID (raft address)"))
					return fmt.Errorf("node ID mismatch: configured %s but rqlite reports %s", expectedID, node.ID)
				}
				r.logger.Info("Node ID validation passed",
					zap.String("node_id", node.ID),
					zap.String("address", node.Address))
				return nil
			}
		}

		// If cluster is still forming, nodes list might be empty - that's okay
		if len(nodes) == 0 {
			r.logger.Debug("Cluster membership not yet available, skipping validation")
			return nil
		}

		// If we can't find ourselves but other nodes exist, log a warning
		r.logger.Warn("Could not find our node in cluster membership",
			zap.String("expected_address", expectedID),
			zap.Int("nodes_in_cluster", len(nodes)))
		return nil
	}

	return nil
}
