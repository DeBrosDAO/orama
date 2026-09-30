package gateway

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
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
}

func (f *fakeMeshService) MeshSelf(context.Context) (pubsub.MeshSelf, error) {
	return f.self, f.selfErr
}

func (f *fakeMeshService) MeshConnect(_ context.Context, addrs []string) (pubsub.MeshConnectResult, error) {
	f.connected = append(f.connected, append([]string(nil), addrs...))
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
		got := svc.connected[len(svc.connected)-1]
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
