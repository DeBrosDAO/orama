package rqlite

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScanIntoDest_snakeCaseColumnsCamelCaseFields is the end-to-end
// regression guard for the feature #65 cron-scheduler bug. The scanner MUST
// populate CamelCase struct fields from snake_case SQL columns even when
// no `db:` tags are present on the struct.
//
// Pre-fix, rqlite returned valid `function_cron_triggers` rows with
// non-empty `id` / `cron_expression` / `next_run_at`, but the cron
// scheduler scanned them into a `CronDueRow` struct (no db tags) and got
// back zero values: `TriggerID == ""`, `CronExpression == ""`. The
// scheduler then logged "bad expression" every poll tick and never fired
// any function — even though everything from the DB to the goroutine to
// the HTTP API was working.
//
// This test reproduces that exact wiring against an in-memory SQLite db
// so any regression on the snake/Camel mapping fails CI loudly.
func TestScanIntoDest_snakeCaseColumnsCamelCaseFields(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE function_cron_triggers (
			id              TEXT PRIMARY KEY,
			function_id     TEXT NOT NULL,
			cron_expression TEXT NOT NULL,
			next_run_at     TIMESTAMP
		);
		INSERT INTO function_cron_triggers VALUES
			('10a70f79-35b3-4bc6-b173-aec3012b570d',
			 'dfe55b75-9c39-45ce-b60e-c72b131350b2',
			 '*/30 * * * * *',
			 '2026-05-09T05:50:00Z');
	`)
	require.NoError(t, err)

	rows, err := db.Query(`
		SELECT id AS trigger_id, function_id, cron_expression, next_run_at
		FROM function_cron_triggers
	`)
	require.NoError(t, err)
	defer rows.Close()

	// Mirror of pkg/serverless/triggers.CronDueRow — CamelCase fields,
	// NO db tags, snake_case SQL columns. Pre-fix the fields stayed at
	// zero values; post-fix they populate.
	type cronDueRowLike struct {
		TriggerID      string
		FunctionID     string
		CronExpression string
		NextRunAt      time.Time
	}

	var dst []cronDueRowLike
	require.NoError(t, scanIntoDest(rows, &dst))
	require.Len(t, dst, 1)

	got := dst[0]
	assert.Equal(t, "10a70f79-35b3-4bc6-b173-aec3012b570d", got.TriggerID,
		"TriggerID populated from `trigger_id` column (regression guard for feature #65)")
	assert.Equal(t, "dfe55b75-9c39-45ce-b60e-c72b131350b2", got.FunctionID,
		"FunctionID populated from `function_id` column")
	assert.Equal(t, "*/30 * * * * *", got.CronExpression,
		"CronExpression populated from `cron_expression` column")
	assert.False(t, got.NextRunAt.IsZero(),
		"NextRunAt populated from `next_run_at` column")
	assert.Equal(t, 2026, got.NextRunAt.Year())
}

// TestScanIntoDest_explicitDBTagStillTakesPrecedence guarantees that adding
// the snake-case fix didn't break callers that already supplied explicit
// `db:` tags. Tag wins; no double-mapping ambiguity.
func TestScanIntoDest_explicitDBTagStillTakesPrecedence(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE t (custom_col TEXT); INSERT INTO t VALUES ('hello');`)
	require.NoError(t, err)

	rows, err := db.Query(`SELECT custom_col FROM t`)
	require.NoError(t, err)
	defer rows.Close()

	type tagged struct {
		// Field name has nothing to do with the column; only the tag binds it.
		Whatever string `db:"custom_col"`
	}
	var dst []tagged
	require.NoError(t, scanIntoDest(rows, &dst))
	require.Len(t, dst, 1)
	assert.Equal(t, "hello", dst[0].Whatever)
}
