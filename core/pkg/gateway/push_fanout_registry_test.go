package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
)

// TestNtfyFanoutResolver_readsTheRegistrysActiveOverlayNodes: the resolver
// reads dns_nodes through the registry handle it is given. A namespace
// gateway handed its own RQLite (empty dns_nodes) answered every push with
// "no active push nodes".
func TestNtfyFanoutResolver_readsTheRegistrysActiveOverlayNodes(t *testing.T) {
	c, db := rqlitetest.SQLite(t)
	ctx := context.Background()
	if err := rqlite.ApplyEmbeddedMigrations(ctx, db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES
		('n1', '192.0.2.1', '10.0.0.1', 'active'),
		('n2', '192.0.2.2', '10.0.0.2', 'inactive'),
		('n3', '192.0.2.3', '', 'active'),
		('n4', '192.0.2.4', '203.0.113.9', 'active'),
		('n5', '192.0.2.5', 'not-an-ip', 'active')`); err != nil {
		t.Fatalf("seed dns_nodes: %v", err)
	}
	targets, err := newNtfyFanoutResolver(c, time.Minute).Targets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].NodeID != "n1" || targets[0].BaseURL != fmt.Sprintf("http://10.0.0.1:%d", constants.GatewayAPIPort) {
		t.Fatalf("targets %+v, want n1 on its overlay address alone (a public or malformed internal_ip is never a target)", targets)
	}
}

func TestNtfyFanoutResolver_emptyRegistryIsNoTargets(t *testing.T) {
	c, db := rqlitetest.SQLite(t)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	targets, err := newNtfyFanoutResolver(c, time.Minute).Targets(context.Background())
	if err != nil || len(targets) != 0 {
		t.Fatalf("targets %v err %v, want none", targets, err)
	}
}
