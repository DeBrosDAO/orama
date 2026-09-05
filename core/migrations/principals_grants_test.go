package migrations_test

// Migration 050 turns one boolean row into a principal and a grant.
//
// namespace_ownership said only "owner or nothing". Everything a team needs
// between those two — a second person who may deploy but not mint keys, a
// service account that is not an owner, a grant that expires — had nowhere to
// live. The backfill has to move what is there without changing who can do
// what, and the single-owner invariant has to survive the move: it is the fix
// for the namespace-takeover bug, and losing it in a schema change would undo
// that quietly.

import (
	"database/sql"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// registryAtV43 is a database holding what the old table held, migrated all the
// way up.
func registryAtV43(t *testing.T) *sql.DB {
	t.Helper()
	db := takenOverRegistry(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply the rest of the migrations: %v", err)
	}
	return db
}

func grantRole(t *testing.T, db *sql.DB, principalType, identifier string, namespaceID int) string {
	t.Helper()
	var role string
	err := db.QueryRow(
		`SELECT g.role FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE p.type = ? AND p.identifier = ? AND g.namespace_id = ? AND g.revoked_at IS NULL`,
		principalType, identifier, namespaceID).Scan(&role)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("read grant for %s %s: %v", principalType, identifier, err)
	}
	return role
}

func TestMigration050_theWalletOwnerBecomesAnOwnerGrant(t *testing.T) {
	db := registryAtV43(t)

	if role := grantRole(t, db, "wallet", "0xcreator", 10); role != "owner" {
		t.Errorf("0xcreator has role %q in namespace 10, want owner", role)
	}
	if role := grantRole(t, db, "wallet", "0xelsewhere", 11); role != "owner" {
		t.Errorf("0xelsewhere has role %q in namespace 11, want owner", role)
	}

	// 043 removed the wallets that took the namespace over. They must not come
	// back as principals with a grant.
	for _, wallet := range []string{"0xsquatter", "0xanother"} {
		if role := grantRole(t, db, "wallet", wallet, 10); role != "" {
			t.Errorf("%s came back with role %q; 043 took that authority away", wallet, role)
		}
	}
}

// A key's ownership row was how it reached its namespace at all. Losing it
// would lock every application out of its own namespace.
func TestMigration050_keysKeepReachingTheirNamespace(t *testing.T) {
	db := registryAtV43(t)

	// hash-a and hash-b were minted with no scopes, which 043 wrote as admin,
	// so their grant is the control-plane one.
	for _, key := range []string{"hash-a", "hash-b"} {
		if role := grantRole(t, db, "service_account", key, 10); role != "admin" {
			t.Errorf("key %s has role %q, want admin — 043 made its implied grant explicit", key, role)
		}
	}
}

// A namespace has one owner, and the database is what says so. This is
// migration 043's invariant, moved.
func TestMigration050_aSecondOwnerIsRefusedByTheDatabase(t *testing.T) {
	db := registryAtV43(t)

	if _, err := db.Exec(`INSERT INTO principals(type, identifier) VALUES ('wallet', '0xlate')`); err != nil {
		t.Fatalf("create principal: %v", err)
	}
	_, err := db.Exec(
		`INSERT INTO grants(principal_id, namespace_id, role)
		 SELECT id, 10, 'owner' FROM principals WHERE identifier = '0xlate'`)
	if err == nil {
		t.Fatal("a second owner was accepted; the invariant is a comment, not a constraint")
	}

	// Every other role may be held alongside the owner: that is the point.
	if _, err := db.Exec(
		`INSERT INTO grants(principal_id, namespace_id, role)
		 SELECT id, 10, 'admin' FROM principals WHERE identifier = '0xlate'`); err != nil {
		t.Errorf("a second admin was refused: %v", err)
	}
}

// A revoked owner grant frees the namespace, or ownership could never be
// transferred.
func TestMigration050_arevokedOwnerLetsANewOneIn(t *testing.T) {
	db := registryAtV43(t)

	if _, err := db.Exec(
		`UPDATE grants SET revoked_at = datetime('now') WHERE namespace_id = 10 AND role = 'owner'`); err != nil {
		t.Fatalf("revoke the owner: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO principals(type, identifier) VALUES ('wallet', '0xnext')`); err != nil {
		t.Fatalf("create principal: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO grants(principal_id, namespace_id, role)
		 SELECT id, 10, 'owner' FROM principals WHERE identifier = '0xnext'`); err != nil {
		t.Errorf("a namespace whose owner was revoked would not accept a new one: %v", err)
	}
}

// Granting the same thing twice must leave one row, or revoking it looks like
// it worked and does not.
func TestMigration050_oneLiveGrantPerShape(t *testing.T) {
	db := registryAtV43(t)

	if _, err := db.Exec(`INSERT INTO principals(type, identifier) VALUES ('wallet', '0xteam')`); err != nil {
		t.Fatalf("create principal: %v", err)
	}
	insert := func(resource any) error {
		_, err := db.Exec(
			`INSERT INTO grants(principal_id, namespace_id, role, resource)
			 SELECT id, 10, 'runtime', ? FROM principals WHERE identifier = '0xteam'`, resource)
		return err
	}

	if err := insert(nil); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if err := insert(nil); err == nil {
		t.Error("the same grant was recorded twice")
	}
	// A grant narrowed to a resource is a different grant, not a duplicate.
	if err := insert("storage:avatars/*"); err != nil {
		t.Errorf("a resource-scoped grant was refused as a duplicate of the whole-role one: %v", err)
	}
}

// The old table is gone rather than left as a view: 002_core.sql creates that
// name as a table and then indexes it, and a database replaying the chain from
// the beginning cannot index a view.
func TestMigration050_theOldTableIsGone(t *testing.T) {
	db := registryAtV43(t)

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'namespace_ownership'`).Scan(&count); err != nil {
		t.Fatalf("look for the old table: %v", err)
	}
	if count != 0 {
		t.Error("namespace_ownership is still there; two places to read authorization from is how they disagree")
	}
}

// Applying the backfill twice must not double the grants.
func TestMigration050_backfillIsIdempotent(t *testing.T) {
	db := registryAtV43(t)

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM grants`).Scan(&before); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if before == 0 {
		t.Fatal("the backfill wrote nothing, so this proves nothing")
	}

	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 50`); err != nil {
		t.Fatalf("forget migration 50: %v", err)
	}
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("re-apply 050: %v", err)
	}

	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM grants`).Scan(&after); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if after != before {
		t.Errorf("grants went from %d to %d on a re-apply", before, after)
	}
}
