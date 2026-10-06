package deployments

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// A replica's A record published the node's WireGuard overlay address,
// which nobody on the internet can reach (stagenet e2e audit, 2026-09-30).
// It takes the node's public address, as the home node's record does.
func TestPublishReplicaRecord_usesThePublicAddress(t *testing.T) {
	svc := registryWith(t)
	svc.baseDomain = "example.test"
	ctx := context.Background()
	if _, err := svc.db.Exec(ctx,
		`INSERT INTO dns_nodes (id, ip_address, internal_ip) VALUES ('node-b', '203.0.113.9', '10.0.0.2')`); err != nil {
		t.Fatal(err)
	}
	d := &deployments.Deployment{ID: "d1", Namespace: "acme", Name: "web", Subdomain: "web-abc123"}

	svc.publishReplicaRecord(ctx, d, "node-b")

	var rows []struct {
		Value string `db:"value"`
	}
	if err := svc.db.Query(ctx, &rows, `SELECT value FROM dns_records WHERE fqdn = 'web-abc123.example.test.' AND record_type = 'A'`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Value != "203.0.113.9" {
		t.Fatalf("A records %+v, want exactly the public 203.0.113.9", rows)
	}
}

func TestPublishReplicaRecord_unknownNodePublishesNothing(t *testing.T) {
	svc := registryWith(t)
	svc.baseDomain = "example.test"
	ctx := context.Background()
	svc.publishReplicaRecord(ctx, &deployments.Deployment{ID: "d1", Namespace: "acme", Name: "web"}, "node-missing")
	var rows []struct {
		N int `db:"n"`
	}
	if err := svc.db.Query(ctx, &rows, `SELECT COUNT(*) AS n FROM dns_records`); err != nil {
		t.Fatal(err)
	}
	if rows[0].N != 0 {
		t.Fatalf("%d records published for a node with no address", rows[0].N)
	}
}
