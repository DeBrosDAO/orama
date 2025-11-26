package rqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	nodeType         string // Node type identifier
	logger           *zap.Logger
	cmd              *exec.Cmd
	connection       *gorqlite.Connection
	discoveryService *ClusterDiscoveryService
}

// waitForSQLAvailable waits until a simple query succeeds, indicating a leader is known and queries can be served.
func (r *RQLiteManager) waitForSQLAvailable(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Check for nil connection inside the loop to handle cases where
			// connection becomes nil during restart/recovery operations
			if r.connection == nil {
				attempts++
				if attempts%5 == 0 { // log every ~5s to reduce noise
					r.logger.Debug("Waiting for RQLite connection to be established")
				}
				continue
			}

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
		logger:         logger.With(zap.String("component", "rqlite-manager")),
	}
}

// SetDiscoveryService sets the cluster discovery service for this RQLite manager
func (r *RQLiteManager) SetDiscoveryService(service *ClusterDiscoveryService) {
	r.discoveryService = service
}

// SetNodeType sets the node type for this RQLite manager
func (r *RQLiteManager) SetNodeType(nodeType string) {
	if nodeType != "" {
		r.nodeType = nodeType
	}
}

// UpdateAdvertisedAddresses overrides the discovery advertised addresses when cluster discovery
// infers a better host than what was provided via configuration (e.g. replacing localhost).
func (r *RQLiteManager) UpdateAdvertisedAddresses(raftAddr, httpAddr string) {
	if r == nil || r.discoverConfig == nil {
		return
	}

	if raftAddr != "" && r.discoverConfig.RaftAdvAddress != raftAddr {
		r.logger.Info("Updating Raft advertised address", zap.String("addr", raftAddr))
		r.discoverConfig.RaftAdvAddress = raftAddr
	}

	if httpAddr != "" && r.discoverConfig.HttpAdvAddress != httpAddr {
		r.logger.Info("Updating HTTP advertised address", zap.String("addr", httpAddr))
		r.discoverConfig.HttpAdvAddress = httpAddr
	}
}

// Start starts the RQLite node
func (r *RQLiteManager) Start(ctx context.Context) error {
	rqliteDataDir, err := r.prepareDataDir()
	if err != nil {
		return err
	}

	if r.discoverConfig.HttpAdvAddress == "" {
		return fmt.Errorf("discovery config HttpAdvAddress is empty")
	}

	// CRITICAL FIX: Ensure peers.json exists with minimum cluster size BEFORE starting RQLite
	// This prevents split-brain where each node starts as a single-node cluster
	// We NEVER start as a single-node cluster - we wait indefinitely until minimum cluster size is met
	// This applies to ALL nodes (with or without join addresses)
	if r.discoveryService != nil {
		r.logger.Info("Ensuring peers.json exists with minimum cluster size before RQLite startup",
			zap.String("policy", "will wait indefinitely - never start as single-node cluster"),
			zap.Bool("has_join_address", r.config.RQLiteJoinAddress != ""))

		// Wait for peer discovery to find minimum cluster size - NO TIMEOUT
		// This ensures we never start as a single-node cluster, regardless of join address
		if err := r.waitForMinClusterSizeBeforeStart(ctx, rqliteDataDir); err != nil {
			r.logger.Error("Failed to ensure minimum cluster size before start",
				zap.Error(err),
				zap.String("action", "startup aborted - will not start as single-node cluster"))
			return fmt.Errorf("cannot start RQLite: minimum cluster size not met: %w", err)
		}
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

	// Launch RQLite process
	if err := r.launchProcess(ctx, rqliteDataDir); err != nil {
		return err
	}

	// Wait for RQLite to be ready and establish connection
	if err := r.waitForReadyAndConnect(ctx); err != nil {
		return err
	}

	// Start periodic health monitoring for automatic recovery
	if r.discoveryService != nil {
		go r.startHealthMonitoring(ctx)
	}

	// Establish leadership/SQL availability
	if err := r.establishLeadershipOrJoin(ctx, rqliteDataDir); err != nil {
		return err
	}

	// Apply migrations - resolve path for production vs development
	migrationsDir, err := r.resolveMigrationsDir()
	if err != nil {
		r.logger.Error("Failed to resolve migrations directory", zap.Error(err))
		return fmt.Errorf("resolve migrations directory: %w", err)
	}
	if err := r.ApplyMigrations(ctx, migrationsDir); err != nil {
		r.logger.Error("Migrations failed", zap.Error(err), zap.String("dir", migrationsDir))
		return fmt.Errorf("apply migrations: %w", err)
	}

	r.logger.Info("RQLite node started successfully")
	return nil
}

// rqliteDataDirPath returns the resolved path to the RQLite data directory
// This centralizes the path resolution logic used throughout the codebase
func (r *RQLiteManager) rqliteDataDirPath() (string, error) {
	// Expand ~ in data directory path
	dataDir := os.ExpandEnv(r.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	return filepath.Join(dataDir, "rqlite"), nil
}

// resolveMigrationsDir resolves the migrations directory path for production vs development
// In production, migrations are at /home/debros/src/migrations
// In development, migrations are relative to the project root (migrations/)
func (r *RQLiteManager) resolveMigrationsDir() (string, error) {
	// Check for production path first: /home/debros/src/migrations
	productionPath := "/home/debros/src/migrations"
	if _, err := os.Stat(productionPath); err == nil {
		r.logger.Info("Using production migrations directory", zap.String("path", productionPath))
		return productionPath, nil
	}

	// Fall back to relative path for development
	devPath := "migrations"
	r.logger.Info("Using development migrations directory", zap.String("path", devPath))
	return devPath, nil
}

// prepareDataDir expands and creates the RQLite data directory
func (r *RQLiteManager) prepareDataDir() (string, error) {
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return "", err
	}

	// Create data directory
	if err := os.MkdirAll(rqliteDataDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create RQLite data directory: %w", err)
	}

	return rqliteDataDir, nil
}

// launchProcess starts the RQLite process with appropriate arguments
func (r *RQLiteManager) launchProcess(ctx context.Context, rqliteDataDir string) error {
	// Build RQLite command
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLitePort),
		"-http-adv-addr", r.discoverConfig.HttpAdvAddress,
		"-raft-adv-addr", r.discoverConfig.RaftAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLiteRaftPort),
	}

	// All nodes follow the same join logic - either join specified address or start as single-node cluster
	if r.config.RQLiteJoinAddress != "" {
		r.logger.Info("Joining RQLite cluster", zap.String("join_address", r.config.RQLiteJoinAddress))

		// Normalize join address to host:port for rqlited -join
		joinArg := r.config.RQLiteJoinAddress
		if strings.HasPrefix(joinArg, "http://") {
			joinArg = strings.TrimPrefix(joinArg, "http://")
		} else if strings.HasPrefix(joinArg, "https://") {
			joinArg = strings.TrimPrefix(joinArg, "https://")
		}

		// Wait for join target to become reachable to avoid forming a separate cluster
		// Use 5 minute timeout to prevent infinite waits on bad configurations
		joinTimeout := 5 * time.Minute
		if err := r.waitForJoinTarget(ctx, r.config.RQLiteJoinAddress, joinTimeout); err != nil {
			r.logger.Warn("Join target did not become reachable within timeout; will still attempt to join",
				zap.String("join_address", r.config.RQLiteJoinAddress),
				zap.Duration("timeout", joinTimeout),
				zap.Error(err))
		}

		// Always add the join parameter in host:port form - let rqlited handle the rest
		// Add retry parameters to handle slow cluster startup (e.g., during recovery)
		args = append(args, "-join", joinArg, "-join-attempts", "30", "-join-interval", "10s")
	} else {
		r.logger.Info("No join address specified - starting as single-node cluster")
		// When no join address is provided, rqlited will start as a single-node cluster
		// This is expected for the first node in a fresh cluster
	}

	// Add data directory as positional argument
	args = append(args, rqliteDataDir)

	r.logger.Info("Starting RQLite node",
		zap.String("data_dir", rqliteDataDir),
		zap.Int("http_port", r.config.RQLitePort),
		zap.Int("raft_port", r.config.RQLiteRaftPort),
		zap.String("join_address", r.config.RQLiteJoinAddress))

	// Start RQLite process (not bound to ctx for graceful Stop handling)
	r.cmd = exec.Command("rqlited", args...)

	// Setup log file for RQLite output
	// Determine node type for log filename
	nodeType := r.nodeType
	if nodeType == "" {
		nodeType = "node"
	}

	// Create logs directory
	logsDir := filepath.Join(filepath.Dir(r.dataDir), "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory at %s: %w", logsDir, err)
	}

	// Open log file for RQLite output
	logPath := filepath.Join(logsDir, fmt.Sprintf("rqlite-%s.log", nodeType))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open RQLite log file at %s: %w", logPath, err)
	}

	r.logger.Info("RQLite logs will be written to file",
		zap.String("path", logPath))

	r.cmd.Stdout = logFile
	r.cmd.Stderr = logFile

	if err := r.cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Close the log file handle after process starts (the subprocess maintains its own reference)
	// This allows the file to be rotated or inspected while the process is running
	logFile.Close()

	return nil
}

// waitForReadyAndConnect waits for RQLite to be ready and establishes connection
// For joining nodes, retries if gorqlite.Open fails with "store is not open" error
func (r *RQLiteManager) waitForReadyAndConnect(ctx context.Context) error {
	// Wait for RQLite to be ready
	if err := r.waitForReady(ctx); err != nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("RQLite failed to become ready: %w", err)
	}

	// For joining nodes, retry gorqlite.Open if store is not yet open
	// This handles recovery scenarios where the store opens after HTTP is responsive
	var conn *gorqlite.Connection
	var err error
	maxConnectAttempts := 10
	connectBackoff := 500 * time.Millisecond

	for attempt := 0; attempt < maxConnectAttempts; attempt++ {
		// Create connection
		conn, err = gorqlite.Open(fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
		if err == nil {
			// Success
			r.connection = conn
			r.logger.Debug("Successfully connected to RQLite", zap.Int("attempt", attempt+1))
			break
		}

		// Check if error is "store is not open" (recovery scenario)
		if strings.Contains(err.Error(), "store is not open") {
			if attempt < maxConnectAttempts-1 {
				// Retry with exponential backoff for all nodes during recovery
				// The store may not open immediately, especially during cluster recovery
				if attempt%3 == 0 {
					r.logger.Debug("RQLite store not yet accessible for connection, retrying...",
						zap.Int("attempt", attempt+1), zap.Error(err))
				}
				time.Sleep(connectBackoff)
				connectBackoff = time.Duration(float64(connectBackoff) * 1.5)
				if connectBackoff > 5*time.Second {
					connectBackoff = 5 * time.Second
				}
				continue
			}
		}

		// For any other error or final attempt, fail
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("failed to connect to RQLite: %w", err)
	}

	if conn == nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("failed to establish RQLite connection after %d attempts", maxConnectAttempts)
	}

	// Sanity check: verify rqlite's node ID matches our configured raft address
	if err := r.validateNodeID(); err != nil {
		r.logger.Debug("Node ID validation skipped", zap.Error(err))
		// Don't fail startup, but log at debug level
	}

	return nil
}

// establishLeadershipOrJoin handles post-startup cluster establishment
// All nodes follow the same pattern: wait for SQL availability
// For nodes without a join address, RQLite automatically forms a single-node cluster and becomes leader
func (r *RQLiteManager) establishLeadershipOrJoin(ctx context.Context, rqliteDataDir string) error {
	if r.config.RQLiteJoinAddress == "" {
		// First node - no join address specified
		// RQLite will automatically form a single-node cluster and become leader
		r.logger.Info("Starting as first node in cluster")

		// Wait for SQL to be available (indicates RQLite cluster is ready)
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
			return fmt.Errorf("SQL not available for first node: %w", err)
		}

		r.logger.Info("First node established successfully")
		return nil
	}

	// Joining node - wait for SQL availability (indicates it joined the leader)
	r.logger.Info("Waiting for RQLite SQL availability (joining cluster)")
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

	r.logger.Info("Node successfully joined cluster")
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
// It checks for HTTP 200 + valid raft state (leader/follower)
// The store may not be fully open initially during recovery, but connection retries will handle it
// For joining nodes in recovery, this may take longer (up to 3 minutes)
func (r *RQLiteManager) waitForReady(ctx context.Context) error {
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := &http.Client{Timeout: 2 * time.Second}

	// All nodes may need time to open the store during recovery
	// Use consistent timeout for cluster consistency
	maxAttempts := 180 // 180 seconds (3 minutes) for all nodes

	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			// Parse the response to check for valid raft state
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil {
				var statusResp map[string]interface{}
				if err := json.Unmarshal(body, &statusResp); err == nil {
					// Check for valid raft state (leader or follower)
					// If raft is established, we consider the node ready even if store.open is false
					// The store will eventually open during recovery, and connection retries will handle it
					if raft, ok := statusResp["raft"].(map[string]interface{}); ok {
						state, ok := raft["state"].(string)
						if ok && (state == "leader" || state == "follower") {
							r.logger.Debug("RQLite raft ready", zap.String("state", state), zap.Int("attempt", i+1))
							return nil
						}
						// Raft not yet ready (likely in candidate state)
						if i%10 == 0 {
							r.logger.Debug("RQLite raft not yet ready", zap.String("state", state), zap.Int("attempt", i+1))
						}
					} else {
						// If no raft field, fall back to treating HTTP 200 as ready
						// (for backwards compatibility with older RQLite versions)
						r.logger.Debug("RQLite HTTP responsive (no raft field)", zap.Int("attempt", i+1))
						return nil
					}
				} else {
					resp.Body.Close()
				}
			}
		} else if err != nil && i%20 == 0 {
			// Log connection errors only periodically (every ~20s)
			r.logger.Debug("RQLite not yet reachable", zap.Int("attempt", i+1), zap.Error(err))
		} else if resp != nil {
			resp.Body.Close()
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("RQLite did not become ready within timeout")
}

// GetConnection returns the RQLite connection
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

// waitForMinClusterSizeBeforeStart waits for minimum cluster size to be discovered
// and ensures peers.json exists before RQLite starts
// CRITICAL: This function waits INDEFINITELY - it will NEVER timeout
// We never start as a single-node cluster, regardless of how long we wait
func (r *RQLiteManager) waitForMinClusterSizeBeforeStart(ctx context.Context, rqliteDataDir string) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available")
	}

	requiredRemotePeers := r.config.MinClusterSize - 1
	r.logger.Info("Waiting for minimum cluster size before RQLite startup",
		zap.Int("min_cluster_size", r.config.MinClusterSize),
		zap.Int("required_remote_peers", requiredRemotePeers),
		zap.String("policy", "waiting indefinitely - will never start as single-node cluster"))

	// Trigger peer exchange to collect metadata
	if err := r.discoveryService.TriggerPeerExchange(ctx); err != nil {
		r.logger.Warn("Peer exchange failed", zap.Error(err))
	}

	// NO TIMEOUT - wait indefinitely until minimum cluster size is met
	// Only exit on context cancellation or when minimum cluster size is achieved
	checkInterval := 2 * time.Second
	lastLogTime := time.Now()

	for {
		// Check context cancellation first
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for minimum cluster size: %w", ctx.Err())
		default:
		}

		// Trigger sync to update knownPeers
		r.discoveryService.TriggerSync()
		time.Sleep(checkInterval)

		// Check if we have enough remote peers
		allPeers := r.discoveryService.GetAllPeers()
		remotePeerCount := 0
		for _, peer := range allPeers {
			if peer.NodeID != r.discoverConfig.RaftAdvAddress {
				remotePeerCount++
			}
		}

		if remotePeerCount >= requiredRemotePeers {
			// Found enough peers - verify peers.json exists and contains them
			peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")

			// Trigger one more sync to ensure peers.json is written
			r.discoveryService.TriggerSync()
			time.Sleep(2 * time.Second)

			// Verify peers.json exists and contains enough peers
			if info, err := os.Stat(peersPath); err == nil && info.Size() > 10 {
				// Read and verify it contains enough peers
				data, err := os.ReadFile(peersPath)
				if err == nil {
					var peers []map[string]interface{}
					if err := json.Unmarshal(data, &peers); err == nil && len(peers) >= requiredRemotePeers {
						r.logger.Info("peers.json exists with minimum cluster size, safe to start RQLite",
							zap.String("peers_file", peersPath),
							zap.Int("remote_peers_discovered", remotePeerCount),
							zap.Int("peers_in_json", len(peers)),
							zap.Int("min_cluster_size", r.config.MinClusterSize))
						return nil
					}
				}
			}
		}

		// Log progress every 10 seconds
		if time.Since(lastLogTime) >= 10*time.Second {
			r.logger.Info("Waiting for minimum cluster size (indefinitely)...",
				zap.Int("discovered_peers", len(allPeers)),
				zap.Int("remote_peers", remotePeerCount),
				zap.Int("required_remote_peers", requiredRemotePeers),
				zap.String("status", "will continue waiting until minimum cluster size is met"))
			lastLogTime = time.Now()
		}
	}
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
// It reuses launchProcess and waitForReadyAndConnect to ensure all join/backoff logic
// and proper readiness checks are applied during recovery.
func (r *RQLiteManager) recoverCluster(ctx context.Context, peersJSONPath string) error {
	r.logger.Info("Initiating cluster recovery by restarting RQLite",
		zap.String("peers_file", peersJSONPath))

	// Stop the current RQLite process
	r.logger.Info("Stopping RQLite for recovery")
	if err := r.Stop(); err != nil {
		r.logger.Warn("Error stopping RQLite", zap.Error(err))
	}

	// Wait for process to fully stop
	time.Sleep(2 * time.Second)

	// Get the data directory path
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return fmt.Errorf("failed to resolve RQLite data directory: %w", err)
	}

	// Restart RQLite using launchProcess to ensure all join/backoff logic is applied
	// This includes: join address handling, join retries, expect configuration, etc.
	r.logger.Info("Restarting RQLite (will auto-recover using peers.json)")
	if err := r.launchProcess(ctx, rqliteDataDir); err != nil {
		return fmt.Errorf("failed to restart RQLite process: %w", err)
	}

	// Wait for RQLite to be ready and establish connection using proper readiness checks
	// This includes retries for "store is not open" errors during recovery
	if err := r.waitForReadyAndConnect(ctx); err != nil {
		// Clean up the process if connection failed
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("failed to wait for RQLite readiness after recovery: %w", err)
	}

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

// hasExistingRaftState checks if this node has any existing Raft state files
// Returns true if raft.db exists and has content, or if peers.json exists
func (r *RQLiteManager) hasExistingRaftState(rqliteDataDir string) bool {
	// Check for raft.db
	raftLogPath := filepath.Join(rqliteDataDir, "raft.db")
	if info, err := os.Stat(raftLogPath); err == nil {
		// If raft.db exists and has meaningful content (> 1KB), we have state
		if info.Size() > 1024 {
			return true
		}
	}

	// Check for peers.json
	peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
	if _, err := os.Stat(peersPath); err == nil {
		return true
	}

	return false
}

// clearRaftState safely removes Raft state files to allow a clean join
// This removes raft.db and peers.json but preserves db.sqlite
func (r *RQLiteManager) clearRaftState(rqliteDataDir string) error {
	r.logger.Warn("Clearing Raft state to allow clean cluster join",
		zap.String("data_dir", rqliteDataDir))

	// Remove raft.db if it exists
	raftLogPath := filepath.Join(rqliteDataDir, "raft.db")
	if err := os.Remove(raftLogPath); err != nil && !os.IsNotExist(err) {
		r.logger.Warn("Failed to remove raft.db", zap.Error(err))
	} else if err == nil {
		r.logger.Info("Removed raft.db")
	}

	// Remove peers.json if it exists
	peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
	if err := os.Remove(peersPath); err != nil && !os.IsNotExist(err) {
		r.logger.Warn("Failed to remove peers.json", zap.Error(err))
	} else if err == nil {
		r.logger.Info("Removed peers.json")
	}

	// Remove raft directory if it's empty
	raftDir := filepath.Join(rqliteDataDir, "raft")
	if entries, err := os.ReadDir(raftDir); err == nil && len(entries) == 0 {
		if err := os.Remove(raftDir); err != nil {
			r.logger.Debug("Failed to remove empty raft directory", zap.Error(err))
		}
	}

	r.logger.Info("Raft state cleared successfully - node will join as fresh follower")
	return nil
}

// isInSplitBrainState detects if we're in a split-brain scenario where all nodes
// are followers with no peers (each node thinks it's alone)
func (r *RQLiteManager) isInSplitBrainState() bool {
	status, err := r.getRQLiteStatus()
	if err != nil {
		return false
	}

	raft := status.Store.Raft

	// Split-brain indicators:
	// - State is Follower (not Leader)
	// - Term is 0 (no leader election has occurred)
	// - num_peers is 0 (node thinks it's alone)
	// - voter is false (node not configured as voter)
	isSplitBrain := raft.State == "Follower" &&
		raft.Term == 0 &&
		raft.NumPeers == 0 &&
		!raft.Voter &&
		raft.LeaderAddr == ""

	if !isSplitBrain {
		return false
	}

	// Verify all discovered peers are also in split-brain state
	if r.discoveryService == nil {
		r.logger.Debug("No discovery service to verify split-brain across peers")
		return false
	}

	peers := r.discoveryService.GetActivePeers()
	if len(peers) == 0 {
		// No peers discovered yet - might be network issue, not split-brain
		return false
	}

	// Check if all reachable peers are also in split-brain
	splitBrainCount := 0
	reachableCount := 0
	for _, peer := range peers {
		if !r.isPeerReachable(peer.HTTPAddress) {
			continue
		}
		reachableCount++

		peerStatus, err := r.getPeerRQLiteStatus(peer.HTTPAddress)
		if err != nil {
			continue
		}

		peerRaft := peerStatus.Store.Raft
		if peerRaft.State == "Follower" &&
			peerRaft.Term == 0 &&
			peerRaft.NumPeers == 0 &&
			!peerRaft.Voter {
			splitBrainCount++
		}
	}

	// If all reachable peers are in split-brain, we have cluster-wide split-brain
	if reachableCount > 0 && splitBrainCount == reachableCount {
		r.logger.Warn("Detected cluster-wide split-brain state",
			zap.Int("reachable_peers", reachableCount),
			zap.Int("split_brain_peers", splitBrainCount))
		return true
	}

	return false
}

// isPeerReachable checks if a peer is at least responding to HTTP requests
func (r *RQLiteManager) isPeerReachable(httpAddr string) bool {
	url := fmt.Sprintf("http://%s/status", httpAddr)
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// getPeerRQLiteStatus queries a peer's status endpoint
func (r *RQLiteManager) getPeerRQLiteStatus(httpAddr string) (*RQLiteStatus, error) {
	url := fmt.Sprintf("http://%s/status", httpAddr)
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("peer returned status %d", resp.StatusCode)
	}

	var status RQLiteStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}

	return &status, nil
}

// startHealthMonitoring runs periodic health checks and automatically recovers from split-brain
func (r *RQLiteManager) startHealthMonitoring(ctx context.Context) {
	// Wait a bit after startup before starting health checks
	time.Sleep(30 * time.Second)

	ticker := time.NewTicker(60 * time.Second) // Check every minute
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check for split-brain state
			if r.isInSplitBrainState() {
				r.logger.Warn("Split-brain detected during health check, initiating automatic recovery")

				// Attempt automatic recovery
				if err := r.recoverFromSplitBrain(ctx); err != nil {
					r.logger.Error("Automatic split-brain recovery failed",
						zap.Error(err),
						zap.String("action", "will retry on next health check"))
				} else {
					r.logger.Info("Successfully recovered from split-brain")
				}
			}
		}
	}
}

// recoverFromSplitBrain automatically recovers from split-brain state
func (r *RQLiteManager) recoverFromSplitBrain(ctx context.Context) error {
	if r.discoveryService == nil {
		return fmt.Errorf("discovery service not available for recovery")
	}

	r.logger.Info("Starting automatic split-brain recovery")

	// Step 1: Ensure we have latest peer information
	r.discoveryService.TriggerPeerExchange(ctx)
	time.Sleep(2 * time.Second)
	r.discoveryService.TriggerSync()
	time.Sleep(2 * time.Second)

	// Step 2: Get data directory
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return fmt.Errorf("failed to get data directory: %w", err)
	}

	// Step 3: Check if peers have more recent data
	allPeers := r.discoveryService.GetAllPeers()
	maxPeerIndex := uint64(0)
	for _, peer := range allPeers {
		if peer.NodeID == r.discoverConfig.RaftAdvAddress {
			continue // Skip self
		}
		if peer.RaftLogIndex > maxPeerIndex {
			maxPeerIndex = peer.RaftLogIndex
		}
	}

	// Step 4: Clear our Raft state if peers have more recent data
	ourIndex := r.getRaftLogIndex()
	if maxPeerIndex > ourIndex || (maxPeerIndex == 0 && ourIndex == 0) {
		r.logger.Info("Clearing Raft state to allow clean cluster join",
			zap.Uint64("our_index", ourIndex),
			zap.Uint64("peer_max_index", maxPeerIndex))

		if err := r.clearRaftState(rqliteDataDir); err != nil {
			return fmt.Errorf("failed to clear Raft state: %w", err)
		}

		// Step 5: Refresh peer metadata and force write peers.json
		// We trigger peer exchange again to ensure we have the absolute latest metadata
		// after clearing state, then force write peers.json regardless of changes
		r.logger.Info("Refreshing peer metadata after clearing raft state")
		r.discoveryService.TriggerPeerExchange(ctx)
		time.Sleep(1 * time.Second) // Brief wait for peer exchange to complete

		r.logger.Info("Force writing peers.json with all discovered peers")
		// We use ForceWritePeersJSON instead of TriggerSync because TriggerSync
		// only writes if membership changed, but after clearing state we need
		// to write regardless of changes
		if err := r.discoveryService.ForceWritePeersJSON(); err != nil {
			return fmt.Errorf("failed to force write peers.json: %w", err)
		}

		// Verify peers.json was created
		peersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
		if _, err := os.Stat(peersPath); err != nil {
			return fmt.Errorf("peers.json not created after force write: %w", err)
		}

		r.logger.Info("peers.json verified after force write",
			zap.String("peers_path", peersPath))

		// Step 6: Restart RQLite to pick up new peers.json
		r.logger.Info("Restarting RQLite to apply new cluster configuration")
		if err := r.recoverCluster(ctx, peersPath); err != nil {
			return fmt.Errorf("failed to restart RQLite: %w", err)
		}

		// Step 7: Wait for cluster to form (waitForReadyAndConnect already handled readiness)
		r.logger.Info("Waiting for cluster to stabilize after recovery...")
		time.Sleep(5 * time.Second)

		// Verify recovery succeeded
		if r.isInSplitBrainState() {
			return fmt.Errorf("still in split-brain after recovery attempt")
		}

		r.logger.Info("Split-brain recovery completed successfully")
		return nil
	}

	return fmt.Errorf("cannot recover: we have more recent data than peers")
}

// isSafeToClearState verifies we can safely clear Raft state
// Returns true only if peers have higher log indexes (they have more recent data)
// or if we have no meaningful state (index == 0)
func (r *RQLiteManager) isSafeToClearState(rqliteDataDir string) bool {
	if r.discoveryService == nil {
		r.logger.Debug("No discovery service available, cannot verify safety")
		return false // No discovery service, can't verify
	}

	ourIndex := r.getRaftLogIndex()
	peers := r.discoveryService.GetActivePeers()

	if len(peers) == 0 {
		r.logger.Debug("No peers discovered, might be network issue")
		return false // No peers, might be network issue
	}

	// Find max peer log index
	maxPeerIndex := uint64(0)
	for _, peer := range peers {
		if peer.RaftLogIndex > maxPeerIndex {
			maxPeerIndex = peer.RaftLogIndex
		}
	}

	// Safe to clear if peers have higher log indexes (they have more recent data)
	// OR if we have no meaningful state (index == 0)
	safe := maxPeerIndex > ourIndex || ourIndex == 0

	r.logger.Debug("Checking if safe to clear Raft state",
		zap.Uint64("our_log_index", ourIndex),
		zap.Uint64("peer_max_log_index", maxPeerIndex),
		zap.Bool("safe_to_clear", safe))

	return safe
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

	// CRITICAL FIX: Skip recovery if no peers were discovered (other than ourselves)
	// Only ourselves in the cluster means this is a fresh cluster, not a recovery scenario
	if discoveredPeers <= 1 {
		r.logger.Info("No peers discovered during pre-start discovery window - skipping recovery (fresh cluster)",
			zap.Int("discovered_peers", discoveredPeers))
		return nil
	}

	// AUTOMATIC RECOVERY: Check if we have stale Raft state that conflicts with cluster
	// If we have existing state but peers have higher log indexes, clear our state to allow clean join
	allPeers := r.discoveryService.GetAllPeers()
	hasExistingState := r.hasExistingRaftState(rqliteDataDir)

	if hasExistingState {
		// Find the highest log index among other peers (excluding ourselves)
		maxPeerIndex := uint64(0)
		for _, peer := range allPeers {
			// Skip ourselves (compare by raft address)
			if peer.NodeID == r.discoverConfig.RaftAdvAddress {
				continue
			}
			if peer.RaftLogIndex > maxPeerIndex {
				maxPeerIndex = peer.RaftLogIndex
			}
		}

		// If peers have meaningful log history (> 0) and we have stale state, clear it
		// This handles the case where we're starting with old state but the cluster has moved on
		if maxPeerIndex > 0 {
			r.logger.Warn("Detected stale Raft state - clearing to allow clean cluster join",
				zap.Uint64("peer_max_log_index", maxPeerIndex),
				zap.String("data_dir", rqliteDataDir))

			if err := r.clearRaftState(rqliteDataDir); err != nil {
				r.logger.Error("Failed to clear Raft state", zap.Error(err))
				// Continue anyway - rqlite might still be able to recover
			} else {
				// Force write peers.json after clearing stale state
				if r.discoveryService != nil {
					r.logger.Info("Force writing peers.json after clearing stale Raft state")
					if err := r.discoveryService.ForceWritePeersJSON(); err != nil {
						r.logger.Error("Failed to force write peers.json after clearing stale state", zap.Error(err))
					}
				}
			}
		}
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
			// Log at debug level if validation fails - not critical
			r.logger.Debug("Node ID validation skipped (endpoint unavailable)", zap.Error(err))
			return nil
		}

		expectedID := r.discoverConfig.RaftAdvAddress
		if expectedID == "" {
			return fmt.Errorf("raft_adv_address not configured")
		}

		// If cluster is still forming, nodes list might be empty - that's okay
		if len(nodes) == 0 {
			r.logger.Debug("Node ID validation skipped (cluster not yet formed)")
			return nil
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
				r.logger.Debug("Node ID validation passed",
					zap.String("node_id", node.ID),
					zap.String("address", node.Address))
				return nil
			}
		}

		// If we can't find ourselves but other nodes exist, cluster might still be forming
		// This is fine - don't log a warning
		r.logger.Debug("Node ID validation skipped (node not yet in cluster membership)",
			zap.String("expected_address", expectedID),
			zap.Int("nodes_in_cluster", len(nodes)))
		return nil
	}

	return nil
}
