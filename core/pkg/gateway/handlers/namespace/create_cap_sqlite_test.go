package namespace

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// The cap is the statement's, so it is tested as SQL: on a real SQLite, the
// owner grant is written while the wallet is under its cap and writes nothing
// at it. Revoked and non-owner grants do not count.
func TestOwnerGrantUnderCap_againstSQLite(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE principals (id INTEGER PRIMARY KEY, type TEXT, identifier TEXT);
		CREATE TABLE grants (id INTEGER PRIMARY KEY, principal_id INTEGER, namespace_id INTEGER,
			role TEXT, created_by TEXT, revoked_at TIMESTAMP);
		INSERT INTO principals (id, type, identifier) VALUES (1, 'wallet', '0xowner'), (2, 'wallet', '0xother');
		INSERT INTO grants (principal_id, namespace_id, role) VALUES (1, 1, 'owner'), (1, 2, 'admin'), (2, 3, 'owner');
		INSERT INTO grants (principal_id, namespace_id, role, revoked_at) VALUES (1, 4, 'owner', datetime('now'));`); err != nil {
		t.Fatal(err)
	}
	grant := func(nsID int, walletCap int) int64 {
		t.Helper()
		res, err := db.Exec(ownerGrantUnderCap, nsID, "0xowner", "0xowner", "0xowner", walletCap)
		if err != nil {
			t.Fatal(err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// One live owner grant: a cap of two admits one more, then refuses.
	if n := grant(10, 2); n != 1 {
		t.Fatalf("under the cap the grant wrote %d rows, want 1", n)
	}
	if n := grant(11, 2); n != 0 {
		t.Fatalf("at the cap the grant wrote %d rows, want 0", n)
	}
}
