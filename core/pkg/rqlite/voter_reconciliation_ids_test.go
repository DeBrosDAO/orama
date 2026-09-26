package rqlite

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"go.uber.org/zap"
)

// joinRecorder is an rqlited that records POST /join bodies and answers /nodes.
func joinRecorder(t *testing.T, nodes RQLiteNodes) (*RQLiteManager, *[]map[string]any) {
	t.Helper()
	var joins []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/join":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode join: %v", err)
			}
			joins = append(joins, body)
		case "/nodes":
			_ = json.NewEncoder(w).Encode(nodes)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewRQLiteManager(&config.DatabaseConfig{RQLitePort: port, RQLiteUsername: "u", RQLitePassword: "p"},
		&config.DiscoveryConfig{HttpAdvAddress: u.Host}, t.TempDir(), zap.NewNop())
	return mgr, &joins
}

// sixMembers are ids that are not addresses — peer ids, and the recorded
// address ids a node keeps across the 7001 → 10101 move.
func sixMembers() RQLiteNodes {
	nodes := make(RQLiteNodes, 0, 6)
	for i := 1; i <= 6; i++ {
		nodes = append(nodes, RQLiteNode{
			ID:        fmt.Sprintf("10.0.0.%d:7001", i),
			Addr:      fmt.Sprintf("10.0.0.%d:10101", i),
			Voter:     true,
			Reachable: true,
		})
	}
	nodes[0].Leader = true
	return nodes
}

// A voter change is a POST /join, and rqlite removes and re-adds a member
// whose id matches and whose address does not. The address sent must be the
// member's address, never its id: sending the id moved the member to an
// address nothing listens on.
func TestReconcileVoters_joinCarriesTheMemberAddress(t *testing.T) {
	nodes := sixMembers()
	nodes[1].Voter = false // 10.0.0.2 belongs in the voter set
	nodes[5].Voter = true  // 10.0.0.6 does not; promotion comes first in order
	mgr, joins := joinRecorder(t, nodes)
	st := &RQLiteStatus{}
	st.Store.Raft.LeaderID = nodes[0].ID

	rec := &voterReconciler{cooldowns: map[string]time.Time{}, unreachable: unreachableStreaks{}}
	if err := mgr.reconcileVoters(rec, st, nodes, true); err != nil {
		t.Fatal(err)
	}
	if len(*joins) != 1 {
		t.Fatalf("joins = %v, want one promotion", *joins)
	}
	j := (*joins)[0]
	if j["id"] != "10.0.0.2:7001" || j["addr"] != "10.0.0.2:10101" || j["voter"] != true {
		t.Errorf("join = %v, want id 10.0.0.2:7001 at 10.0.0.2:10101", j)
	}
}

// The leader is found in the address-keyed voter set by its address; looked up
// by its id, every leader whose id is not its address was "not in the voter
// set" and reconciliation never ran again.
func TestReconcileVoters_findsTheLeaderByItsAddress(t *testing.T) {
	nodes := sixMembers()
	nodes[1].Voter = false
	mgr, joins := joinRecorder(t, nodes)
	st := &RQLiteStatus{}
	st.Store.Raft.LeaderID = nodes[0].ID

	rec := &voterReconciler{cooldowns: map[string]time.Time{}, unreachable: unreachableStreaks{}}
	if err := mgr.reconcileVoters(rec, st, nodes, true); err != nil {
		t.Fatal(err)
	}
	if len(*joins) == 0 {
		t.Fatal("reconciliation skipped: the leader was not found in the voter set")
	}
}
