package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"
)

// killOrphanedRQLite kills any orphaned rqlited process still holding the port.
// This can happen when the parent node process crashes and rqlited keeps running.
func (r *RQLiteManager) killOrphanedRQLite() {
	// Check if port is already in use by querying the status endpoint
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return // Port not in use, nothing to clean up
	}
	resp.Body.Close()

	// Port is in use — find and kill the orphaned process
	r.logger.Warn("Found orphaned rqlited process on port, killing it",
		zap.Int("port", r.config.RQLitePort))

	// Use fuser to find and kill the process holding the port
	cmd := exec.Command("fuser", "-k", fmt.Sprintf("%d/tcp", r.config.RQLitePort))
	if err := cmd.Run(); err != nil {
		r.logger.Warn("fuser failed, trying lsof", zap.Error(err))
		// Fallback: use lsof
		out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", r.config.RQLitePort)).Output()
		if err == nil {
			for _, pidStr := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if pidStr != "" {
					killCmd := exec.Command("kill", "-9", pidStr)
					killCmd.Run()
				}
			}
		}
	}

	// Wait for port to be released
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(url)
		if err != nil {
			return // Port released
		}
		resp.Body.Close()
	}
	r.logger.Warn("Could not release port from orphaned process")
}

// launchProcess starts the RQLite process with appropriate arguments
func (r *RQLiteManager) launchProcess(ctx context.Context, rqliteDataDir string) error {
	// Kill any orphaned rqlited from a previous crash
	r.killOrphanedRQLite()

	// Remove stale peers.json from the raft directory to prevent rqlite v8
	// from triggering automatic Raft recovery on normal restarts.
	//
	// Only delete when raft.db EXISTS (normal restart). If raft.db does NOT
	// exist, peers.json was likely placed intentionally by ForceWritePeersJSON()
	// as part of a recovery flow (clearRaftState + ForceWritePeersJSON + launch).
	stalePeersPath := filepath.Join(rqliteDataDir, "raft", "peers.json")
	raftDBPath := filepath.Join(rqliteDataDir, "raft.db")
	if _, err := os.Stat(stalePeersPath); err == nil {
		if _, err := os.Stat(raftDBPath); err == nil {
			// raft.db exists → this is a normal restart, peers.json is stale
			r.logger.Warn("Removing stale peers.json from raft directory to prevent accidental recovery",
				zap.String("path", stalePeersPath))
			_ = os.Remove(stalePeersPath)
			_ = os.Remove(stalePeersPath + ".backup")
			_ = os.Remove(stalePeersPath + ".tmp")
		} else {
			// raft.db missing → intentional recovery, keep peers.json for rqlited
			r.logger.Info("Keeping peers.json in raft directory for intentional cluster recovery",
				zap.String("path", stalePeersPath))
		}
	}

	// Build RQLite command
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLitePort),
		"-http-adv-addr", r.discoverConfig.HttpAdvAddress,
		"-raft-adv-addr", r.discoverConfig.RaftAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLiteRaftPort),
	}

	if r.config.NodeCert != "" && r.config.NodeKey != "" {
		r.logger.Info("Enabling node-to-node TLS encryption",
			zap.String("node_cert", r.config.NodeCert),
			zap.String("node_key", r.config.NodeKey))

		args = append(args, "-node-cert", r.config.NodeCert)
		args = append(args, "-node-key", r.config.NodeKey)

		if r.config.NodeCACert != "" {
			args = append(args, "-node-ca-cert", r.config.NodeCACert)
		}
		if r.config.NodeNoVerify {
			args = append(args, "-node-no-verify")
		}
	}

	// Raft tuning — higher timeouts suit WireGuard latency
	raftElection := r.config.RaftElectionTimeout
	if raftElection == 0 {
		raftElection = 5 * time.Second
	}
	raftHeartbeat := r.config.RaftHeartbeatTimeout
	if raftHeartbeat == 0 {
		raftHeartbeat = 2 * time.Second
	}
	raftApply := r.config.RaftApplyTimeout
	if raftApply == 0 {
		raftApply = 30 * time.Second
	}
	raftLeaderLease := r.config.RaftLeaderLeaseTimeout
	if raftLeaderLease == 0 {
		raftLeaderLease = 2 * time.Second
	}
	args = append(args,
		"-raft-election-timeout", raftElection.String(),
		"-raft-timeout", raftHeartbeat.String(),
		"-raft-apply-timeout", raftApply.String(),
		"-raft-leader-lease-timeout", raftLeaderLease.String(),
	)

	// RQLite HTTP Basic Auth — when auth file exists, enforce authentication
	if r.config.RQLiteAuthFile != "" {
		r.logger.Info("Enabling RQLite HTTP Basic Auth",
			zap.String("auth_file", r.config.RQLiteAuthFile))
		args = append(args, "-auth", r.config.RQLiteAuthFile)
	}

	if r.config.RQLiteJoinAddress != "" && !r.hasExistingState(rqliteDataDir) {
		r.logger.Info("First-time join to RQLite cluster", zap.String("join_address", r.config.RQLiteJoinAddress))

		joinArg := r.config.RQLiteJoinAddress
		if strings.HasPrefix(joinArg, "http://") {
			joinArg = strings.TrimPrefix(joinArg, "http://")
		} else if strings.HasPrefix(joinArg, "https://") {
			joinArg = strings.TrimPrefix(joinArg, "https://")
		}

		joinTimeout := 5 * time.Minute
		if err := r.waitForJoinTarget(ctx, r.config.RQLiteJoinAddress, joinTimeout); err != nil {
			r.logger.Warn("Join target did not become reachable within timeout; will still attempt to join",
				zap.Error(err))
		}

		args = append(args, "-join", joinArg, "-join-as", r.discoverConfig.RaftAdvAddress, "-join-attempts", "30", "-join-interval", "10s")

		// Check if this node should join as a non-voter (read replica).
		// Query the join target's /nodes endpoint to count existing voters,
		// rather than relying on LibP2P peer count which is incomplete at join time.
		if shouldBeNonVoter := r.checkShouldBeNonVoter(r.config.RQLiteJoinAddress); shouldBeNonVoter {
			r.logger.Info("Joining as non-voter (read replica) - cluster already has max voters",
				zap.String("raft_address", r.discoverConfig.RaftAdvAddress),
				zap.Int("max_voters", MaxDefaultVoters))
			args = append(args, "-raft-non-voter")
		}
	}

	args = append(args, rqliteDataDir)

	r.cmd = exec.Command("rqlited", args...)

	nodeType := r.nodeType
	if nodeType == "" {
		nodeType = "node"
	}

	logsDir := filepath.Join(filepath.Dir(r.dataDir), "logs")
	_ = os.MkdirAll(logsDir, 0755)

	logPath := filepath.Join(logsDir, fmt.Sprintf("rqlite-%s.log", nodeType))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	r.cmd.Stdout = logFile
	r.cmd.Stderr = logFile

	if err := r.cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	// Write PID file for reliable orphan detection
	pidPath := filepath.Join(logsDir, "rqlited.pid")
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", r.cmd.Process.Pid)), 0644)
	r.logger.Info("RQLite process started", zap.Int("pid", r.cmd.Process.Pid), zap.String("pid_file", pidPath))

	// Reap the child process in the background to prevent zombies.
	// Stop() waits on this channel instead of calling cmd.Wait() directly.
	r.waitDone = make(chan struct{})
	go func() {
		_ = r.cmd.Wait()
		logFile.Close()
		close(r.waitDone)
	}()

	return nil
}

// waitForReadyAndConnect waits for the local rqlited to be participating in
// raft and then opens the connection. Used by the recovery path, which restarts
// the unit and needs both halves before it can report success.
func (r *RQLiteManager) waitForReadyAndConnect(ctx context.Context) error {
	if err := r.waitForReady(ctx); err != nil {
		return err
	}
	return r.connect(ctx)
}

// connectMaxAttempts and connectBaseBackoff bound one connect call. "store is
// not open" is the only retryable failure: rqlited has bound its HTTP port but
// has not finished opening its SQLite store yet.
const (
	connectMaxAttempts   = 10
	connectBaseBackoff   = 1 * time.Second
	connectMaxBackoff    = 5 * time.Second
	connectBackoffGrowth = 1.5
)

// connect opens the local gorqlite connection.
//
// It does NOT require a raft leader: the point of opening the connection early
// is that a node without quorum can still read its own replica. Callers that
// need leader-routed reads wait for them separately.
func (r *RQLiteManager) connect(ctx context.Context) error {
	// Use disableClusterDiscovery=true to avoid gorqlite calling /nodes on Open().
	// The /nodes endpoint probes all cluster members including unreachable ones,
	// which can block for the full HTTP timeout (~10s per attempt).
	// This is safe because rqlited followers automatically forward writes to the leader.
	connURL := fmt.Sprintf("http://localhost:%d?disableClusterDiscovery=true", r.config.RQLitePort)

	backoff := connectBaseBackoff
	var lastErr error

	for attempt := 0; attempt < connectMaxAttempts; attempt++ {
		conn, err := gorqlite.Open(connURL)
		if err == nil {
			r.setConnection(conn)
			_ = r.validateNodeID()
			return nil
		}
		lastErr = err

		if !strings.Contains(err.Error(), "store is not open") {
			return fmt.Errorf("failed to connect to RQLite: %w", err)
		}

		r.logger.Debug("RQLite store not open yet, retrying",
			zap.Int("attempt", attempt+1),
			zap.Error(err))

		select {
		case <-ctx.Done():
			return fmt.Errorf("connecting to RQLite cancelled after %d attempts (last error: %v): %w", attempt+1, lastErr, ctx.Err())
		case <-time.After(backoff):
		}
		backoff = time.Duration(float64(backoff) * connectBackoffGrowth)
		if backoff > connectMaxBackoff {
			backoff = connectMaxBackoff
		}
	}

	return fmt.Errorf("RQLite store still not open after %d attempts: %w", connectMaxAttempts, lastErr)
}

// rqliteReadyTimeout matches the previous 180 one-second attempts.
const rqliteReadyTimeout = 3 * time.Minute

// waitForReady waits until the local rqlited is participating in raft.
func (r *RQLiteManager) waitForReady(ctx context.Context) error {
	return WaitForRaftReady(ctx, r.config.RQLitePort, rqliteReadyTimeout)
}

// waitForSQLAvailable waits until a simple query succeeds
func (r *RQLiteManager) waitForSQLAvailable(ctx context.Context) error {
	r.logger.Info("Waiting for SQL to become available...")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			r.logger.Error("waitForSQLAvailable timed out", zap.Int("attempts", attempts))
			return ctx.Err()
		case <-ticker.C:
			attempts++
			conn := r.GetConnection()
			if conn == nil {
				r.logger.Warn("connection is nil in waitForSQLAvailable")
				continue
			}
			_, err := conn.QueryOne("SELECT 1")
			if err == nil {
				r.logger.Info("SQL is available", zap.Int("attempts", attempts))
				return nil
			}
			if attempts <= 5 || attempts%10 == 0 {
				r.logger.Debug("SQL not yet available", zap.Int("attempt", attempts), zap.Error(err))
			}
		}
	}
}

// testJoinAddress tests if a join address is reachable
func (r *RQLiteManager) testJoinAddress(joinAddress string) error {
	client := tlsutil.NewHTTPClient(5 * time.Second)
	var statusURL string
	if strings.HasPrefix(joinAddress, "http://") || strings.HasPrefix(joinAddress, "https://") {
		statusURL = strings.TrimRight(joinAddress, "/") + "/status"
	} else {
		host := joinAddress
		if idx := strings.Index(joinAddress, ":"); idx != -1 {
			host = joinAddress[:idx]
		}
		statusURL = fmt.Sprintf("http://%s:%d/status", host, constants.RQLiteHTTPPort)
	}

	resp, err := client.Get(statusURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("leader returned status %d", resp.StatusCode)
	}
	return nil
}

// checkShouldBeNonVoter queries the join target's /nodes endpoint to count
// existing voters. Returns true if the cluster already has MaxDefaultVoters
// voters, meaning this node should join as a non-voter.
func (r *RQLiteManager) checkShouldBeNonVoter(joinAddress string) bool {
	// Derive HTTP API URL from the join address (which is a raft address like 10.0.0.1:10101)
	host := joinAddress
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimPrefix(host, "https://")
	}
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	nodesURL := fmt.Sprintf("http://%s:%d/nodes?timeout=2s", host, r.config.RQLitePort)

	// Retry with backoff — network (WireGuard) may not be ready immediately.
	// Defaulting to voter on failure is dangerous: it creates excess voters
	// that can cause split-brain during leader failover.
	const maxRetries = 5
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*2) * time.Second
			r.logger.Info("Retrying voter check",
				zap.Int("attempt", attempt+1),
				zap.Duration("delay", delay))
			time.Sleep(delay)
		}

		voterCount, err := r.queryVoterCount(nodesURL)
		if err != nil {
			lastErr = err
			continue
		}

		r.logger.Info("Checked existing voter count from join target",
			zap.Int("reachable_voters", voterCount),
			zap.Int("max_voters", MaxDefaultVoters))

		return voterCount >= MaxDefaultVoters
	}

	r.logger.Warn("Could not determine voter count after retries, defaulting to voter",
		zap.Int("attempts", maxRetries),
		zap.Error(lastErr))
	return false
}

// queryVoterCount queries the /nodes endpoint and returns the number of reachable voters.
func (r *RQLiteManager) queryVoterCount(nodesURL string) (int, error) {
	client := tlsutil.NewHTTPClient(5 * time.Second)
	resp, err := client.Get(nodesURL)
	if err != nil {
		return 0, fmt.Errorf("query /nodes: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read /nodes response: %w", err)
	}

	var nodes map[string]struct {
		Voter     bool `json:"voter"`
		Reachable bool `json:"reachable"`
	}
	if err := json.Unmarshal(body, &nodes); err != nil {
		return 0, fmt.Errorf("parse /nodes response: %w", err)
	}

	voterCount := 0
	for _, n := range nodes {
		if n.Voter && n.Reachable {
			voterCount++
		}
	}

	return voterCount, nil
}

// waitForJoinTarget waits until the join target's HTTP status becomes reachable
func (r *RQLiteManager) waitForJoinTarget(ctx context.Context, joinAddress string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := r.testJoinAddress(joinAddress); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return lastErr
}
