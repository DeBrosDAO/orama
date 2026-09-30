package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type fakeMeshService struct {
	self      pubsub.MeshSelf
	selfErr   error
	connectFn func(addrs []string) (pubsub.MeshConnectResult, error)
	connected [][]string
	allowed   [][]string
}

func (f *fakeMeshService) MeshSelf(context.Context) (pubsub.MeshSelf, error) {
	return f.self, f.selfErr
}

func (f *fakeMeshService) MeshConnect(_ context.Context, addrs, allow []string) (pubsub.MeshConnectResult, error) {
	f.connected = append(f.connected, append([]string(nil), addrs...))
	f.allowed = append(f.allowed, append([]string(nil), allow...))
	if f.connectFn != nil {
		return f.connectFn(addrs)
	}
	return pubsub.MeshConnectResult{Connected: len(addrs)}, nil
}

func meshRegistry(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestMesh(t *testing.T, db *sql.DB, node, peerID, addr string, now *time.Time) (*PubsubMesh, *fakeMeshService) {
	t.Helper()
	svc := &fakeMeshService{self: pubsub.MeshSelf{PeerID: peerID, Addrs: []string{addr}}}
	m := NewPubsubMesh(svc, db, node, zap.NewNop())
	m.now = func() time.Time { return *now }
	if err := m.initTable(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m, svc
}

// Bug (stagenet e2e TestPubsub_crossNodeDelivery): nothing told a node's
// pubsub service about the others, so messages never left the node. Each
// gateway registers its service and has it connect to the others registered.
func TestPubsubMesh_eachServiceConnectsToTheOthersRegistered(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	a, svcA := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	b, svcB := newTestMesh(t, db, "node-b", "peer-b", "/ip4/10.0.0.2/tcp/2/p2p/peer-b", &now)
	c, svcC := newTestMesh(t, db, "node-c", "peer-c", "/ip4/10.0.0.3/tcp/3/p2p/peer-c", &now)
	ctx := context.Background()

	for _, m := range []*PubsubMesh{a, b, c} {
		if err := m.reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// a ran first and saw nobody; c, last, saw both. Every service learns the
	// others by its next round.
	if len(svcA.connected) != 0 {
		t.Errorf("a connected %v before anyone else registered", svcA.connected)
	}
	if err := a.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[*fakeMeshService][]string{
		svcA: {"/ip4/10.0.0.2/tcp/2/p2p/peer-b", "/ip4/10.0.0.3/tcp/3/p2p/peer-c"},
		svcB: {"/ip4/10.0.0.1/tcp/1/p2p/peer-a", "/ip4/10.0.0.3/tcp/3/p2p/peer-c"},
		svcC: {"/ip4/10.0.0.1/tcp/1/p2p/peer-a", "/ip4/10.0.0.2/tcp/2/p2p/peer-b"},
	}
	for svc, addrs := range want {
		// Which peers, not in what order: a node lists its successors on the
		// peer-id ring, starting after itself.
		got := append([]string(nil), svc.connected[len(svc.connected)-1]...)
		sort.Strings(got)
		if !reflect.DeepEqual(got, addrs) {
			t.Errorf("service %s connected %v, want %v", svc.self.PeerID, got, addrs)
		}
		for _, addr := range got {
			if addr == svc.self.Addrs[0] {
				t.Errorf("service %s was told to connect to itself", svc.self.PeerID)
			}
		}
	}
}

func TestPubsubMesh_aRegistrationNobodyRefreshesIsNotDialled(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	a, _ := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	b, svcB := newTestMesh(t, db, "node-b", "peer-b", "/ip4/10.0.0.2/tcp/2/p2p/peer-b", &now)
	ctx := context.Background()
	if err := a.reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	now = now.Add(pubsubMeshPeerTTL + time.Second)
	if err := b.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(svcB.connected) != 0 {
		t.Fatalf("b dialled %v, a registration older than the TTL", svcB.connected)
	}

	if err := a.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(svcB.connected) != 1 || svcB.connected[0][0] != "/ip4/10.0.0.1/tcp/1/p2p/peer-a" {
		t.Fatalf("b connected %v after a refreshed, want a", svcB.connected)
	}
}

func TestPubsubMesh_registrationsPastRetentionAreForgotten(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	old, _ := newTestMesh(t, db, "node-old", "peer-old", "/ip4/10.0.0.9/tcp/9/p2p/peer-old", &now)
	if err := old.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(pubsubMeshRetention + time.Second)
	m, _ := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	if err := m.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _pubsub_mesh_peers WHERE peer_id = 'peer-old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a registration past retention was kept")
	}
}

func TestPubsubMesh_aServiceThatIsDownIsAnActionableError(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	m, svc := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	svc.selfErr = errors.New("connection refused")
	err := m.reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "orama-namespace-pubsub@index") || !errors.Is(err, svc.selfErr) {
		t.Fatalf("err = %v, want one naming the unit and wrapping the cause", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _pubsub_mesh_peers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a service that could not be reached was registered")
	}
}

func TestPubsubMesh_aFailedConnectIsReturned(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	a, _ := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	b, svcB := newTestMesh(t, db, "node-b", "peer-b", "/ip4/10.0.0.2/tcp/2/p2p/peer-b", &now)
	if err := a.reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	svcB.connectFn = func([]string) (pubsub.MeshConnectResult, error) {
		return pubsub.MeshConnectResult{}, errors.New("400 Bad Request")
	}
	if err := b.reconcile(context.Background()); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want the service's refusal", err)
	}
}

func TestPubsubMesh_registeringTwiceKeepsOneRowPerService(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_000_000, 0)
	a, _ := newTestMesh(t, db, "node-a", "peer-a", "/ip4/10.0.0.1/tcp/1/p2p/peer-a", &now)
	for range 3 {
		now = now.Add(pubsubMeshInterval)
		if err := a.reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var seen int64
	if err := db.QueryRow(`SELECT COUNT(*), MAX(last_seen) FROM _pubsub_mesh_peers`).Scan(&n, &seen); err != nil {
		t.Fatal(err)
	}
	if n != 1 || seen != now.Unix() {
		t.Fatalf("rows = %d, last_seen = %d, want 1 row refreshed to %d", n, seen, now.Unix())
	}
}

func TestPubsubMeshServiceOf_onlyTheOramaClientReachesAService(t *testing.T) {
	if got := pubsubMeshServiceOf(nil); got != nil {
		t.Fatalf("a nil client has a pubsub service: %v", got)
	}
}

// The table used to be created once, before the loop, and a failure there
// (a registry mid-election during an upgrade) ended the mesh for good. Every
// reconcile now creates it, so a mesh started against a registry with no
// table forms on its first pass.
func TestPubsubMesh_aReconcileCreatesTheTableItNeeds(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_800_000_000, 0)
	svc := &fakeMeshService{self: pubsub.MeshSelf{PeerID: "p1", Addrs: []string{"/ip4/10.0.0.1/tcp/4001/p2p/p1"}}}
	m := NewPubsubMesh(svc, db, "n1", zap.NewNop())
	m.now = func() time.Time { return now }

	if err := m.reconcile(context.Background()); err != nil {
		t.Fatalf("a reconcile against a registry with no table: %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _pubsub_mesh_peers`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("registered rows = %d, err %v; want this service registered", rows, err)
	}
}

func TestRingSuccessors(t *testing.T) {
	peers := []registeredPeer{{"a", "A"}, {"c", "C"}, {"e", "E"}, {"g", "G"}}
	cases := map[string]struct {
		self  string
		limit int
		want  []string
	}{
		"from the middle, wrapping": {"d", 3, []string{"E", "G", "A"}},
		"after the last":            {"z", 2, []string{"A", "C"}},
		"all when under the limit":  {"b", 10, []string{"C", "E", "G", "A"}},
		"none allowed":              {"b", 0, []string{}},
	}
	for name, c := range cases {
		got := ringSuccessors(peers, c.self, c.limit)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if got := ringSuccessors(nil, "x", 5); len(got) != 0 {
		t.Errorf("no peers: %v", got)
	}
}

// More live services than one ConnectPeers call takes: the service refused
// the whole list and the mesh formed for nobody. A node now asks for at most
// pubsub.MaxMeshPeers, its successors on the ring.
func TestPubsubMesh_aNodeAsksForNoMoreThanTheServiceTakes(t *testing.T) {
	db := meshRegistry(t)
	now := time.Unix(1_800_000_000, 0)
	m, svc := newTestMesh(t, db, "n0", "p-self", "/ip4/10.0.0.1/tcp/4001/p2p/p-self", &now)
	for i := 0; i < pubsub.MaxMeshPeers+20; i++ {
		id := fmt.Sprintf("p-%04d", i)
		if _, err := db.Exec(`INSERT INTO _pubsub_mesh_peers (peer_id, node_id, multiaddr, last_seen) VALUES (?, ?, ?, ?)`,
			id, "n"+id, "/ip4/10.0.0.2/tcp/4001/p2p/"+id, now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(svc.connected) != 1 || len(svc.connected[0]) != pubsub.MaxMeshPeers {
		t.Fatalf("asked the service for %d peers, want %d", len(svc.connected[0]), pubsub.MaxMeshPeers)
	}
}

// Past MaxMeshPeers live services, a node dials only its ring successors. The
// gate on the other side admits only who it was told about, so it must be told
// about every node that dials it: for every node X and every Y that X dials, X
// is in Y's allow list, at a fleet size where most pairs are not.
func TestRing_whoeverDialsANodeIsOnItsAllowList(t *testing.T) {
	const fleet = 600
	peers := make([]registeredPeer, fleet)
	for i := range peers {
		id := fmt.Sprintf("peer-%04d", i)
		peers[i] = registeredPeer{id: id, addr: id}
	}
	allowOf := make(map[string]map[string]bool, fleet)
	for _, p := range peers {
		others := withoutPeer(peers, p.id)
		allowOf[p.id] = make(map[string]bool)
		for _, a := range ringPredecessors(others, p.id, pubsub.MaxMeshPeers) {
			allowOf[p.id][a] = true
		}
	}
	for _, x := range peers {
		for _, y := range ringSuccessors(withoutPeer(peers, x.id), x.id, pubsub.MaxMeshPeers) {
			if !allowOf[y][x.id] {
				t.Fatalf("%s dials %s, which does not allow it", x.id, y)
			}
		}
	}
}

func withoutPeer(peers []registeredPeer, id string) []registeredPeer {
	out := make([]registeredPeer, 0, len(peers)-1)
	for _, p := range peers {
		if p.id != id {
			out = append(out, p)
		}
	}
	return out
}

func TestRingPredecessors(t *testing.T) {
	peers := []registeredPeer{{"a", "A"}, {"c", "C"}, {"e", "E"}, {"g", "G"}}
	cases := map[string]struct {
		self  string
		limit int
		want  []string
	}{
		"from the middle, wrapping": {"d", 3, []string{"C", "A", "G"}},
		"before the first":          {"0", 2, []string{"G", "E"}},
		"all when under the limit":  {"f", 10, []string{"E", "C", "A", "G"}},
	}
	for name, c := range cases {
		if got := ringPredecessors(peers, c.self, c.limit); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
