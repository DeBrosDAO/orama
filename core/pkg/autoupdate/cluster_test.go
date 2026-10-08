package autoupdate

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

func members() []Member {
	return []Member{
		{ID: "n1", InternalIP: "10.0.0.1", Role: "nameserver-ns1", Live: true},
		{ID: "n2", InternalIP: "10.0.0.2", Role: "node", Live: true},
		{ID: "n3", InternalIP: "10.0.0.3", Role: "node", Live: true},
	}
}

func TestNextNode_followersFirstAndTheLeaderLast(t *testing.T) {
	raft := RaftView{LeaderHost: "10.0.0.1", Voters: 3, HealthyVoters: 3}
	installs := map[string]string{}
	var order []string
	for {
		next, ok, err := NextNode(members(), raft, installs)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		order = append(order, next.ID)
		installs[next.ID] = StateInstalled
	}
	if !slices.Equal(order, []string{"n2", "n3", "n1"}) {
		t.Fatalf("order = %v, want the leader n1 last", order)
	}
}

func TestNextNode_aNodeThatFailedStillHoldsItsTurn(t *testing.T) {
	raft := RaftView{LeaderHost: "10.0.0.1", Voters: 3, HealthyVoters: 3}
	next, ok, err := NextNode(members(), raft, map[string]string{"n2": StateFailed})
	if err != nil || !ok || next.ID != "n2" {
		t.Fatalf("next = %+v, %v, %v", next, ok, err)
	}
}

func TestNextNode_refusals(t *testing.T) {
	raft := RaftView{LeaderHost: "10.0.0.1"}
	if _, _, err := NextNode(members(), RaftView{}, nil); err == nil || !strings.Contains(err.Error(), "leader") {
		t.Errorf("no leader: err = %v", err)
	}
	noAddress := members()
	noAddress[1].InternalIP = ""
	if _, _, err := NextNode(noAddress, raft, nil); err == nil {
		t.Error("a node with no overlay address was planned")
	}
	if _, ok, err := NextNode(nil, raft, nil); err == nil && ok {
		t.Error("an empty cluster has a next node")
	}
}

func TestClusterHealth(t *testing.T) {
	raft := RaftView{Voters: 3, HealthyVoters: 2}
	if h := ClusterHealth(members(), raft); h.Degraded || h.Voters != 3 || h.HealthyVoters != 2 {
		t.Errorf("healthy members: %+v", h)
	}
	m := members()
	m[2].Live = false
	if h := ClusterHealth(m, raft); !h.Degraded {
		t.Error("a member that is not live did not degrade the cluster")
	}
	if h := ClusterHealth(nil, raft); !h.Degraded {
		t.Error("a cluster with no members is not healthy")
	}
}

func TestSettingsFrom(t *testing.T) {
	s, err := SettingsFrom(nil, RoleCluster)
	if err != nil || s.Mode != ModeNotify || s.Channel != "stable" || s.RepoURL != "" || s.MaxParallel != 1 {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	s, err = SettingsFrom(map[string]string{
		updatepolicy.KeyMode: "auto", updatepolicy.KeyChannel: "nightly", updatepolicy.KeyWindow: "22-4",
		updatepolicy.KeyRepo: "https://r.example.org/tuf",
	}, RoleCluster)
	if err != nil || s.Mode != ModeAuto || s.Channel != "nightly" || s.WindowStart != 22 || s.WindowEnd != 4 || s.RepoURL != "https://r.example.org/tuf" {
		t.Fatalf("stored = %+v, %v", s, err)
	}
	for key, bad := range map[string]string{
		updatepolicy.KeyMode: "x", updatepolicy.KeyChannel: "No Way", updatepolicy.KeyWindow: "late", updatepolicy.KeyRepo: "http://x.example.org",
	} {
		if _, err := SettingsFrom(map[string]string{key: bad}, RoleCluster); err == nil {
			t.Errorf("%s=%q was accepted", key, bad)
		}
	}
}

func TestSQLStore_membersAreTheLiveRegisteredNodes(t *testing.T) {
	db := newClusterDB(t)
	for _, q := range []string{
		`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, role, last_seen) VALUES ('retired', '198.51.100.1', '10.0.0.8', 'offline', 'node', '1970-01-01 00:00:00')`,
		`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, role, last_seen) VALUES ('stale', '198.51.100.2', '10.0.0.7', 'active', 'node', datetime('now', '-1 hour'))`,
		`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, role, last_seen) VALUES ('draining', '198.51.100.3', '10.0.0.6', 'draining', 'node', datetime('now'))`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got, err := SQLStore{DB: db}.Members(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{}
	for _, m := range got {
		live[m.ID] = m.Live
	}
	want := map[string]bool{"n1": true, "n2": true, "n3": true, "stale": false, "draining": false}
	if len(live) != len(want) {
		t.Fatalf("members = %v, want %v (the retired node is not one)", live, want)
	}
	for id, w := range want {
		if live[id] != w {
			t.Errorf("%s live = %v, want %v", id, live[id], w)
		}
	}
}

func TestSQLStore_recordReplacesAndRefusesAnUnknownState(t *testing.T) {
	db := newClusterDB(t)
	s := SQLStore{DB: db}
	if err := s.Record(t.Context(), "1.0.0", "n1", StateFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(t.Context(), "1.0.0", "n1", StateInstalled, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Installs(t.Context(), "1.0.0")
	if err != nil || len(got) != 1 || got["n1"] != StateInstalled {
		t.Fatalf("installs = %v, %v", got, err)
	}
	if err := s.Record(t.Context(), "1.0.0", "n1", "weird", ""); err == nil {
		t.Fatal("an unknown state was recorded")
	}
	if other, _ := s.Installs(t.Context(), "2.0.0"); len(other) != 0 {
		t.Fatalf("another version has installs: %v", other)
	}
}

func TestSQLStore_twoNodesRacingForTheLockOneWins(t *testing.T) {
	db := newClusterDB(t)
	s := SQLStore{DB: db}
	var wins, held atomic.Int32
	var mu sync.Mutex
	var releases []func()
	var wg sync.WaitGroup
	for _, id := range []string{"n1", "n2", "n3", "n1", "n2", "n3"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := s.Lock(t.Context(), id)
			switch {
			case err == nil:
				wins.Add(1)
				mu.Lock()
				releases = append(releases, func() { _ = release(t.Context()) })
				mu.Unlock()
			case errors.Is(err, rqlite.ErrClusterLockHeld):
				held.Add(1)
			default:
				t.Errorf("lock: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || held.Load() != 5 {
		t.Fatalf("%d acquired and %d found it held, want 1 and 5", wins.Load(), held.Load())
	}
	for _, release := range releases {
		release()
	}
	if _, err := s.Lock(t.Context(), "n2"); err != nil {
		t.Fatalf("the lock was not free after its holder released it: %v", err)
	}
}

func TestSQLStore_storedReadsOnlyTheUpdateSettings(t *testing.T) {
	db := newClusterDB(t)
	setSetting(t, db, "namespace_creation", "open")
	setSetting(t, db, updatepolicy.KeyChannel, "nightly")
	got, err := SQLStore{DB: db}.Stored(t.Context())
	if err != nil || len(got) != 1 || got[updatepolicy.KeyChannel] != "nightly" {
		t.Fatalf("stored = %v, %v", got, err)
	}
}
