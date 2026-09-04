package namespace

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

type stubDeprov struct{}

func (stubDeprov) DeprovisionCluster(context.Context, int64) error { return nil }

// The schema these tests run against is the real one.
//
// It used to be nine hand-written CREATE TABLEs, and one of them still had the
// audit_events shape from migration 002 — a namespace_id column that migration
// 048 removed. So the delete ran `DELETE FROM audit_events WHERE namespace_id`
// against a table that no longer has the column, the test passed, and on a real
// cluster deleting a namespace failed after its cluster had already been torn
// down.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestDeleteNamespaceRows_removesKeysAndNamespace(t *testing.T) {
	db := migratedDB(t)

	for _, stmt := range []string{
		`INSERT INTO namespaces(id, name) VALUES (42, 'gone'), (43, 'keep')`,
		`INSERT INTO api_keys(id, key, namespace_id, scopes, expires_at)
		   VALUES (1, 'ak_x:gone', 42, '', datetime('now','+90 days')),
		          (2, 'ak_y:keep', 43, '', datetime('now','+90 days'))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	if err := h.deleteNamespaceRows(context.Background(), 42, "gone"); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, db, "SELECT count(*) FROM namespaces WHERE id = 42"); n != 0 {
		t.Errorf("namespace 42 still present")
	}
	if n := countRows(t, db, "SELECT count(*) FROM api_keys WHERE namespace_id = 42"); n != 0 {
		t.Errorf("api_keys for 42 still present")
	}
	if n := countRows(t, db, "SELECT count(*) FROM namespaces WHERE id = 43"); n != 1 {
		t.Errorf("unrelated namespace was deleted")
	}
	if n := countRows(t, db, "SELECT count(*) FROM api_keys WHERE namespace_id = 43"); n != 1 {
		t.Errorf("an unrelated namespace's keys were deleted")
	}
}

// The trail goes with the namespace, and it is keyed by NAME. A name is
// reusable, so leaving the rows behind would open the next tenant's audit trail
// on the previous one's wallet.
func TestDeleteNamespaceRows_removesTheAuditTrailByName(t *testing.T) {
	db := migratedDB(t)

	for _, stmt := range []string{
		`INSERT INTO namespaces(id, name) VALUES (42, 'gone'), (43, 'keep')`,
		`INSERT INTO audit_events(namespace, actor, action, result)
		   VALUES ('gone', '0xowner', 'key.issue', 'success'),
		          ('keep', '0xother', 'key.issue', 'success')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	if err := h.deleteNamespaceRows(context.Background(), 42, "gone"); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, db, "SELECT count(*) FROM audit_events WHERE namespace = 'gone'"); n != 0 {
		t.Errorf("%d audit events of the deleted namespace remain", n)
	}
	if n := countRows(t, db, "SELECT count(*) FROM audit_events WHERE namespace = 'keep'"); n != 1 {
		t.Errorf("another namespace's audit trail was deleted")
	}
}

// Every table listed as a namespace child is deleted by namespace_id, so one
// that is not keyed that way makes the whole deletion fail — after the cluster
// has already been torn down.
func TestNamespaceFKChildren_areAllKeyedByNamespaceID(t *testing.T) {
	db := migratedDB(t)

	for _, table := range namespaceFKChildren {
		if n := countRows(t, db,
			"SELECT count(*) FROM pragma_table_info(?) WHERE name = 'namespace_id'", table); n != 1 {
			t.Errorf("%s is listed as a namespace child but has no namespace_id column", table)
		}
	}
}

// A namespace whose rows could not be deleted must say so. The cluster is torn
// down before this runs, so a swallowed error leaves a namespace that answers
// nothing and still holds keys.
func TestDeleteNamespaceRows_surfacesAFailedChildDelete(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`DROP TABLE api_keys`); err != nil {
		t.Fatal(err)
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	err := h.deleteNamespaceRows(context.Background(), 42, "gone")
	if err == nil {
		t.Fatal("a child table that could not be emptied was reported as a success")
	}
	if !strings.Contains(err.Error(), "api_keys") {
		t.Errorf("error = %v, want it to name the table", err)
	}
}

func TestDeleteNamespaceRows_surfacesAFailedAuditDelete(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`DROP TABLE audit_events`); err != nil {
		t.Fatal(err)
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	err := h.deleteNamespaceRows(context.Background(), 42, "gone")
	if err == nil {
		t.Fatal("an audit trail that could not be cleared was reported as a success")
	}
	if !strings.Contains(err.Error(), "audit_events") {
		t.Errorf("error = %v, want it to name the table", err)
	}
}

// The namespace row itself is the last thing to go: leaving it behind means the
// name stays taken by a namespace with nothing behind it.
func TestDeleteNamespaceRows_surfacesAFailedNamespaceDelete(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`DROP TABLE namespaces`); err != nil {
		t.Fatal(err)
	}

	h := NewDeleteHandler(stubDeprov{}, rqlite.NewClient(db), nil, nil, zap.NewNop())
	err := h.deleteNamespaceRows(context.Background(), 42, "gone")
	if err == nil {
		t.Fatal("a namespace row that could not be deleted was reported as a success")
	}
}
