package migrations_test

// Migration 049 removes phantom_auth_sessions.
//
// 017 created it for the Phantom browser-session flow: the CLI inserted a
// pending row, showed a QR code, and polled an unauthenticated status endpoint
// until a phone completed the sign-in. The completed row carried the minted API
// key in cleartext because the poll had to hand it back, and 036 exists only to
// null the keys that flow left behind.
//
// The flow is gone — Solana wallets sign the same challenge as every other
// wallet — so the table is a credential store with nothing left to serve.

import (
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func TestMigration049_removesThePhantomSessionTable(t *testing.T) {
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore(t, "049"), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 049: %v", err)
	}
	if !tableExists(t, db, "phantom_auth_sessions") {
		t.Fatal("the table this migration removes was never there, so the migration proves nothing")
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply all migrations: %v", err)
	}
	if tableExists(t, db, "phantom_auth_sessions") {
		t.Error("phantom_auth_sessions is still there after 049")
	}

	// The tables the surviving login path uses must be untouched: a Solana
	// wallet still signs a nonce and still gets an API key.
	for _, name := range []string{"nonces", "api_keys"} {
		if !tableExists(t, db, name) {
			t.Errorf("the migration took %s with it", name)
		}
	}
}

// A runner that dies after applying a file but before recording its version
// re-applies that file from the beginning on the next boot — and this one runs
// against a database whose table is already gone. Without IF EXISTS the DROP
// fails there and the gateway never starts.
//
// Re-running everything is not that case: 017 recreates the table on the way
// back through, so 049 finds it again. Forgetting 049 alone is.
func TestMigration049_survivesBeingReappliedAfterItSucceeded(t *testing.T) {
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 49`); err != nil {
		t.Fatalf("forget migration 49: %v", err)
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply 049 on a database it already ran against: %v", err)
	}
	if tableExists(t, db, "phantom_auth_sessions") {
		t.Error("phantom_auth_sessions came back")
	}
}
