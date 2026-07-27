package gateway

import (
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	_ "github.com/mattn/go-sqlite3"
)

// fakeNetworkClient is a minimal client.NetworkClient stand-in used to
// exercise Gateway.apiKeyDB()'s resolution order.
type fakeNetworkClient struct {
	client.NetworkClient
	db client.DatabaseClient
}

func (f *fakeNetworkClient) Database() client.DatabaseClient { return f.db }

type fakeDatabaseClient struct {
	client.DatabaseClient
	name string
}

func TestGateway_ApiKeyDB_PrefersAuthClient(t *testing.T) {
	authDB := &fakeDatabaseClient{name: "auth"}
	clientDB := &fakeDatabaseClient{name: "client"}
	g := &Gateway{
		authClient: &fakeNetworkClient{db: authDB},
		client:     &fakeNetworkClient{db: clientDB},
	}
	q := g.apiKeyDB()
	got, ok := q.(*fakeDatabaseClient)
	if !ok {
		t.Fatalf("expected *fakeDatabaseClient, got %T", q)
	}
	if got != authDB {
		t.Fatalf("expected apiKeyDB() to prefer g.authClient's database, got %q", got.name)
	}
}

func TestGateway_ApiKeyDB_FallsBackToClientWhenAuthClientNil(t *testing.T) {
	clientDB := &fakeDatabaseClient{name: "client"}
	g := &Gateway{client: &fakeNetworkClient{db: clientDB}}
	q := g.apiKeyDB()
	got, ok := q.(*fakeDatabaseClient)
	if !ok {
		t.Fatalf("expected *fakeDatabaseClient, got %T", q)
	}
	if got != clientDB {
		t.Fatalf("expected apiKeyDB() to fall back to g.client's database, got %q", got.name)
	}
}

func TestGateway_ApiKeyDB_NeverUsesSqlDB(t *testing.T) {
	// g.sqlDB is the namespace-bound RQLite handle; it must never be
	// consulted for API-key validation even when set, since its api_keys
	// table is not authoritative (bugboard #151/#152 regression).
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	clientDB := &fakeDatabaseClient{name: "client"}
	g := &Gateway{sqlDB: db, client: &fakeNetworkClient{db: clientDB}}
	q := g.apiKeyDB()
	got, ok := q.(*fakeDatabaseClient)
	if !ok {
		t.Fatalf("expected apiKeyDB() to resolve via g.client, not g.sqlDB; got %T", q)
	}
	if got != clientDB {
		t.Fatalf("expected apiKeyDB() to return g.client's database even with sqlDB set, got %q", got.name)
	}
}

func TestGateway_ApiKeyDB_NilWhenNeitherAvailable(t *testing.T) {
	g := &Gateway{}
	if q := g.apiKeyDB(); q != nil {
		t.Fatalf("expected nil querier when neither authClient nor client is set, got %v", q)
	}
}
