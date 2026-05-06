package rqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// openTestDB returns an in-memory SQLite database for migration-runner tests.
// SQLite gives us identical semantics for the error messages we tolerate
// ("duplicate column name", "table ... already exists"), so we don't need a
// real RQLite to verify the idempotency logic.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestIsAlreadyAppliedError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
		name string
	}{
		{nil, false, "nil"},
		{errors.New("duplicate column name: foo"), true, "duplicate column"},
		{errors.New("Duplicate Column Name: foo"), true, "case insensitive duplicate column"},
		{errors.New("table foo already exists"), true, "table already exists"},
		{errors.New("index idx_x already exists"), true, "index already exists"},
		{errors.New("syntax error near token"), false, "real error"},
		{errors.New("no such table: missing"), false, "no such table is NOT idempotent"},
		{errors.New("UNIQUE constraint failed"), false, "UNIQUE violation is NOT idempotent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAlreadyAppliedError(c.err); got != c.want {
				t.Errorf("isAlreadyAppliedError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestApplySQL_idempotent_alter_add_column(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}

	// First apply: adds column.
	script := `ALTER TABLE t ADD COLUMN x INTEGER DEFAULT 0;`
	if err := applySQL(context.Background(), db, script); err != nil {
		t.Fatalf("first apply failed: %v", err)
	}

	// Second apply: column already exists. Must NOT return an error —
	// this is the critical idempotency property the AnChat-test bug
	// hit: a re-run had to succeed without operator intervention.
	if err := applySQL(context.Background(), db, script); err != nil {
		t.Fatalf("second apply (idempotent re-run) failed: %v", err)
	}

	// Verify column is there exactly once and queryable.
	if _, err := db.Exec("INSERT INTO t (x) VALUES (42)"); err != nil {
		t.Fatalf("INSERT after re-apply failed: %v", err)
	}
}

func TestApplySQL_idempotent_create_table(t *testing.T) {
	db := openTestDB(t)
	script := `CREATE TABLE foo (id INTEGER);`
	if err := applySQL(context.Background(), db, script); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := applySQL(context.Background(), db, script); err != nil {
		t.Fatalf("re-apply CREATE TABLE without IF NOT EXISTS should be tolerated: %v", err)
	}
}

func TestApplySQL_real_errors_still_fail(t *testing.T) {
	db := openTestDB(t)
	// A genuine syntax error must still propagate — we don't want
	// to swallow real bugs.
	err := applySQL(context.Background(), db, "ALTER TABLE nonexistent_table ADD COLUMN x INT;")
	if err == nil {
		t.Fatal("expected error for ALTER on missing table")
	}
}

func TestApplyEmbeddedMigrations_partial_apply_can_recover(t *testing.T) {
	db := openTestDB(t)

	// Simulate the AnChat scenario: someone manually added one of the
	// columns from migration 025 (e.g. via direct rqlite query during
	// debugging) but the schema_migrations row was never recorded.
	if _, err := db.Exec("CREATE TABLE functions (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE functions ADD COLUMN ws_persistent BOOLEAN DEFAULT FALSE"); err != nil {
		t.Fatal(err)
	}

	// Now run a migration that tries to add ws_persistent again + 3 new columns.
	embeddedFS := fstest.MapFS{
		"001_persistent_ws.sql": &fstest.MapFile{
			Data: []byte(`
				ALTER TABLE functions ADD COLUMN ws_persistent BOOLEAN DEFAULT FALSE;
				ALTER TABLE functions ADD COLUMN ws_idle_timeout_sec INTEGER DEFAULT 0;
				ALTER TABLE functions ADD COLUMN ws_max_frame_bytes INTEGER DEFAULT 0;
				ALTER TABLE functions ADD COLUMN ws_max_inflight_per_conn INTEGER DEFAULT 0;
			`),
		},
	}

	if err := ApplyEmbeddedMigrations(context.Background(), db, embeddedFS, zap.NewNop()); err != nil {
		t.Fatalf("partial-state apply should succeed: %v", err)
	}

	// All four columns must be present and writable.
	_, err := db.Exec(`INSERT INTO functions (id, name, ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn) VALUES (?, ?, ?, ?, ?, ?)`,
		1, "test", true, 30, 65536, 64)
	if err != nil {
		t.Fatalf("INSERT after recovery failed: %v", err)
	}

	// schema_migrations must now record version 1 — re-runs are no-ops.
	var v int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil {
		t.Fatalf("schema_migrations query: %v", err)
	}
	if v != 1 {
		t.Errorf("expected schema_migrations to record version 1, got %d", v)
	}
}

func TestApplyEmbeddedMigrations_re_run_is_noop(t *testing.T) {
	db := openTestDB(t)
	embeddedFS := fstest.MapFS{
		"001_initial.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE foo (id INTEGER PRIMARY KEY);"),
		},
	}

	for i := 0; i < 3; i++ {
		if err := ApplyEmbeddedMigrations(context.Background(), db, embeddedFS, zap.NewNop()); err != nil {
			t.Fatalf("apply iter %d: %v", i, err)
		}
	}

	// Schema-migrations should have version 1 exactly once.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 schema_migrations row, got %d", count)
	}
}

func TestApplyEmbeddedMigrations_genuine_failure_aborts(t *testing.T) {
	db := openTestDB(t)
	embeddedFS := fstest.MapFS{
		"001_bad.sql": &fstest.MapFile{
			Data: []byte("THIS IS NOT VALID SQL;"),
		},
	}

	err := ApplyEmbeddedMigrations(context.Background(), db, embeddedFS, zap.NewNop())
	if err == nil {
		t.Fatal("expected error for invalid SQL")
	}
	if !strings.Contains(err.Error(), "apply migration") {
		t.Errorf("error message lacks migration context: %v", err)
	}

	// schema_migrations row must NOT have been written.
	var v int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&v); err == nil {
		if v != 0 {
			t.Errorf("expected no schema_migrations row, got version %d", v)
		}
	}
}
