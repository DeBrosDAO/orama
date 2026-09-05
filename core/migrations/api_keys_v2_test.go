package migrations_test

// Migration 051 rebuilds api_keys so that a key cannot exist without an expiry
// or without a grant set.
//
// Neither could be added in place: SQLite cannot put a NOT NULL constraint on
// an existing column. The rebuild is the point — a constraint the database
// enforces rather than a rule every minting path has to remember.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// registryWithOldKeys is a database holding keys minted before expiry existed.
func registryWithOldKeys(t *testing.T) *sql.DB {
	t.Helper()
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore(t, "051"), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 051: %v", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO namespaces(id, name) VALUES (10, 'anchat')`); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	// One minted long ago, one minted yesterday, one already revoked.
	if _, err := db.Exec(`INSERT INTO api_keys(id, key, name, namespace_id, scopes, created_at) VALUES
		(300, 'hash-old', 'ancient', 10, 'admin', '2024-01-01 00:00:00'),
		(301, 'hash-new', 'recent', 10, 'invoke,storage', datetime('now', '-1 day'))`); err != nil {
		t.Fatalf("seed keys: %v", err)
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply 051: %v", err)
	}
	return db
}

// Every existing key gets 90 days from the migration, not 90 days from when it
// was minted. Dating the expiry from creation would expire every key older than
// three months the moment this runs — a fleet-wide outage dressed up as a
// security improvement.
func TestMigration051_givesExistingKeysAWindowNotAnImmediateDeath(t *testing.T) {
	db := registryWithOldKeys(t)

	for _, id := range []int{300, 301} {
		var expires time.Time
		if err := db.QueryRow(`SELECT expires_at FROM api_keys WHERE id = ?`, id).Scan(&expires); err != nil {
			t.Fatalf("read key %d: %v", id, err)
		}
		if !expires.After(time.Now().Add(80 * 24 * time.Hour)) {
			t.Errorf("key %d expires %s, which is not the window this migration is supposed to give it",
				id, expires)
		}
	}
}

func TestMigration051_keepsWhatTheKeysAlreadyHad(t *testing.T) {
	db := registryWithOldKeys(t)

	var key, name, scopes string
	var nsID int
	if err := db.QueryRow(
		`SELECT key, name, namespace_id, scopes FROM api_keys WHERE id = 300`).Scan(&key, &name, &nsID, &scopes); err != nil {
		t.Fatalf("read the key: %v", err)
	}
	if key != "hash-old" || name != "ancient" || nsID != 10 || scopes != "admin" {
		t.Errorf("the rebuild changed the row: %q %q %d %q", key, name, nsID, scopes)
	}
}

// The constraint is the whole reason for a rebuild. A minting path that forgets
// to say when a key dies has to fail at the database.
func TestMigration051_aKeyCannotExistWithoutAnExpiry(t *testing.T) {
	db := registryWithOldKeys(t)

	if _, err := db.Exec(
		`INSERT INTO api_keys(key, name, namespace_id, scopes) VALUES ('hash-x', '', 10, 'admin')`); err == nil {
		t.Error("a key with no expiry was written; a bearer token that works until somebody " +
			"remembers to revoke it is what this migration exists to end")
	}
	if _, err := db.Exec(
		`INSERT INTO api_keys(key, name, namespace_id, scopes, expires_at) VALUES ('hash-y', '', 10, 'admin', NULL)`); err == nil {
		t.Error("a NULL expiry was accepted")
	}
	if _, err := db.Exec(
		`INSERT INTO api_keys(key, name, namespace_id, scopes, expires_at) VALUES ('hash-z', '', 10, 'admin', datetime('now', '+1 day'))`); err != nil {
		t.Errorf("a key with an expiry was refused: %v", err)
	}
}

func TestMigration051_theOldTableIsGone(t *testing.T) {
	db := registryWithOldKeys(t)

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'api_keys_new'`).Scan(&count); err != nil {
		t.Fatalf("look for the scratch table: %v", err)
	}
	if count != 0 {
		t.Error("api_keys_new survived the rename")
	}
}
