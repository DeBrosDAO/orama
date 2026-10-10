package migrations_test

// Every migration must be safe to run twice.
//
// The runner takes a cluster-wide lock now, so a re-run is not the normal path
// — but the lock is TTL-bounded, and a runner that dies mid-file leaves the
// file unrecorded, so the next start re-applies it from the beginning. DDL
// survives that because it is guarded by IF NOT EXISTS. DML is not guarded by
// anything, and migration 019
// (`UPDATE refresh_tokens SET revoked_at = ... WHERE revoked_at IS NULL`)
// revokes every token issued since the first run.
//
// So: apply everything, snapshot the database, apply everything again, and
// assert nothing moved.

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

func TestMigrations_areIdempotent(t *testing.T) {
	db := openRoundtripDB(t)

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	seedIdempotenceFixtures(t, db)
	before := snapshotDatabase(t, db)

	// Forget that the migrations ran, so the second pass really re-applies
	// them rather than skipping on schema_migrations.
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear schema_migrations: %v", err)
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	after := snapshotDatabase(t, db)

	for table, beforeRows := range before {
		afterRows, ok := after[table]
		if !ok {
			t.Errorf("%s disappeared on the second apply", table)
			continue
		}
		if beforeRows != afterRows {
			t.Errorf("%s changed on re-apply: %s → %s\n"+
				"  A migration's DML is not re-runnable. Guard it — a WHERE against a marker "+
				"column, or INSERT OR IGNORE — so a retried apply is a no-op.",
				table, beforeRows, afterRows)
		}
	}
}

// seedIdempotenceFixtures puts rows in the tables whose migrations carry DML,
// so a non-idempotent statement has something to damage. Without them every
// UPDATE touches zero rows and the test passes vacuously.
func seedIdempotenceFixtures(t *testing.T, db *sql.DB) {
	t.Helper()

	// A live refresh token in the CURRENT format — a 64-char SHA-256 hash —
	// against the namespace the migrations create, so no fixture perturbs a
	// table a migration derives from another.
	//
	// This is the production failure: migration 019 revoked every unrevoked
	// token, so a node re-reaching it after a crashed apply logged out
	// everyone who had signed in since. Seeding a legitimately-issued token
	// and asserting it survives a re-apply is the whole point.
	//
	// A PLAINTEXT token is deliberately not seeded: one appearing after the
	// migration ran would be a bug elsewhere, and revoking it is correct.
	seedIfTableExists(t, db, "refresh_tokens",
		`INSERT INTO refresh_tokens (namespace_id, subject, token, expires_at)
		 VALUES (1, 'idempotence-hashed', '`+strings.Repeat("a", 64)+`', datetime('now', '+1 day'))`)
}

// seedIfTableExists inserts only when the table is present, so the fixture set
// does not have to track every schema change.
func seedIfTableExists(t *testing.T, db *sql.DB, table, stmt string) {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		t.Fatalf("look up %s: %v", table, err)
	}
	if _, err := db.Exec(stmt); err != nil {
		// A fixture that no longer matches the schema should be fixed, not
		// silently skipped — it is the only thing making this test non-vacuous.
		t.Fatalf("seed %s: %v\n  Update the fixture to match the current schema.", table, err)
	}
}

// snapshotDatabase records every user table's row count and a checksum of its
// contents, so a changed value is caught as well as a changed count.
func snapshotDatabase(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		if name == "schema_migrations" {
			// Cleared deliberately between passes.
			continue
		}
		tables = append(tables, name)
	}
	rows.Close()
	sort.Strings(tables)

	out := make(map[string]string, len(tables))
	for _, table := range tables {
		out[table] = summariseTable(t, db, table)
	}
	return out
}

// summariseTable renders a table as a row count plus its sorted contents.
func summariseTable(t *testing.T, db *sql.DB, table string) string {
	t.Helper()

	// Table names come from sqlite_master, not from input.
	rows, err := db.Query(fmt.Sprintf(`SELECT * FROM %q`, table))
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}

	var lines []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}

		parts := make([]string, len(cols))
		for i, v := range vals {
			switch b := v.(type) {
			case nil:
				parts[i] = cols[i] + "=NULL"
			case []byte:
				parts[i] = cols[i] + "=" + string(b)
			default:
				parts[i] = fmt.Sprintf("%s=%v", cols[i], b)
			}
		}
		lines = append(lines, strings.Join(parts, ","))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", table, err)
	}

	sort.Strings(lines)
	return fmt.Sprintf("%d rows [%s]", len(lines), strings.Join(lines, " | "))
}
