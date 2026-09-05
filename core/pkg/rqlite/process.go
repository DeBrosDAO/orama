package rqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"
)

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
			// Logged, not returned: an id mismatch does not stop this node
			// working, it means the cluster is carrying a duplicate entry for
			// it. Discarding the result silently — which is what this used to
			// do — is how a 5-voter cluster ends up needing 4 of 7 to agree
			// with nobody having been told.
			if idErr := r.validateNodeID(); idErr != nil {
				r.logger.Error("RQLite raft membership carries a stale entry for this node; "+
					"quorum arithmetic is counting a member that no longer exists at that address",
					zap.String("advertise_address", r.discoverConfig.RaftAdvAddress),
					zap.Error(idErr))
			}
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
