package migrations_test

// Migration 051 gives api_keys an expiry, expand-only: the columns are added in
// place and nothing is constrained yet, because 0.122.x gateways keep minting
// keys without an expiry for the length of the rolling upgrade. The rebuild
// that makes expires_at and scopes NOT NULL is the next release's contract
// migration.

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

// Expand only: during a rolling upgrade 0.122.x gateways still mint keys the
// way they always did — no expiry, and on the wallet path no grant set. Those
// inserts must keep working while the old gateways serve, and the key they
// make must not authenticate on this release, whose lookup requires an expiry
// in the future.
func TestMigration051_oldGatewayInsertsStillWork(t *testing.T) {
	db := registryWithOldKeys(t)

	// 0.122.x's two minting statements, verbatim.
	for _, stmt := range []string{
		`INSERT INTO api_keys(key, name, namespace_id) VALUES ('hash-wallet', '', 10)`,
		`INSERT INTO api_keys(key, name, namespace_id, scopes) VALUES ('hash-scoped', 'ci', 10, 'invoke')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("a 0.122.x insert failed during the rolling window: %v\n%s", err, stmt)
		}
	}

	// This release's lookup (pkg/gateway middleware) does not match them.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys
		WHERE key IN ('hash-wallet', 'hash-scoped') AND revoked_at IS NULL AND expires_at > datetime('now')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d key(s) minted without an expiry authenticate on this release", n)
	}
}

// This release's own mint names the expiry and works.
func TestMigration051_newMintWorks(t *testing.T) {
	db := registryWithOldKeys(t)
	if _, err := db.Exec(
		`INSERT INTO api_keys(key, name, namespace_id, scopes, expires_at, rotated_from) VALUES ('hash-z', '', 10, 'admin', datetime('now', '+1 day'), 300)`); err != nil {
		t.Errorf("a key with an expiry was refused: %v", err)
	}
}

// The columns are added in place: nothing a 0.122.x gateway reads moves.
func TestMigration051_isExpandOnly(t *testing.T) {
	db := registryWithOldKeys(t)
	for _, q := range []string{
		`SELECT id, key, name, namespace_id, scopes, created_at, last_used_at, revoked_at FROM api_keys LIMIT 1`,
		`SELECT expires_at, rotated_from, principal_id FROM api_keys LIMIT 1`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

// Replaying 051 — a runner that dies before recording it — changes nothing:
// the columns exist, and only rows still missing an expiry get one.
func TestMigration051_replayIsANoOp(t *testing.T) {
	db := registryWithOldKeys(t)
	var before string
	if err := db.QueryRow(`SELECT expires_at FROM api_keys WHERE id = 300`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 51`); err != nil {
		t.Fatal(err)
	}
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply 051: %v", err)
	}
	var after string
	if err := db.QueryRow(`SELECT expires_at FROM api_keys WHERE id = 300`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("a replay moved key 300's expiry from %s to %s", before, after)
	}
}

// A replay after 0.122.x gateways minted keys in the window must not give those
// keys the 90-day window: only keys that existed when 051 first ran are
// backfilled, and the contract release revokes the rest.
func TestMigration051_replayDoesNotBackfillWindowKeys(t *testing.T) {
	db := registryWithOldKeys(t)
	if _, err := db.Exec(`INSERT INTO api_keys(key, name, namespace_id) VALUES ('hash-window', '', 10)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 51`); err != nil {
		t.Fatal(err)
	}
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply 051: %v", err)
	}
	var expires sql.NullString
	if err := db.QueryRow(`SELECT expires_at FROM api_keys WHERE key = 'hash-window'`).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.Valid {
		t.Errorf("a key minted in the window was given an expiry (%s) by a replay", expires.String)
	}
	var cutoff int
	if err := db.QueryRow(`SELECT max_key_id FROM api_keys_expiry_cutoff WHERE id = 1`).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	if cutoff != 301 {
		t.Errorf("cutoff = %d, want the highest key id when 051 first ran (301)", cutoff)
	}
}
