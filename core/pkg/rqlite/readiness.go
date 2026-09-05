package rqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

const (
	// readinessPollInterval is how often WaitForLeader re-probes. Short enough
	// that a normal boot (leader already elected) proceeds immediately, long
	// enough not to hammer a node that is still replaying its raft log.
	readinessPollInterval = 500 * time.Millisecond

	// readinessProbe is the cheapest statement that still proves the node can
	// serve a leader-routed read: it needs a leader, but touches no table, so
	// it works before any migration has run.
	readinessProbe = "SELECT 1"
)

// WaitForLeader blocks until the rqlite behind db can serve a leader-routed
// read, or until timeout elapses.
//
// Why this exists: opening a database/sql handle to rqlite establishes nothing
// — sql.Open is lazy, and rqlite accepts connections long before it has
// elected a leader. Schema migrations issued into that window fail with
// "leader not found", and (before SafeExecContext was applied to the migration
// path) crashed the process outright. systemd's After=/Requires= does not close
// the gap: for Type=simple units it only orders process *start*, which says
// nothing about whether the database is usable.
//
// So the ordering is enforced here, against the real readiness signal, rather
// than assumed from unit ordering or slept on.
//
// A nil db is a programming error and returns immediately.
func WaitForLeader(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	if db == nil {
		return fmt.Errorf("rqlite.WaitForLeader: nil database handle")
	}

	deadline := time.Now().Add(timeout)
	attempts := 0
	var lastErr error

	for {
		attempts++
		// A per-probe budget keeps one hung request from eating the whole
		// window; the loop retries until the outer deadline instead.
		probeCtx, cancel := context.WithTimeout(ctx, readinessPollInterval*4)
		rows, err := SafeQueryContext(db, probeCtx, readinessProbe)
		if err == nil {
			rows.Close()
			cancel()
			return nil
		}
		cancel()
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("rqlite.WaitForLeader: cancelled after %d attempts (last error: %v): %w", attempts, lastErr, ctxErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rqlite.WaitForLeader: no leader after %s and %d attempts — "+
				"check that the local rqlited is running and that this node can reach its raft peers "+
				"over the WireGuard mesh (last error: %w)", timeout, attempts, lastErr)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("rqlite.WaitForLeader: cancelled after %d attempts (last error: %v): %w", attempts, lastErr, ctx.Err())
		case <-time.After(readinessPollInterval):
		}
	}
}

// raftReadyPollInterval is how often WaitForRaftReady re-reads /status. Short
// enough that an already-converged node proceeds almost immediately.
const raftReadyPollInterval = 500 * time.Millisecond

// WaitForRaftReady blocks until the rqlite on port reports a raft state in
// which it is actually participating in the cluster - Leader or Follower - or
// until timeout elapses.
//
// Why the state and not the port: rqlited binds its HTTP listener before it has
// joined anything, so "the port answers" is true of a node that is still
// Candidate, still replaying its log, or still retrying a join. Both readiness
// checks in this package used to accept the first HTTP 200 (one of them by
// reading a top-level "raft" key that rqlite does not emit - it nests under
// store.raft - and returning success from the else branch when the assertion
// failed). Boot then continued past "RQLite ready" before consensus existed,
// and the real wait landed in a much longer, fatal timeout further down.
//
// An unreadable or unparseable status is NOT readiness: it keeps polling and,
// on timeout, reports the last state it managed to observe.
func WaitForRaftReady(ctx context.Context, port int, timeout time.Duration) error {
	client := tlsutil.NewHTTPClient(2 * time.Second)
	url := fmt.Sprintf("http://localhost:%d/status", port)

	deadline := time.Now().Add(timeout)
	lastState := "unknown"
	var lastErr error

	for {
		state, err := readRaftState(ctx, client, url)
		if err == nil {
			lastState = state
			switch strings.ToLower(state) {
			case "leader", "follower":
				return nil
			}
		} else {
			lastErr = err
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("rqlite.WaitForRaftReady: cancelled on port %d (last state %q): %w", port, lastState, ctxErr)
		}
		if time.Now().After(deadline) {
			if lastErr != nil && lastState == "unknown" {
				return fmt.Errorf("rqlite.WaitForRaftReady: port %d never reported a raft state within %s (last error: %w)", port, timeout, lastErr)
			}
			return fmt.Errorf("rqlite.WaitForRaftReady: port %d still in raft state %q after %s (want Leader or Follower)", port, lastState, timeout)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("rqlite.WaitForRaftReady: cancelled on port %d (last state %q): %w", port, lastState, ctx.Err())
		case <-time.After(raftReadyPollInterval):
		}
	}
}

// RaftState reports the raft state of the rqlite at hostPort — Leader,
// Follower, Candidate or Shutdown. Exposed for diagnostics: a caller that is
// waiting for readiness wants to log WHY it is waiting, and "Candidate" is a
// very different answer from "the port is not answering".
//
// It takes a host:port rather than a port because the caller's rqlite is not
// always local — a namespace gateway can be configured against a remote DSN,
// and reporting the LOCAL node's raft state under a remote address would be
// confidently wrong rather than merely unknown.
func RaftState(ctx context.Context, hostPort string) (string, error) {
	return readRaftState(ctx, tlsutil.NewHTTPClient(raftStateTimeout),
		fmt.Sprintf("http://%s/status", hostPort))
}

// raftStateTimeout bounds a single RaftState probe. It only ever talks to
// localhost.
const raftStateTimeout = 2 * time.Second

// readRaftState fetches /status and returns store.raft.state.
func readRaftState(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var status RQLiteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return "", fmt.Errorf("decode status: %w", err)
	}
	if status.Store.Raft.State == "" {
		return "", fmt.Errorf("status carried no store.raft.state")
	}
	return status.Store.Raft.State, nil
}
