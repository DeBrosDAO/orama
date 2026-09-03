// Package nodehealth answers one question in one place: is this node actually
// carrying its share of the cluster?
//
// The answer used to be given four different ways, and the one that mattered
// most — the gate between two nodes in a rolling upgrade — was both wrong and
// non-fatal. It queried a port nothing had listened on since the port
// migration, burned two minutes failing, printed "Cluster health check
// warning ... Continuing", and the rollout moved on to restart the next voter.
// That is how a rolling upgrade takes out a quorum.
//
// Every readiness gate in the fleet now comes through here.
package nodehealth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Target is where to reach one node's own endpoints.
//
// Base URLs rather than a host, because the two callers reach a node
// differently: on the node itself these are localhost, and over the WireGuard
// overlay they are 10.0.0.x.
type Target struct {
	// RQLiteBase is the rqlite HTTP base, e.g. "http://localhost:10100".
	RQLiteBase string

	// GatewayBase is the gateway HTTP base, e.g. "http://localhost:10104".
	// Empty skips the gateway check — correct for a node whose gateway is
	// deliberately not running, and never used to skip it silently otherwise.
	GatewayBase string

	// RQLite basic-auth credentials. Empty is correct while rqlited runs
	// without -auth; supplying them means this gate keeps working when
	// enforcement is switched on, rather than reading a 401 as "the node is
	// down" and stopping a rollout that was fine.
	RQLiteUser string
	RQLitePass string
}

// Options tunes what "ready" means for a particular caller.
type Options struct {
	// Budget is the total time allowed. Zero means DefaultBudget.
	Budget time.Duration

	// MaxIndexLag is how far this node's applied index may trail the leader's
	// commit index and still count as ready. Zero means DefaultMaxIndexLag.
	//
	// A node that is a Follower but 40,000 entries behind is not carrying
	// reads; restarting the next voter while it catches up is how a rolling
	// upgrade ends up with one usable copy of the data.
	MaxIndexLag uint64

	// RequireLeaderKnown fails the check while the node reports no leader.
	// True for a rollout gate: no leader means no quorum, and continuing is
	// the specific mistake this package exists to prevent.
	RequireLeaderKnown bool
}

// Defaults. Generous, because every one of these is waiting on a machine that
// has just restarted a database.
const (
	DefaultBudget      = 3 * time.Minute
	DefaultMaxIndexLag = 200

	pollInterval   = 2 * time.Second
	httpTimeout    = 5 * time.Second
	handshakeFresh = 3 * time.Minute
)

// Status is one observation of a node.
type Status struct {
	RaftState    string
	LeaderID     string
	AppliedIndex uint64
	CommitIndex  uint64
	GatewayOK    bool
}

// Ready reports whether this observation satisfies opts, and why not.
func (s Status) Ready(opts Options) error {
	switch strings.ToLower(s.RaftState) {
	case "leader", "follower":
	default:
		return fmt.Errorf("raft state is %q, want Leader or Follower", s.RaftState)
	}

	if opts.RequireLeaderKnown && s.LeaderID == "" {
		return fmt.Errorf("raft state is %s but the node reports no leader — the cluster has no quorum", s.RaftState)
	}

	maxLag := opts.MaxIndexLag
	if maxLag == 0 {
		maxLag = DefaultMaxIndexLag
	}
	if s.CommitIndex > s.AppliedIndex && s.CommitIndex-s.AppliedIndex > maxLag {
		return fmt.Errorf("applied index %d trails commit index %d by %d entries (limit %d) — still catching up",
			s.AppliedIndex, s.CommitIndex, s.CommitIndex-s.AppliedIndex, maxLag)
	}

	if !s.GatewayOK {
		return fmt.Errorf("raft is healthy but the gateway is not serving /health")
	}
	return nil
}

// WaitReady blocks until the node satisfies opts, or the budget runs out.
//
// The timeout error carries the last observation, not the word "timeout": what
// an operator needs is "still Candidate" or "trails by 40000 entries", which
// says what to do next.
func WaitReady(ctx context.Context, t Target, opts Options) error {
	budget := opts.Budget
	if budget == 0 {
		budget = DefaultBudget
	}

	client := &http.Client{Timeout: httpTimeout}
	deadline := time.Now().Add(budget)

	var last error

	for {
		status, err := Observe(ctx, client, t)
		if err == nil {
			last = status.Ready(opts)
			if last == nil {
				return nil
			}
		} else {
			last = err
		}

		if ctx.Err() != nil {
			return fmt.Errorf("nodehealth: cancelled (last: %v)", last)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nodehealth: not ready after %s: %w", budget, last)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("nodehealth: cancelled (last: %v)", last)
		case <-time.After(pollInterval):
		}
	}
}

// Observe takes one reading. Exported so a report command can show the state
// without waiting for it to become good.
func Observe(ctx context.Context, client *http.Client, t Target) (Status, error) {
	// Through the admin client, so this gate carries credentials. A 401 read
	// as "the node is down" would stop a rollout that was fine — and the
	// rollout gate is the last place that should misdiagnose a healthy node.
	status, err := rqlite.NewAdminClient(t.RQLiteBase, t.RQLiteUser, t.RQLitePass).Status(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("read rqlite status: %w", err)
	}

	s := Status{
		RaftState:    status.Store.Raft.State,
		LeaderID:     status.Store.Raft.LeaderID,
		AppliedIndex: status.Store.Raft.AppliedIndex,
		CommitIndex:  status.Store.Raft.CommitIndex,
	}

	// No gateway base means the caller has said this node has no gateway to
	// check — not that the check passed by default.
	if t.GatewayBase == "" {
		s.GatewayOK = true
		return s, nil
	}
	if _, err := get(ctx, client, t.GatewayBase+"/health"); err == nil {
		s.GatewayOK = true
	}
	return s, nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}
	if readErr != nil {
		return nil, fmt.Errorf("read %s: %w", url, readErr)
	}
	return body, nil
}
