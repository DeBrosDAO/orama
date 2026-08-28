package rqlite

// freshness.go implements the in-process "is the local follower fresh enough
// to serve a none-read?" gate behind bug #1022.
//
// Why this exists: a per-namespace gateway reads its OWN local rqlite follower
// at level=none for speed (~1ms, no leader hop). When that follower falls
// minutes behind the raft leader (network partition, slow replay), none-reads
// silently serve stale rows and recipients miss messages. This gate inspects
// the local node's raft /status and, when the follower is stale, lets the read
// path AUTO-DEGRADE to the weak (leader-routed) connection — transparent to
// callers, always correct.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// StalenessMaxLastContact is the maximum time-since-leader-contact a
	// follower may report and still be trusted for a none-read. Beyond this the
	// follower is treated as stale and none-reads degrade to the weak conn.
	StalenessMaxLastContact = 2 * time.Second

	// StalenessMaxApplyGap is the maximum (commit_index - applied_index) a
	// follower may have unreplayed and still be trusted for a none-read. A large
	// gap means committed entries the leader knows about have not yet been
	// applied locally, so a none-read could miss them.
	StalenessMaxApplyGap uint64 = 50

	// staleNeverContact is the synthetic last_contact duration used when rqlite
	// reports "never" or an unparseable value — far above any real bound so the
	// follower is always judged stale (fail-safe).
	staleNeverContact = time.Duration(1<<62 - 1)

	// defaultFreshnessGateTTL is how long a freshness verdict is cached before
	// re-checking /status, so a hot read loop does not hammer the status
	// endpoint once per batch.
	defaultFreshnessGateTTL = 250 * time.Millisecond
)

// LocalFollowerFresh reports whether the local rqlite node at the given status
// port is fresh enough to serve a none-read. A Leader is always fresh (it holds
// the authoritative state). A Follower is fresh only when its last contact with
// the leader is recent AND its raft apply gap is small. Any error reaching
// /status returns (false, reason, err) — callers must treat that as NOT fresh.
func LocalFollowerFresh(port int) (fresh bool, reason string, err error) {
	status, err := GetRaftStatus(port)
	if err != nil {
		return false, fmt.Sprintf("status query failed on port %d: %v", port, err), fmt.Errorf("LocalFollowerFresh: %w", err)
	}
	raft := status.Store.Raft
	if strings.EqualFold(raft.State, "Leader") {
		return true, "leader", nil
	}
	lastContact := parseLastContact(raft.LastContact.String())
	if lastContact > StalenessMaxLastContact {
		return false, fmt.Sprintf("follower last_contact=%q exceeds max %s (port %d) — degrading none-read to leader-routed weak", raft.LastContact, StalenessMaxLastContact, port), nil
	}
	// Guard underflow: only meaningful when commit has advanced past applied.
	if raft.CommitIndex >= raft.AppliedIndex {
		if gap := raft.CommitIndex - raft.AppliedIndex; gap > StalenessMaxApplyGap {
			return false, fmt.Sprintf("follower apply gap=%d exceeds max %d (commit=%d applied=%d, port %d) — degrading none-read to leader-routed weak", gap, StalenessMaxApplyGap, raft.CommitIndex, raft.AppliedIndex, port), nil
		}
	}
	return true, "follower fresh", nil
}

// parseLastContact converts rqlite's last_contact string into a duration.
// "never" or any unparseable value maps to staleNeverContact (effectively
// infinite) so the follower is judged stale — never accidentally fresh.
func parseLastContact(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "never") {
		return staleNeverContact
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return staleNeverContact
	}
	return d
}

// followerFreshnessGate caches the freshness verdict for a single local node so
// a burst of none-reads shares one /status check per ttl window. FAIL-SAFE: a
// check error caches fresh=false (degrade to weak) — an error is never fresh.
type followerFreshnessGate struct {
	port  int
	check func(int) (bool, string, error)
	ttl   time.Duration

	mu     sync.Mutex
	at     time.Time
	fresh  bool
	reason string
}

// newFollowerFreshnessGate builds a gate for the local status port. A nil check
// defaults to LocalFollowerFresh; a non-positive ttl defaults to 250ms.
func newFollowerFreshnessGate(port int, check func(int) (bool, string, error), ttl time.Duration) *followerFreshnessGate {
	if check == nil {
		check = LocalFollowerFresh
	}
	if ttl <= 0 {
		ttl = defaultFreshnessGateTTL
	}
	return &followerFreshnessGate{port: port, check: check, ttl: ttl}
}

// Fresh returns the cached verdict, re-checking only when the cache has aged
// past ttl. On a check error the verdict is cached as not-fresh with the error
// in the reason, so the read path degrades to the weak connection.
func (g *followerFreshnessGate) Fresh() (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.at.IsZero() && time.Since(g.at) < g.ttl {
		return g.fresh, g.reason
	}
	fresh, reason, err := g.check(g.port)
	if err != nil {
		fresh = false
		reason = fmt.Sprintf("freshness check error (fail-safe to weak): %v", err)
	}
	g.fresh = fresh
	g.reason = reason
	g.at = time.Now()
	return g.fresh, g.reason
}
