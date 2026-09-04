package migrations_test

// Migration 043 is the data half of the namespace-takeover fix: the code stops
// writing a second wallet owner, and this brings the rows that were already
// written in line with that rule.
//
// The rows exist because ownership was a side effect of minting a key. Any
// wallet that signed a fresh nonce and named an existing namespace in the body
// of /v1/auth/verify became a co-owner of it, and the key it got back was
// minted with no scopes, which the read path treated as admin. So a production
// registry holds namespaces with several wallet owners and live keys with no
// scope set, and both have to be resolved before the new rules can hold.

import (
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// migrationsBefore43 is the embedded migration set with 043 removed, so a test
// can reach the schema as it was, seed the rows the bug produced, and then let
// the real runner apply 043 over them.
func migrationsBefore43(t *testing.T) fs.FS {
	t.Helper()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	out := fstest.MapFS{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasPrefix(name, "043_") {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out[name] = &fstest.MapFile{Data: body}
	}
	if len(out) == 0 {
		t.Fatal("no migrations before 043 — the filter is wrong")
	}
	return out
}

// takenOverRegistry is a database in the state the bug produced: one namespace
// with three wallet owners, another with one, and keys minted with no scopes.
func takenOverRegistry(t *testing.T) *sql.DB {
	t.Helper()
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrationsBefore43(t), zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 043: %v", err)
	}

	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("seed %q: %v", snippet(stmt), err)
		}
	}

	exec(`INSERT OR IGNORE INTO namespaces(id, name) VALUES (10, 'anchat'), (11, 'other')`)

	// The creator first, then two logins that should have been refused.
	exec(`INSERT INTO namespace_ownership(id, namespace_id, owner_type, owner_id) VALUES
		(100, 10, 'wallet', '0xcreator'),
		(101, 10, 'wallet', '0xsquatter'),
		(102, 10, 'wallet', '0xanother'),
		(103, 11, 'wallet', '0xelsewhere')`)

	// api_key ownership rows are a different owner_type and must survive: they
	// are how a minted key reaches the namespace it belongs to.
	exec(`INSERT INTO namespace_ownership(id, namespace_id, owner_type, owner_id) VALUES
		(104, 10, 'api_key', 'hash-a'),
		(105, 10, 'api_key', 'hash-b')`)

	exec(`INSERT INTO api_keys(id, key, name, namespace_id, scopes) VALUES
		(200, 'hash-a', '', 10, NULL),
		(201, 'hash-b', '', 10, ''),
		(202, 'hash-c', '', 10, '   '),
		(203, 'hash-d', '', 10, 'invoke,storage')`)
	exec(`INSERT INTO api_keys(id, key, name, namespace_id, scopes, revoked_at) VALUES
		(204, 'hash-e', '', 10, NULL, '2026-01-01 00:00:00')`)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply 043: %v", err)
	}
	return db
}

func walletOwners(t *testing.T, db *sql.DB, namespaceID int) []string {
	t.Helper()
	rows, err := db.Query(
		`SELECT owner_id FROM namespace_ownership
		  WHERE namespace_id = ? AND owner_type = 'wallet' ORDER BY id`, namespaceID)
	if err != nil {
		t.Fatalf("read owners: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan owner: %v", err)
		}
		out = append(out, owner)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read owners: %v", err)
	}
	return out
}

func TestMigration043_keepsOnlyTheFirstWalletOwner(t *testing.T) {
	db := takenOverRegistry(t)

	owners := walletOwners(t, db, 10)
	if len(owners) != 1 || owners[0] != "0xcreator" {
		t.Errorf("namespace 10 has owners %v, want only 0xcreator — every later "+
			"wallet row is a login that should have been refused", owners)
	}

	if owners := walletOwners(t, db, 11); len(owners) != 1 || owners[0] != "0xelsewhere" {
		t.Errorf("namespace 11 has owners %v; a namespace with one owner must be untouched", owners)
	}
}

// The api_key rows are how a minted key reaches its namespace. Collapsing
// wallet owners must not take them with it.
func TestMigration043_leavesKeyOwnershipAlone(t *testing.T) {
	db := takenOverRegistry(t)

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM namespace_ownership WHERE owner_type = 'api_key'`).Scan(&count); err != nil {
		t.Fatalf("count key ownership: %v", err)
	}
	if count != 2 {
		t.Errorf("%d api_key ownership rows survived, want 2", count)
	}
}

// Two wallets racing on an unowned namespace both read no owner and both
// insert. The database has to be the thing that decides.
func TestMigration043_aSecondWalletOwnerIsRefusedByTheDatabase(t *testing.T) {
	db := takenOverRegistry(t)

	_, err := db.Exec(
		`INSERT INTO namespace_ownership(namespace_id, owner_type, owner_id)
		 VALUES (10, 'wallet', '0xlate')`)
	if err == nil {
		t.Fatal("a second wallet owner was accepted; the invariant is a comment, not a constraint")
	}

	// A second api_key owner is still fine — a namespace holds several keys.
	if _, err := db.Exec(
		`INSERT INTO namespace_ownership(namespace_id, owner_type, owner_id)
		 VALUES (10, 'api_key', 'hash-f')`); err != nil {
		t.Errorf("a second api_key owner was refused: %v", err)
	}
}

// A namespace with no owner is what the first login writes, and it has to stay
// possible.
func TestMigration043_anUnownedNamespaceStillAcceptsItsFirstOwner(t *testing.T) {
	db := takenOverRegistry(t)

	if _, err := db.Exec(`INSERT INTO namespaces(id, name) VALUES (12, 'fresh')`); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO namespace_ownership(namespace_id, owner_type, owner_id)
		 VALUES (12, 'wallet', '0xfirst')`); err != nil {
		t.Fatalf("the first wallet owner of a fresh namespace was refused: %v", err)
	}
}

// An empty scopes column used to be read as admin. That rule is going away, so
// every key relying on it is given the grant it already had — access does not
// change, it becomes explicit.
func TestMigration043_backfillsTheGrantThatWasBeingInferred(t *testing.T) {
	db := takenOverRegistry(t)

	scopeOf := func(id int) string {
		t.Helper()
		var scopes sql.NullString
		if err := db.QueryRow(`SELECT scopes FROM api_keys WHERE id = ?`, id).Scan(&scopes); err != nil {
			t.Fatalf("read scopes of key %d: %v", id, err)
		}
		return scopes.String
	}

	for _, id := range []int{200, 201, 202} {
		if got := scopeOf(id); got != "admin" {
			t.Errorf("key %d has scopes %q, want admin — an empty column denies from "+
				"here on, so a key that was relying on it would stop working", id, got)
		}
	}

	if got := scopeOf(203); got != "invoke,storage" {
		t.Errorf("key 203 has scopes %q; an explicit scope set must not be widened to admin", got)
	}

	// A revoked key authenticates nothing, and rewriting it would destroy the
	// record of what it could do.
	var scopes sql.NullString
	if err := db.QueryRow(`SELECT scopes FROM api_keys WHERE id = 204`).Scan(&scopes); err != nil {
		t.Fatalf("read revoked key: %v", err)
	}
	if scopes.Valid {
		t.Errorf("a revoked key was given scopes %q; it authenticates nothing", scopes.String)
	}
}
