package namespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
)

// The enable used to delete a TURN host's records and insert each address
// plainly. The per-node reconciler adds the same rows, and one that landed in
// between failed the enable on the unique index (fqdn, record_type, value).
// Now the set converges: a row already there is kept, a missing one added, a
// stale one removed, and running it again changes nothing.
func TestCreateTURNRecords_convergesBesideTheReconciler(t *testing.T) {
	c, db := rqlitetest.SQLite(t)
	ctx := context.Background()
	if err := rqlite.ApplyEmbeddedMigrations(ctx, db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	drm := NewDNSRecordManager(c, "example.test", zap.NewNop())
	host := "turn.ns-acme.example.test."
	tag := "namespace-turn:acme"
	// The reconciler already wrote one node's record; an old node's is stale.
	for _, ip := range []string{"10.1.1.1", "10.9.9.9"} {
		if _, err := db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at)
			VALUES (?, 'A', ?, 60, ?, 'turn-dns-reconcile', datetime('now'), datetime('now'))`, host, ip, tag); err != nil {
			t.Fatalf("seed %s: %v", ip, err)
		}
	}

	for i := 0; i < 2; i++ {
		if err := drm.CreateTURNRecords(ctx, "acme", []string{"10.1.1.1", "10.2.2.2"}); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	var values []string
	rows, err := db.Query(`SELECT value FROM dns_records WHERE fqdn = ? AND record_type = 'A' ORDER BY value`, host)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		values = append(values, v)
	}
	if len(values) != 2 || values[0] != "10.1.1.1" || values[1] != "10.2.2.2" {
		t.Fatalf("TURN records for %s are %v, want exactly [10.1.1.1 10.2.2.2]", host, values)
	}
}

// racingClient writes the reconciler's row the moment a TURN delete has run,
// the interleaving that failed the enable on stagenet.
type racingClient struct {
	rqlite.Client
	db interface {
		Exec(string, ...any) (sql.Result, error)
	}
	host  string
	raced bool
}

func (r *racingClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := r.Client.Exec(ctx, query, args...)
	if err == nil && !r.raced && strings.HasPrefix(strings.TrimSpace(query), "DELETE FROM dns_records") {
		r.raced = true
		if _, ierr := r.db.Exec(`INSERT INTO dns_records (fqdn, record_type, value, ttl, namespace, created_by, created_at, updated_at)
			VALUES (?, 'A', '10.1.1.1', 60, 'namespace-turn:acme', 'turn-dns-reconcile', datetime('now'), datetime('now'))`, r.host); ierr != nil {
			return nil, ierr
		}
	}
	return res, err
}

func TestCreateTURNRecords_aReconcilerWriteBetweenTheStepsDoesNotFailTheEnable(t *testing.T) {
	c, db := rqlitetest.SQLite(t)
	ctx := context.Background()
	if err := rqlite.ApplyEmbeddedMigrations(ctx, db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	host := "turn.ns-acme.example.test."
	drm := NewDNSRecordManager(&racingClient{Client: c, db: db, host: host}, "example.test", zap.NewNop())
	if err := drm.CreateTURNRecords(ctx, "acme", []string{"10.1.1.1", "10.2.2.2"}); err != nil {
		t.Fatalf("the enable failed when the reconciler wrote between its steps: %v", err)
	}
}
