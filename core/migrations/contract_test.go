package migrations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// openTestDB returns an in-memory SQLite database. The migrations contract
// only cares about ANSI-ish SQL (CREATE TABLE, SELECT MAX, INSERT) — we
// don't need RQLite's distributed semantics for these tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func ensureMigrationsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
}

func TestRequiredVersion_matches_highest_embedded(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("no embedded migrations — embed.FS broken?")
	}
	want := all[len(all)-1].Version
	if got := RequiredVersion(); got != want {
		t.Errorf("RequiredVersion() = %d, want %d", got, want)
	}
}

func TestAll_returns_sorted_copy(t *testing.T) {
	a := All()
	for i := 1; i < len(a); i++ {
		if a[i-1].Version >= a[i].Version {
			t.Errorf("All() not sorted: %d before %d", a[i-1].Version, a[i].Version)
		}
	}
	// Mutating the returned slice must not affect subsequent calls.
	if len(a) > 0 {
		a[0].Version = -999
	}
	a2 := All()
	if len(a2) > 0 && a2[0].Version == -999 {
		t.Error("All() returned a shared slice — subsequent calls see mutation")
	}
}

func TestAppliedVersion_empty_returns_zero(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)

	v, err := AppliedVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 0 {
		t.Errorf("expected 0 for empty schema_migrations, got %d", v)
	}
}

func TestAppliedVersion_returns_max(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	for _, v := range []int{1, 5, 3, 10, 7} {
		_, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", v)
		if err != nil {
			t.Fatalf("insert %d: %v", v, err)
		}
	}
	v, err := AppliedVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("AppliedVersion: %v", err)
	}
	if v != 10 {
		t.Errorf("expected 10, got %d", v)
	}
}

func TestAppliedVersion_no_table_returns_error(t *testing.T) {
	db := openTestDB(t)
	// Don't create schema_migrations table.
	_, err := AppliedVersion(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when schema_migrations missing")
	}
}

func TestAssertSchema_ok_when_at_required(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	_, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", RequiredVersion())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := AssertSchema(context.Background(), db); err != nil {
		t.Errorf("AssertSchema returned error when at required version: %v", err)
	}
}

func TestAssertSchema_ok_when_above_required(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	_, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", RequiredVersion()+10)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := AssertSchema(context.Background(), db); err != nil {
		t.Errorf("AssertSchema returned error when ahead of required: %v", err)
	}
}

func TestAssertSchema_fails_when_below_required(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	// Seed only the first migration.
	_, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", 1)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	err = AssertSchema(context.Background(), db)
	if err == nil {
		t.Fatal("expected SchemaMismatchError, got nil")
	}
	var smErr *SchemaMismatchError
	if !errors.As(err, &smErr) {
		t.Fatalf("expected *SchemaMismatchError, got %T: %v", err, err)
	}
	if smErr.RequiredVersion != RequiredVersion() {
		t.Errorf("RequiredVersion mismatch: got %d, want %d", smErr.RequiredVersion, RequiredVersion())
	}
	if smErr.AppliedVersion != 1 {
		t.Errorf("AppliedVersion mismatch: got %d, want 1", smErr.AppliedVersion)
	}
	if len(smErr.Pending) == 0 {
		t.Error("expected pending migrations list, got empty")
	}

	// Error message must contain the actionable hint.
	if !strings.Contains(err.Error(), "orama node migrate") {
		t.Errorf("error message lacks actionable hint: %v", err)
	}
}

func TestPendingMigrations_empty_when_at_required(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	_, _ = db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", RequiredVersion())

	pending, err := PendingMigrations(context.Background(), db)
	if err != nil {
		t.Fatalf("PendingMigrations: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending))
	}
}

func TestPendingMigrations_lists_all_when_empty_db(t *testing.T) {
	db := openTestDB(t)
	ensureMigrationsTable(t, db)
	pending, err := PendingMigrations(context.Background(), db)
	if err != nil {
		t.Fatalf("PendingMigrations: %v", err)
	}
	if len(pending) != len(All()) {
		t.Errorf("expected %d pending (all), got %d", len(All()), len(pending))
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
		ok   bool
	}{
		{"valid 3-digit", "001_initial.sql", 1, true},
		{"valid 25", "025_persistent_ws.sql", 25, true},
		{"valid 100", "100_future.sql", 100, true},
		{"no underscore", "999.sql", 0, false},
		{"non-numeric prefix", "abc_initial.sql", 0, false},
		{"empty", "", 0, false},
		{"only underscore", "_x.sql", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseVersion(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("parseVersion(%q) = (%d, %v), want (%d, %v)",
					c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestSchemaMismatchError_message_lists_pending(t *testing.T) {
	e := &SchemaMismatchError{
		RequiredVersion: 25,
		AppliedVersion:  22,
		Pending: []MigrationInfo{
			{Version: 23, Name: "push_devices"},
			{Version: 24, Name: "namespace_publish_seq"},
			{Version: 25, Name: "persistent_ws"},
		},
	}
	msg := e.Error()
	for _, want := range []string{"025", "024", "023", "push_devices", "namespace_publish_seq", "persistent_ws", "orama node migrate"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}
