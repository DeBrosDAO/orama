package gateway

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// A key used to work until somebody remembered to revoke it. The expiry column
// only means anything if the lookup applies it, and the lookup is one SQL
// statement — so this runs the real one against the real schema rather than a
// fake that would filter the rows itself and pass either way.

// sqliteKeyQuerier is an apiKeyQuerier over a real *sql.DB.
type sqliteKeyQuerier struct{ db *sql.DB }

func (q *sqliteKeyQuerier) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := &client.QueryResult{Columns: columns}
	for rows.Next() {
		cells := make([]interface{}, len(columns))
		into := make([]interface{}, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			return nil, err
		}
		for i, cell := range cells {
			if b, ok := cell.([]byte); ok {
				cells[i] = string(b)
			}
		}
		out.Rows = append(out.Rows, cells)
	}
	out.Count = int64(len(out.Rows))
	return out, rows.Err()
}

// keyRegistry is a migrated registry holding one namespace, and a gateway
// wired to read keys out of it.
func keyRegistry(t *testing.T) (*Gateway, *sqliteKeyQuerier, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO namespaces(id, name) VALUES (10, 'anchat')`); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	querier := &sqliteKeyQuerier{db: db}
	svc, err := gwauth.NewService(newRQLiteTestLogger(), &sqliteKeyNet{db: querier}, "", "default")
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	svc.SetAPIKeyHMACSecret("test-hmac-secret")
	return &Gateway{authService: svc}, querier, db
}

// sqliteKeyNet lets the auth service read its own tables — the revocation list
// lives in one of them.
type sqliteKeyNet struct {
	client.NetworkClient
	db *sqliteKeyQuerier
}

func (n *sqliteKeyNet) Database() client.DatabaseClient { return n.db }

// storeKey writes a key row with an explicit expiry.
func storeKey(t *testing.T, g *Gateway, db *sql.DB, raw, scopes string, expiresAt time.Time) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO api_keys(key, name, namespace_id, scopes, expires_at) VALUES (?, '', 10, ?, ?)`,
		g.authService.HashAPIKey(raw), scopes, expiresAt.UTC().Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("store key: %v", err)
	}
}

func TestLookupAPIKeyEntry_expiry(t *testing.T) {
	g, q, db := keyRegistry(t)

	live, err := gwauth.NewKey(gwauth.KeyTypeRuntime)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	expired, err := gwauth.NewKey(gwauth.KeyTypeRuntime)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	storeKey(t, g, db, live, "invoke,storage", time.Now().Add(24*time.Hour))
	storeKey(t, g, db, expired, "invoke,storage", time.Now().Add(-time.Minute))

	ns, scopes, err := g.lookupAPIKeyEntry(context.Background(), live, q)
	if err != nil {
		t.Fatalf("a live key was refused: %v", err)
	}
	if ns != "anchat" || scopes != "invoke,storage" {
		t.Errorf("got (%q, %q)", ns, scopes)
	}

	if _, _, err := g.lookupAPIKeyEntry(context.Background(), expired, q); err == nil {
		t.Error("an expired key resolved; the expiry column is decoration unless the lookup applies it")
	}
}

func TestLookupAPIKeyEntry_revokedRowDoesNotResolve(t *testing.T) {
	g, q, db := keyRegistry(t)

	key, err := gwauth.NewKey(gwauth.KeyTypeService)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	storeKey(t, g, db, key, "admin", time.Now().Add(24*time.Hour))

	if _, _, err := g.lookupAPIKeyEntry(context.Background(), key, q); err != nil {
		t.Fatalf("a live key was refused: %v", err)
	}
	if _, err := db.Exec(`UPDATE api_keys SET revoked_at = datetime('now')`); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := g.lookupAPIKeyEntry(context.Background(), key, q); err == nil {
		t.Error("a revoked key resolved")
	}
}

// The cache is what made a revocation take up to a minute to bite, on every
// gateway that had seen the key. The revocation list is replicated and reloaded
// every ten seconds, and is consulted before the cache for exactly that reason.
func TestLookupAPIKeyEntry_aRevokedKeyIsRefusedEvenWhenCached(t *testing.T) {
	g, q, db := keyRegistry(t)
	g.mwCache = newMiddlewareCache(time.Minute)

	key, err := gwauth.NewKey(gwauth.KeyTypeService)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	storeKey(t, g, db, key, "admin", time.Now().Add(24*time.Hour))

	if _, _, err := g.lookupAPIKeyEntry(context.Background(), key, q); err != nil {
		t.Fatalf("a live key was refused: %v", err)
	}
	// It is in the cache now: the querier is not consulted again.
	if _, _, err := g.lookupAPIKeyEntry(context.Background(), key, q); err != nil {
		t.Fatalf("the cached key was refused: %v", err)
	}

	// Revoke it the way RevokeKey does, without touching the cache.
	if err := g.authService.Revocations().RevokeSubject(
		context.Background(), g.authService.HashAPIKey(key), "test", time.Hour); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, _, err := g.lookupAPIKeyEntry(context.Background(), key, q); err == nil {
		t.Error("a revoked key was served out of the cache; the whole point of consulting the " +
			"revocation list first is that the cache is the longer of the two windows")
	}
}

// The querier is also a DatabaseClient: the auth service writes revocations
// through it, and everything else it needs is unused here.
func (q *sqliteKeyQuerier) Transaction(context.Context, []string) error { return nil }
func (q *sqliteKeyQuerier) CreateTable(context.Context, string) error   { return nil }
func (q *sqliteKeyQuerier) DropTable(context.Context, string) error     { return nil }
func (q *sqliteKeyQuerier) GetSchema(context.Context) (*client.SchemaInfo, error) {
	return nil, nil
}
