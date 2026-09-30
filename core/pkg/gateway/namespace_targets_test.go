package gateway

import (
	"context"
	"testing"

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
