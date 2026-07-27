package gateway

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	_ "github.com/mattn/go-sqlite3"
)

// newTestSQLiteDB returns an in-memory sqlite3 *sql.DB seeded with a minimal
// api_keys/namespaces schema, for exercising sqlAPIKeyQuerier against a real
// database/sql connection (the rqlite stdlib driver isn't reachable in unit
// tests, but sqlite3's driver.Value semantics for TEXT/INTEGER columns are
// equivalent for the purposes of this scan path).
func newTestSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE namespaces (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE api_keys (
			id INTEGER PRIMARY KEY,
			namespace_id INTEGER NOT NULL,
			key TEXT NOT NULL,
			scopes TEXT,
			revoked_at TEXT
		);
	`); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	return db
}

func TestSqlAPIKeyQuerier_HappyPath(t *testing.T) {
	db := newTestSQLiteDB(t)
	if _, err := db.Exec(`INSERT INTO namespaces (id, name) VALUES (1, 'vrf708')`); err != nil {
		t.Fatalf("seed namespaces failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (namespace_id, key, scopes) VALUES (1, 'hashed_key', 'invoke,storage')`); err != nil {
		t.Fatalf("seed api_keys failed: %v", err)
	}

	q := &sqlAPIKeyQuerier{db: db}
	res, err := q.Query(context.Background(),
		"SELECT namespaces.name, api_keys.scopes FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? AND api_keys.revoked_at IS NULL LIMIT 1",
		"hashed_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 1 || len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got count=%d rows=%d", res.Count, len(res.Rows))
	}
	if got := getString(res.Rows[0][0]); got != "vrf708" {
		t.Errorf("expected namespace vrf708, got %q", got)
	}
	if got := getString(res.Rows[0][1]); got != "invoke,storage" {
		t.Errorf("expected scopes 'invoke,storage', got %q", got)
	}
}

func TestSqlAPIKeyQuerier_NoRows(t *testing.T) {
	db := newTestSQLiteDB(t)
	q := &sqlAPIKeyQuerier{db: db}
	res, err := q.Query(context.Background(),
		"SELECT namespaces.name, api_keys.scopes FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? AND api_keys.revoked_at IS NULL LIMIT 1",
		"does_not_exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 0 || len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows, got count=%d rows=%d", res.Count, len(res.Rows))
	}
}

func TestSqlAPIKeyQuerier_RevokedKeyExcluded(t *testing.T) {
	db := newTestSQLiteDB(t)
	if _, err := db.Exec(`INSERT INTO namespaces (id, name) VALUES (1, 'vrf708')`); err != nil {
		t.Fatalf("seed namespaces failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (namespace_id, key, scopes, revoked_at) VALUES (1, 'revoked_key', '', datetime('now'))`); err != nil {
		t.Fatalf("seed api_keys failed: %v", err)
	}

	q := &sqlAPIKeyQuerier{db: db}
	res, err := q.Query(context.Background(),
		"SELECT namespaces.name, api_keys.scopes FROM api_keys JOIN namespaces ON api_keys.namespace_id = namespaces.id WHERE api_keys.key = ? AND api_keys.revoked_at IS NULL LIMIT 1",
		"revoked_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 0 {
		t.Fatalf("expected revoked key to yield 0 rows, got count=%d", res.Count)
	}
}

func TestSqlAPIKeyQuerier_NilDB(t *testing.T) {
	q := &sqlAPIKeyQuerier{}
	_, err := q.Query(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for nil db, got nil")
	}
}

func TestSqlAPIKeyQuerier_QueryErrorPropagates(t *testing.T) {
	db := newTestSQLiteDB(t)
	q := &sqlAPIKeyQuerier{db: db}
	// Malformed SQL must surface as an error, never be treated as "no rows".
	_, err := q.Query(context.Background(), "SELECT * FROM no_such_table")
	if err == nil {
		t.Fatal("expected error for query against a nonexistent table, got nil")
	}
}

// fakeNetworkClient is a minimal client.NetworkClient stand-in used only to
// exercise Gateway.apiKeyDB()'s fallback path.
type fakeNetworkClient struct {
	client.NetworkClient
	db client.DatabaseClient
}

func (f *fakeNetworkClient) Database() client.DatabaseClient { return f.db }

type fakeDatabaseClient struct {
	client.DatabaseClient
}

func TestGateway_ApiKeyDB_PrefersSqlDB(t *testing.T) {
	db := newTestSQLiteDB(t)
	g := &Gateway{sqlDB: db, client: &fakeNetworkClient{db: &fakeDatabaseClient{}}}
	q := g.apiKeyDB()
	if _, ok := q.(*sqlAPIKeyQuerier); !ok {
		t.Fatalf("expected apiKeyDB() to prefer sqlDB, got %T", q)
	}
}

func TestGateway_ApiKeyDB_FallsBackToClient(t *testing.T) {
	fdb := &fakeDatabaseClient{}
	g := &Gateway{client: &fakeNetworkClient{db: fdb}}
	q := g.apiKeyDB()
	if q == nil {
		t.Fatal("expected non-nil querier falling back to client")
	}
	if _, ok := q.(*sqlAPIKeyQuerier); ok {
		t.Fatal("expected fallback to client.DatabaseClient, not sqlAPIKeyQuerier")
	}
}

func TestGateway_ApiKeyDB_NilWhenNeitherAvailable(t *testing.T) {
	g := &Gateway{}
	if q := g.apiKeyDB(); q != nil {
		t.Fatalf("expected nil querier when neither sqlDB nor client is set, got %v", q)
	}
}
