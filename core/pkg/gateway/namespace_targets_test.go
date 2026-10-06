package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
)

// targetsDB is the real registry schema; seed statements add rows.
func targetsDB(t *testing.T, seed ...string) rqlite.Client {
	t.Helper()
	c, db := rqlitetest.SQLite(t)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for _, s := range seed {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return c
}

// acmeOnTwoNodes is namespace acme served by gateways on n1 and n2, each node
// holding the three per-role membership rows a live cluster has.
var acmeOnTwoNodes = []string{
	`INSERT INTO namespace_clusters (id, namespace_id, namespace_name, status, provisioned_by) VALUES ('c1', 1, 'acme', 'ready', 'test')`,
	`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('n1', '192.0.2.1', '10.0.0.1', 'active'), ('n2', '192.0.2.2', '10.0.0.2', 'active')`,
	`INSERT INTO namespace_port_allocations (id, node_id, namespace_cluster_id, port_start, port_end, rqlite_http_port, rqlite_raft_port, olric_http_port, olric_memberlist_port, gateway_http_port)
	 VALUES ('p1', 'n1', 'c1', 10030, 10034, 10030, 10031, 10032, 10033, 10034), ('p2', 'n2', 'c1', 10030, 10034, 10030, 10031, 10032, 10033, 10034)`,
	`INSERT INTO namespace_cluster_nodes (id, namespace_cluster_id, node_id, role, status) VALUES
	 ('a', 'c1', 'n1', 'gateway', 'running'), ('b', 'c1', 'n1', 'olric', 'running'),
	 ('c', 'c1', 'n2', 'gateway', 'running'), ('d', 'c1', 'n2', 'olric', 'running')`,
}

func TestNamespaceGatewayTargets_eachLiveGatewayOnce(t *testing.T) {
	g := &Gateway{registry: targetsDB(t, acmeOnTwoNodes...)}
	got, err := g.namespaceGatewayTargets(context.Background(), "acme")
	if err != nil {
		t.Fatalf("namespaceGatewayTargets: %v", err)
	}
	want := map[namespaceGatewayTarget]bool{{ip: "10.0.0.1", port: 10034}: true, {ip: "10.0.0.2", port: 10034}: true}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for _, tg := range got {
		if !want[tg] {
			t.Errorf("unexpected target %v", tg)
		}
	}
}

func TestNamespaceGatewayTargets_aDepartedNodeIsNotATarget(t *testing.T) {
	seed := append(append([]string{}, acmeOnTwoNodes...), `UPDATE dns_nodes SET status = 'offline' WHERE id = 'n2'`)
	g := &Gateway{registry: targetsDB(t, seed...)}
	got, err := g.namespaceGatewayTargets(context.Background(), "acme")
	if err != nil {
		t.Fatalf("namespaceGatewayTargets: %v", err)
	}
	if len(got) != 1 || got[0].ip != "10.0.0.1" {
		t.Fatalf("targets = %v, want only n1", got)
	}
}

func TestNamespaceGatewayTargets_unknownNamespaceIsNoTargetsNotAnError(t *testing.T) {
	g := &Gateway{registry: targetsDB(t, acmeOnTwoNodes...)}
	got, err := g.namespaceGatewayTargets(context.Background(), "other")
	if err != nil || len(got) != 0 {
		t.Fatalf("= %v, %v; want none, nil", got, err)
	}
}

func TestNamespaceGatewayTargets_noRegistryIsAnError(t *testing.T) {
	if _, err := (&Gateway{}).namespaceGatewayTargets(context.Background(), "acme"); err == nil {
		t.Fatal("a gateway with no registry must say so, not report no gateways")
	}
}

// gatedTargetsRegistry holds every lookup until release is closed and counts
// how many reached it.
type gatedTargetsRegistry struct {
	rqlite.Client
	lookups atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (g *gatedTargetsRegistry) Query(ctx context.Context, dest any, query string, args ...any) error {
	if g.lookups.Add(1) == 1 {
		close(g.entered)
	}
	<-g.release
	if err := ctx.Err(); err != nil {
		return err
	}
	return g.Client.Query(ctx, dest, query, args...)
}

// Concurrent misses for one namespace share one registry read.
func TestNamespaceGatewayTargets_concurrentMissesShareOneRead(t *testing.T) {
	registry := &gatedTargetsRegistry{Client: targetsDB(t, acmeOnTwoNodes...), entered: make(chan struct{}), release: make(chan struct{})}
	g := &Gateway{registry: registry}
	const callers = 6

	counts := make(chan int, callers)
	lookup := func() {
		got, err := g.namespaceGatewayTargets(context.Background(), "acme")
		if err != nil {
			t.Errorf("namespaceGatewayTargets: %v", err)
		}
		counts <- len(got)
	}
	go lookup()
	<-registry.entered
	for i := 1; i < callers; i++ {
		go lookup()
	}
	// Followers sharing the read cannot be seen waiting; an unshared follower
	// would show up as a second lookup within this window.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) && registry.lookups.Load() < callers {
		time.Sleep(5 * time.Millisecond)
	}
	close(registry.release)

	for i := 0; i < callers; i++ {
		if n := <-counts; n != 2 {
			t.Errorf("a caller got %d targets, want 2", n)
		}
	}
	if n := registry.lookups.Load(); n != 1 {
		t.Errorf("%d registry reads for %d concurrent callers, want 1", n, callers)
	}
}

// The shared read does not belong to the request that started it: that caller
// hanging up must not fail the lookup for the callers waiting on it.
func TestNamespaceGatewayTargets_aCancelledLeaderDoesNotFailItsFollowers(t *testing.T) {
	registry := &gatedTargetsRegistry{Client: targetsDB(t, acmeOnTwoNodes...), entered: make(chan struct{}), release: make(chan struct{})}
	g := &Gateway{registry: registry}

	leaderCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		_, _ = g.namespaceGatewayTargets(leaderCtx, "acme")
	}()
	<-registry.entered

	type result struct {
		n   int
		err error
	}
	follower := make(chan result, 1)
	go func() {
		got, err := g.namespaceGatewayTargets(context.Background(), "acme")
		follower <- result{len(got), err}
	}()
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) && registry.lookups.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	close(registry.release)
	<-leaderDone

	if r := <-follower; r.err != nil || r.n != 2 {
		t.Errorf("follower got %d targets, err %v, after the leader hung up; want 2, nil", r.n, r.err)
	}
}
