package rqlite

// freshness_test.go covers the local-follower freshness gate (#1022): the
// auto-degrade decision that keeps a none-read off a stale local follower.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// statusServer spins up an httptest server that serves a /status payload with
// the given raft state, last_contact, commit and applied indices. Returns the
// numeric port the gate/GetRaftStatus would use — but GetRaftStatus is pinned
// to localhost:<port>, so we drive LocalFollowerFresh through the parsed port.
func statusServer(t *testing.T, state, lastContact string, commit, applied uint64) (int, func()) {
	t.Helper()
	body := fmt.Sprintf(`{"store":{"raft":{"state":%q,"last_contact":%q,"commit_index":%d,"applied_index":%d}}}`,
		state, lastContact, commit, applied)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/status") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	// GetRaftStatus hits localhost:<port>; httptest binds 127.0.0.1, so extract
	// the port and reuse it.
	u := srv.URL
	idx := strings.LastIndex(u, ":")
	port, err := strconv.Atoi(u[idx+1:])
	if err != nil {
		srv.Close()
		t.Fatalf("parse httptest port from %q: %v", u, err)
	}
	return port, srv.Close
}

func TestLocalFollowerFresh_leaderAlwaysFresh(t *testing.T) {
	// A leader has the authoritative state — fresh regardless of contact/gap.
	port, closeFn := statusServer(t, "Leader", "", 100, 0)
	defer closeFn()

	fresh, reason, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fresh {
		t.Errorf("leader must be fresh; reason=%q", reason)
	}
}

func TestLocalFollowerFresh_leaderCaseInsensitive(t *testing.T) {
	port, closeFn := statusServer(t, "LEADER", "", 0, 0)
	defer closeFn()

	fresh, _, err := LocalFollowerFresh(port)
	if err != nil || !fresh {
		t.Errorf("leader state must match case-insensitively; fresh=%v err=%v", fresh, err)
	}
}

func TestLocalFollowerFresh_followerFresh(t *testing.T) {
	// Recent contact + tiny apply gap → fresh.
	port, closeFn := statusServer(t, "Follower", "150ms", 1000, 999)
	defer closeFn()

	fresh, reason, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fresh {
		t.Errorf("fresh follower judged stale; reason=%q", reason)
	}
}

func TestLocalFollowerFresh_staleByLastContact(t *testing.T) {
	// Contact older than StalenessMaxLastContact → stale even with no apply gap.
	port, closeFn := statusServer(t, "Follower", "5s", 1000, 1000)
	defer closeFn()

	fresh, reason, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fresh {
		t.Error("follower stale by last_contact must NOT be fresh")
	}
	if !strings.Contains(reason, "last_contact") {
		t.Errorf("reason should mention last_contact; got %q", reason)
	}
}

func TestLocalFollowerFresh_staleByApplyGap(t *testing.T) {
	// Recent contact but apply gap beyond StalenessMaxApplyGap → stale.
	gap := StalenessMaxApplyGap + 10
	port, closeFn := statusServer(t, "Follower", "10ms", 1000, 1000-gap)
	defer closeFn()

	fresh, reason, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fresh {
		t.Error("follower stale by apply gap must NOT be fresh")
	}
	if !strings.Contains(reason, "apply gap") {
		t.Errorf("reason should mention apply gap; got %q", reason)
	}
}

func TestLocalFollowerFresh_neverContactNotFresh(t *testing.T) {
	// rqlite reports "never" before first leader contact — fail-safe to stale.
	port, closeFn := statusServer(t, "Follower", "never", 1000, 1000)
	defer closeFn()

	fresh, _, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fresh {
		t.Error(`last_contact="never" must be judged stale (fail-safe)`)
	}
}

func TestLocalFollowerFresh_applyGapUnderflowGuarded(t *testing.T) {
	// applied > commit (transient) must not underflow into a huge "gap" and
	// wrongly mark the node stale — last_contact recent → fresh.
	port, closeFn := statusServer(t, "Follower", "10ms", 1000, 1005)
	defer closeFn()

	fresh, reason, err := LocalFollowerFresh(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fresh {
		t.Errorf("applied>commit must be guarded (no underflow); reason=%q", reason)
	}
}

func TestLocalFollowerFresh_statusErrorNotFresh(t *testing.T) {
	// Unreachable status port → (false, reason, err); never fresh.
	fresh, _, err := LocalFollowerFresh(1) // port 1 is unbindable/closed
	if err == nil {
		t.Fatal("expected an error reaching /status on a dead port")
	}
	if fresh {
		t.Error("a status error must never be reported as fresh")
	}
}

func TestParseLastContact(t *testing.T) {
	cases := []struct {
		in       string
		wantHuge bool
		want     time.Duration
	}{
		{"150ms", false, 150 * time.Millisecond},
		{"2s", false, 2 * time.Second},
		{"never", true, 0},
		{"", true, 0},
		{"garbage", true, 0},
	}
	for _, tc := range cases {
		got := parseLastContact(tc.in)
		if tc.wantHuge {
			if got != staleNeverContact {
				t.Errorf("parseLastContact(%q) = %v; want staleNeverContact", tc.in, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("parseLastContact(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestFollowerFreshnessGate_cachesWithinTTL(t *testing.T) {
	// The gate must re-use a verdict within ttl and re-check only after it ages.
	var mu sync.Mutex
	calls := 0
	check := func(int) (bool, string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return true, "ok", nil
	}
	g := newFollowerFreshnessGate(7000, check, 50*time.Millisecond)

	for i := 0; i < 5; i++ {
		if fresh, _ := g.Fresh(); !fresh {
			t.Fatal("expected fresh")
		}
	}
	mu.Lock()
	if calls != 1 {
		mu.Unlock()
		t.Fatalf("expected exactly 1 check within ttl, got %d", calls)
	}
	mu.Unlock()

	time.Sleep(60 * time.Millisecond)
	_, _ = g.Fresh()
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected a re-check after ttl elapsed, got %d calls", calls)
	}
}

func TestFollowerFreshnessGate_checkErrorFailsSafe(t *testing.T) {
	// A check error must cache fresh=false (degrade to weak), never fresh.
	check := func(int) (bool, string, error) {
		return false, "boom", fmt.Errorf("status unreachable")
	}
	g := newFollowerFreshnessGate(7001, check, time.Second)

	fresh, reason := g.Fresh()
	if fresh {
		t.Error("a check error must yield fresh=false (fail-safe)")
	}
	if !strings.Contains(reason, "fail-safe") {
		t.Errorf("reason should flag the fail-safe; got %q", reason)
	}
}

func TestNewFollowerFreshnessGate_defaults(t *testing.T) {
	g := newFollowerFreshnessGate(5, nil, 0)
	if g.ttl != defaultFreshnessGateTTL {
		t.Errorf("nil ttl must default to %v; got %v", defaultFreshnessGateTTL, g.ttl)
	}
	if g.check == nil {
		t.Error("nil check must default to LocalFollowerFresh")
	}
}
