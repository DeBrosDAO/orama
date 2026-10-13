package rqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func voter(id string, reachable bool) raftMember {
	return raftMember{ID: id, Voter: true, Reachable: reachable}
}

func nonVoter(id string, reachable bool) raftMember {
	return raftMember{ID: id, Voter: false, Reachable: reachable}
}

func TestSafeToEvict(t *testing.T) {
	tests := []struct {
		name    string
		members []raftMember
		target  string
		allowed bool
	}{
		{
			// The case this exists for: a VPS deleted without ceremony. Two of
			// three voters answer, so the cluster has a leader and can commit
			// the removal; afterwards two of two answer.
			name:    "one dead voter of three",
			members: []raftMember{voter("a", true), voter("b", true), voter("c", false)},
			target:  "c",
			allowed: true,
		},
		{
			name:    "one dead voter of five",
			members: []raftMember{voter("a", true), voter("b", true), voter("c", true), voter("d", true), voter("e", false)},
			target:  "e",
			allowed: true,
		},
		{
			// Two dead of five: removing one leaves 3 of 4, quorum 3. Tight,
			// but committable — and it is the step that makes the next one safe.
			name:    "two dead voters of five, evicting the first",
			members: []raftMember{voter("a", true), voter("b", true), voter("c", true), voter("d", false), voter("e", false)},
			target:  "d",
			allowed: true,
		},
		{
			name:    "a reachable member is demoted, never evicted",
			members: []raftMember{voter("a", true), voter("b", true), voter("c", true)},
			target:  "c",
			allowed: false,
		},
		{
			// Nothing is gained: a non-voter does not count toward quorum, so
			// removing it is risk without benefit.
			name:    "a dead non-voter is left alone",
			members: []raftMember{voter("a", true), voter("b", true), nonVoter("c", false)},
			target:  "c",
			allowed: false,
		},
		{
			name:    "unknown member",
			members: []raftMember{voter("a", true), voter("b", true)},
			target:  "zz",
			allowed: false,
		},
		{
			name:    "the last voter is never removed",
			members: []raftMember{voter("a", false)},
			target:  "a",
			allowed: false,
		},
		{
			// Three of four dead. Removing one leaves 1 of 3 reachable against
			// a quorum of 2 — the cluster could not commit, and the removal
			// would be an unrecoverable step in the wrong direction.
			name:    "removal that leaves no quorum is refused",
			members: []raftMember{voter("a", true), voter("b", false), voter("c", false), voter("d", false)},
			target:  "b",
			allowed: false,
		},
		{
			name:    "non-voters do not count toward quorum",
			members: []raftMember{voter("a", true), voter("b", false), nonVoter("c", true), nonVoter("d", true)},
			target:  "b",
			allowed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			refusal := safeToEvict(tc.members, tc.target)
			if allowed := refusal == ""; allowed != tc.allowed {
				t.Fatalf("safeToEvict(%s) allowed = %v (%q), want %v", tc.target, allowed, refusal, tc.allowed)
			}
			if !tc.allowed && refusal == "" {
				t.Error("a refusal must carry a reason")
			}
		})
	}
}

// Raft has no memory of how long a member has been unreachable, so the
// reconciler keeps the streak itself.
func TestUnreachableStreaks(t *testing.T) {
	s := unreachableStreaks{}

	// Short outages never reach the threshold.
	for i := 0; i < deadVoterTicks-1; i++ {
		if expired := s.observe([]raftMember{voter("a", false)}); len(expired) != 0 {
			t.Fatalf("tick %d: expired too early: %v", i, expired)
		}
	}
	expired := s.observe([]raftMember{voter("a", false)})
	if len(expired) != 1 || expired[0] != "a" {
		t.Fatalf("expired = %v, want [a]", expired)
	}

	// One good tick resets the count: a node that answered is not dead.
	s.observe([]raftMember{voter("a", true)})
	if got := s["a"]; got != 0 {
		t.Fatalf("streak after a reachable tick = %d, want 0", got)
	}
	if expired := s.observe([]raftMember{voter("a", false)}); len(expired) != 0 {
		t.Fatalf("streak was not reset: %v", expired)
	}

	// A member that leaves the configuration is forgotten, so its stale count
	// cannot be applied to a node that later reuses the id.
	s.observe([]raftMember{voter("b", false)})
	if _, present := s["a"]; present {
		t.Error("a member absent from the configuration must be forgotten")
	}
}

func TestUnreachableStreaks_countsEachMemberSeparately(t *testing.T) {
	s := unreachableStreaks{}
	for i := 0; i < deadVoterTicks; i++ {
		s.observe([]raftMember{voter("a", false), voter("b", true), voter("c", false)})
	}
	expired := s.observe([]raftMember{voter("a", false), voter("b", true), voter("c", false)})
	if len(expired) != 2 {
		t.Fatalf("expired = %v, want both dead members", expired)
	}
}

// evictionDB opens an in-memory database with the tables the eviction path
// reads and writes.
func evictionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
	CREATE TABLE node_health_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		observer_id TEXT NOT NULL,
		target_id TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE dns_nodes (
		id TEXT PRIMARY KEY,
		internal_ip TEXT,
		ip_address TEXT
	);
	CREATE TABLE raft_evicted_nodes (
		node_id TEXT PRIMARY KEY,
		raft_addr TEXT NOT NULL DEFAULT '',
		peer_id TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL,
		evicted_by TEXT NOT NULL DEFAULT '',
		evicted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func recordHealth(t *testing.T, db *sql.DB, observer, target, status string, age time.Duration) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO node_health_events (observer_id, target_id, status, created_at)
		 VALUES (?, ?, ?, datetime('now', ?))`,
		observer, target, status, formatSecondsAgo(age))
	if err != nil {
		t.Fatalf("insert health event: %v", err)
	}
}

// formatSecondsAgo is production's own modifier builder, used here so the two
// cannot drift. SQLite's datetime modifiers take "-N seconds"; Go's
// Duration.String() would produce "-1m0s", which SQLite rejects by returning
// NULL — silently dropping every row the comparison touches.
func formatSecondsAgo(d time.Duration) string {
	return secondsAgoModifier(d)
}

// The leader's own view is not enough evidence to change a raft configuration:
// a leader that has lost its route to one peer would otherwise evict a node the
// rest of the cluster can still see.
func TestConfirmedDeadByPeers(t *testing.T) {
	ctx := context.Background()

	t.Run("no events is not confirmation", func(t *testing.T) {
		db := evictionDB(t)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Error("an empty table must not confirm a death")
		}
	})

	t.Run("one observer is not enough", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "c", "dead", time.Minute)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Errorf("one observer confirmed a death; %d are required", deadVoterConfirmations)
		}
	})

	t.Run("two distinct observers confirm", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "c", "dead", time.Minute)
		recordHealth(t, db, "b", "c", "dead", 2*time.Minute)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if !confirmed {
			t.Error("two distinct observers should confirm")
		}
	})

	t.Run("the same observer twice is one observer", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "c", "dead", time.Minute)
		recordHealth(t, db, "a", "c", "dead", 2*time.Minute)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Error("one node reporting twice is not corroboration")
		}
	})

	t.Run("stale evidence does not count", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "c", "dead", 2*time.Hour)
		recordHealth(t, db, "b", "c", "dead", 3*time.Hour)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Error("evidence older than the window must not confirm a death")
		}
	})

	t.Run("a later recovery cancels the evidence", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "c", "dead", 10*time.Minute)
		recordHealth(t, db, "b", "c", "dead", 10*time.Minute)
		recordHealth(t, db, "a", "c", "recovered", time.Minute)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Error("a node that came back must not be evicted on the old evidence")
		}
	})

	t.Run("events about another node are irrelevant", func(t *testing.T) {
		db := evictionDB(t)
		recordHealth(t, db, "a", "d", "dead", time.Minute)
		recordHealth(t, db, "b", "d", "dead", time.Minute)
		confirmed, err := confirmedDeadByPeers(ctx, db, "c", deadVoterEvidenceWindow)
		if err != nil {
			t.Fatalf("confirmedDeadByPeers: %v", err)
		}
		if confirmed {
			t.Error("evidence about a different target must not confirm this one")
		}
	})

	t.Run("a nil handle is an error, not a confirmation", func(t *testing.T) {
		if _, err := confirmedDeadByPeers(ctx, nil, "c", deadVoterEvidenceWindow); err == nil {
			t.Error("a missing database handle must be reported, not read as 'no evidence'")
		}
	})
}

// A tombstone is what stops orphan recovery undoing a deliberate removal
// within five minutes.
func TestTombstones(t *testing.T) {
	ctx := context.Background()
	db := evictionDB(t)

	tombstoned, err := tombstonedNodes(ctx, db)
	if err != nil {
		t.Fatalf("tombstonedNodes: %v", err)
	}
	if len(tombstoned) != 0 {
		t.Fatalf("expected no tombstones, got %v", tombstoned)
	}

	if err := tombstoneNode(ctx, db, "10.0.0.9:10101", "10.0.0.9:10101", "peer-9", "dead-voter", "10.0.0.1:10101"); err != nil {
		t.Fatalf("tombstoneNode: %v", err)
	}

	tombstoned, err = tombstonedNodes(ctx, db)
	if err != nil {
		t.Fatalf("tombstonedNodes: %v", err)
	}
	if _, ok := tombstoned["10.0.0.9:10101"]; !ok {
		t.Fatalf("tombstone not recorded: %v", tombstoned)
	}

	// Re-evicting the same node must not fail on the primary key.
	if err := tombstoneNode(ctx, db, "10.0.0.9:10101", "10.0.0.9:10101", "peer-9", "operator", "10.0.0.2:10101"); err != nil {
		t.Fatalf("re-tombstoning must be idempotent: %v", err)
	}

	var reason string
	if err := db.QueryRow(`SELECT reason FROM raft_evicted_nodes WHERE node_id = ?`, "10.0.0.9:10101").Scan(&reason); err != nil {
		t.Fatalf("read reason: %v", err)
	}
	if reason != "operator" {
		t.Errorf("reason = %q, want the latest one", reason)
	}

	// A node that legitimately returns clears its own veto by joining.
	if err := clearTombstone(ctx, db, "10.0.0.9:10101"); err != nil {
		t.Fatalf("clearTombstone: %v", err)
	}
	tombstoned, err = tombstonedNodes(ctx, db)
	if err != nil {
		t.Fatalf("tombstonedNodes: %v", err)
	}
	if len(tombstoned) != 0 {
		t.Fatalf("tombstone survived a clear: %v", tombstoned)
	}
}

func TestTombstones_nilHandleIsAnError(t *testing.T) {
	ctx := context.Background()
	if err := tombstoneNode(ctx, nil, "a", "a", "", "dead-voter", "b"); err == nil {
		t.Error("writing a tombstone without a handle must be reported")
	}
	if _, err := tombstonedNodes(ctx, nil); err == nil {
		t.Error("reading tombstones without a handle must be reported")
	}
	if err := clearTombstone(ctx, nil, "a"); err == nil {
		t.Error("clearing a tombstone without a handle must be reported")
	}
}

// A membership change issued during an election can be applied against a
// configuration that is already gone.
func TestNoteTerm(t *testing.T) {
	v := &voterReconciler{cooldowns: map[string]time.Time{}, unreachable: unreachableStreaks{}}

	if v.noteTerm(7) {
		t.Fatal("the first observation of a term is not stability")
	}
	for i := 2; i < termsBeforeMembershipChange; i++ {
		if v.noteTerm(7) {
			t.Fatalf("tick %d reported stable too early", i)
		}
	}
	if !v.noteTerm(7) {
		t.Fatalf("term should be stable after %d unchanged ticks", termsBeforeMembershipChange)
	}

	// An election resets the count.
	if v.noteTerm(8) {
		t.Fatal("a term change must reset stability")
	}
	if v.noteTerm(8) {
		t.Fatal("stability must be re-earned after an election")
	}
}

// The two identifier spaces are different and it is not obvious. A raft node id
// here IS the raft advertise address (10.0.0.4:10101); node_health_events keys
// on the libp2p peer id, because the health monitor reads its node list from
// dns_nodes. Querying the health table with a raft address matches nothing,
// silently and for ever — which is exactly the shape of bug this test exists
// to prevent: an evidence gate that can never be satisfied and an eviction path
// that never fires.
//
// Every id below is realistically shaped for that reason. Synthetic ids that
// happen to match on both sides would let the mismatch through again.
func TestEvictionResolvesRaftAddressToPeerID(t *testing.T) {
	ctx := context.Background()
	db := evictionDB(t)

	const (
		deadRaftAddr = "10.0.0.9:10101"
		deadPeerID   = "12D3KooWDeadNodeNine"
		obsA         = "12D3KooWObserverA"
		obsB         = "12D3KooWObserverB"
	)

	if _, err := db.Exec(`INSERT INTO dns_nodes (id, internal_ip) VALUES (?, ?)`, deadPeerID, "10.0.0.9"); err != nil {
		t.Fatalf("seed dns_nodes: %v", err)
	}
	recordHealth(t, db, obsA, deadPeerID, "dead", time.Minute)
	recordHealth(t, db, obsB, deadPeerID, "dead", 2*time.Minute)

	// The raft address on its own corroborates nothing — this is the bug.
	confirmed, err := confirmedDeadByPeers(ctx, db, deadRaftAddr, deadVoterEvidenceWindow)
	if err != nil {
		t.Fatalf("confirmedDeadByPeers: %v", err)
	}
	if confirmed {
		t.Fatal("a raft address should not match peer-id-keyed health events; the test fixture is wrong")
	}

	// Resolved through dns_nodes, it does.
	peerID, err := peerIDForRaftAddress(ctx, db, deadRaftAddr)
	if err != nil {
		t.Fatalf("peerIDForRaftAddress: %v", err)
	}
	if peerID != deadPeerID {
		t.Fatalf("peerIDForRaftAddress = %q, want %q", peerID, deadPeerID)
	}

	confirmed, err = confirmedDeadByPeers(ctx, db, peerID, deadVoterEvidenceWindow)
	if err != nil {
		t.Fatalf("confirmedDeadByPeers: %v", err)
	}
	if !confirmed {
		t.Fatal("two observers of the resolved peer id should corroborate the death")
	}
}

func TestPeerIDForRaftAddress(t *testing.T) {
	ctx := context.Background()
	db := evictionDB(t)

	if _, err := db.Exec(`INSERT INTO dns_nodes (id, internal_ip) VALUES (?, ?)`, "12D3KooWFour", "10.0.0.4"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A node registered before internal_ip existed falls back to ip_address.
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, internal_ip, ip_address) VALUES (?, NULL, ?)`, "12D3KooWLegacy", "10.0.0.5"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	tests := []struct {
		name     string
		raftAddr string
		want     string
		wantErr  bool
	}{
		{"known node", "10.0.0.4:10101", "12D3KooWFour", false},
		{"internal_ip null falls back", "10.0.0.5:10101", "12D3KooWLegacy", false},
		{"unknown node", "10.0.0.99:10101", "", true},
		{"no port", "10.0.0.4", "", true},
		{"empty", "", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := peerIDForRaftAddress(ctx, db, tc.raftAddr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("peerIDForRaftAddress(%q) err = %v, wantErr %v", tc.raftAddr, err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("peerIDForRaftAddress(%q) = %q, want %q", tc.raftAddr, got, tc.want)
			}
		})
	}

	if _, err := peerIDForRaftAddress(ctx, nil, "10.0.0.4:10101"); err == nil {
		t.Error("a missing database handle must be reported")
	}
}

// An evicted node is the one node that cannot clear its own tombstone: it is
// outside the raft configuration, so its local rqlite has no leader and it
// cannot write to the cluster. Without expiry that is a permanent removal with
// no automatic way back — worse than the problem being solved.
func TestTombstonesExpire(t *testing.T) {
	ctx := context.Background()
	db := evictionDB(t)

	fresh := "10.0.0.8:10101"
	stale := "10.0.0.9:10101"

	if err := tombstoneNode(ctx, db, fresh, fresh, "", "dead-voter", "10.0.0.1:10101"); err != nil {
		t.Fatalf("tombstoneNode: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, reason, evicted_at)
		 VALUES (?, ?, 'dead-voter', datetime('now', ?))`,
		stale, stale, formatSecondsAgo(tombstoneTTL+time.Hour)); err != nil {
		t.Fatalf("seed stale tombstone: %v", err)
	}

	tombstoned, err := tombstonedNodes(ctx, db)
	if err != nil {
		t.Fatalf("tombstonedNodes: %v", err)
	}
	if _, ok := tombstoned[fresh]; !ok {
		t.Error("a fresh tombstone must still veto re-adding")
	}
	if _, ok := tombstoned[stale]; ok {
		t.Errorf("a tombstone older than %s must expire, or an evicted node has no way back", tombstoneTTL)
	}
}
