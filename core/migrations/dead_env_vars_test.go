package migrations_test

// Migration 046 removes deployment_env_vars.
//
// 007 created it with the comment "separate for security" and a value column
// commented "Plaintext JSON (not encrypted)". Nothing ever wrote to it or read
// from it — a deployment's environment has always been a column on the
// deployments row — so its only effect was to suggest that environment
// variables were held somewhere deliberate while they sat in the clear
// elsewhere.

import (
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&count); err != nil {
		t.Fatalf("look up table %s: %v", name, err)
	}
	return count > 0
}

func TestMigration046_removesTheTableThatClaimedToHoldSecrets(t *testing.T) {
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore(t, "046"), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 046: %v", err)
	}
	if !tableExists(t, db, "deployment_env_vars") {
		t.Fatal("the table this migration removes was never there, so the migration proves nothing")
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply all migrations: %v", err)
	}
	if tableExists(t, db, "deployment_env_vars") {
		t.Error("deployment_env_vars is still there after 046")
	}

	// The deployments table, which is where the environment actually lives,
	// must be untouched.
	if !tableExists(t, db, "deployments") {
		t.Error("the migration took the deployments table with it")
	}
}

// Migrations are replayed on every boot of every node, so applying 046 twice
// has to be as harmless as applying it once.
func TestMigration046_isIdempotent(t *testing.T) {
	db := openRoundtripDB(t)

	for i := 0; i < 3; i++ {
		if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
			t.Fatalf("apply pass %d: %v", i+1, err)
		}
	}
	if tableExists(t, db, "deployment_env_vars") {
		t.Error("deployment_env_vars came back")
	}
}
