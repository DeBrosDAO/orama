package namespace

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type stubDeprov struct{}

func (stubDeprov) DeprovisionCluster(context.Context, int64) error { return nil }

func TestDeleteNamespaceRows_removesKeysAndNamespace(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:delns?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	stmts := []string{
		`CREATE TABLE namespaces (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE api_keys (id INTEGER PRIMARY KEY, key TEXT, namespace_id INTEGER, revoked_at TEXT)`,
		`CREATE TABLE wallet_api_keys (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE namespace_ownership (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE apps (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE nonces (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE subscriptions (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE refresh_tokens (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE audit_events (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`CREATE TABLE namespace_clusters (id INTEGER PRIMARY KEY, namespace_id INTEGER)`,
		`INSERT INTO namespaces(id, name) VALUES (42, 'gone'), (1, 'keep')`,
		`INSERT INTO api_keys(id, key, namespace_id) VALUES (1, 'ak_x:gone', 42), (2, 'ak_y:keep', 1)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, zap.NewNop())
	if err := h.deleteNamespaceRows(context.Background(), 42); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow("SELECT count(*) FROM namespaces WHERE id = 42").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("namespace 42 still present")
	}
	if err := db.QueryRow("SELECT count(*) FROM api_keys WHERE namespace_id = 42").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("api_keys for 42 still present")
	}
	if err := db.QueryRow("SELECT count(*) FROM namespaces WHERE id = 1").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("unrelated namespace was deleted")
	}
}

func TestDeleteNamespaceRows_execErrorSurfaces(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:delnserr?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, zap.NewNop())
	if err := h.deleteNamespaceRows(context.Background(), 1); err == nil {
		t.Fatal("expected error when child tables are missing")
	}
}
