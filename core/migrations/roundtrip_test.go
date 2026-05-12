package migrations_test

// roundtrip_test.go is the build-time guard that prevents
// "binary references column X but X is missing from migrations"
// drift — the bug that triggered the AnChat-test outage on 2026-05-06.
//
// How it works:
//
//  1. Open an in-memory SQLite database.
//  2. Apply EVERY embedded migration in version order.
//  3. Run a series of "exemplar" SQL operations against the resulting
//     schema. If any operation fails, the test fails — meaning either:
//        a. A migration was deleted / renumbered and the schema regressed
//        b. A new migration was added but isn't reachable via embed.FS
//        c. (Most importantly) a Go file references a column / table /
//           index that no migration creates
//
// The exemplars are drawn from the actual SQL strings the platform's
// Go code executes. Adding a new INSERT/SELECT in the gateway → add the
// matching exemplar here so drift is caught at `go test` time, not
// at production deploy.
//
// This is generic by design — every platform table participates. Adding
// a new table doesn't require new test infrastructure, only one new
// exemplar string.

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// TestSchemaRoundtrip_AllMigrationsApplyClean verifies every embedded
// migration applies successfully against a fresh database in version
// order. Failure here means a migration is broken in isolation
// (syntax error, references a missing prior migration's column, etc.).
func TestSchemaRoundtrip_AllMigrationsApplyClean(t *testing.T) {
	db := openRoundtripDB(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("ApplyEmbeddedMigrations failed: %v", err)
	}

	// Sanity: applied version should equal RequiredVersion.
	applied, err := migrations.AppliedVersion(t.Context(), db)
	if err != nil {
		t.Fatalf("AppliedVersion: %v", err)
	}
	if applied != migrations.RequiredVersion() {
		t.Errorf("applied=%d != required=%d after full roundtrip", applied, migrations.RequiredVersion())
	}
}

// TestSchemaRoundtrip_PlatformExemplars exercises representative SQL
// statements from the Go codebase against the migrated schema.
//
// Each exemplar is a string that should EXECUTE successfully (we don't
// care about row counts — only that the SQL parses and binds against
// the schema). Args are placeholders; values can be anything matching
// the column types.
//
// When a Go handler is added that touches a new table or column, add
// an exemplar here. The diff at review time enforces the contract:
// "if you write Go that uses column X, an exemplar exercises it,
// which means migrations must declare X."
func TestSchemaRoundtrip_PlatformExemplars(t *testing.T) {
	db := openRoundtripDB(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("ApplyEmbeddedMigrations: %v", err)
	}

	// Each exemplar is (table, sql, args). The args don't have to satisfy
	// constraints — we use Prepare to validate column references without
	// actually running mutations. Statements that have to execute (because
	// SQLite delays some checks) get marked exec=true.
	type exemplar struct {
		name string
		sql  string
		args []any
		exec bool // true: actually execute; false: just Prepare
	}
	exemplars := []exemplar{
		// functions table — bug #214's table, which is why we care.
		// Every column written by the function-store INSERT must be here.
		{
			name: "functions INSERT (full column list incl. ws_*)",
			sql: `INSERT INTO functions (
				id, name, namespace, version, wasm_cid,
				memory_limit_mb, timeout_seconds, is_public,
				retry_count, retry_delay_seconds, dlq_topic,
				status, created_at, updated_at, created_by,
				ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{
				"id-1", "fn", "ns", 1, "cid-1",
				64, 30, false,
				0, 5, "",
				"active", 0, 0, "ns",
				false, 0, 0, 0,
			},
			exec: true,
		},
		{
			name: "functions SELECT (full column list)",
			sql: `SELECT id, name, namespace, version, wasm_cid, source_cid,
				ws_persistent, ws_idle_timeout_sec, ws_max_frame_bytes, ws_max_inflight_per_conn,
				memory_limit_mb, timeout_seconds, is_public,
				retry_count, retry_delay_seconds, dlq_topic,
				status, created_at, updated_at, created_by
				FROM functions WHERE namespace = ? AND name = ?`,
			args: []any{"ns", "fn"},
		},

		// function_invocations — used by the invocation-history view (#211 fix).
		{
			name: "function_invocations INSERT",
			sql: `INSERT INTO function_invocations (
				id, function_id, request_id, trigger_type, caller_wallet,
				input_size, output_size, started_at, completed_at,
				duration_ms, status, error_message, memory_used_mb
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{
				"inv-1", "id-1", "req-A", "http", "0xwallet",
				0, 0, 0, 0,
				0, "success", "", 0.0,
			},
			exec: true,
		},
		{
			name: "function_invocations SELECT for GetInvocations",
			sql: `SELECT i.id, i.request_id, i.trigger_type, i.caller_wallet,
				i.input_size, i.output_size, i.started_at, i.completed_at,
				i.duration_ms, i.status, i.error_message, i.memory_used_mb
				FROM function_invocations i
				JOIN functions f ON i.function_id = f.id
				WHERE f.namespace = ? AND f.name = ?
				ORDER BY i.started_at DESC LIMIT ?`,
			args: []any{"ns", "fn", 50},
		},

		// function_logs — WASM-emitted log lines.
		{
			name: "function_logs INSERT",
			sql: `INSERT INTO function_logs (
				id, function_id, invocation_id, level, message, timestamp
			) VALUES (?, ?, ?, ?, ?, ?)`,
			args: []any{"log-1", "id-1", "inv-1", "info", "hi", 0},
			exec: true,
		},

		// function_pubsub_triggers — wildcard trigger column rename (plan 03).
		// During the dual-column rolling-upgrade window the Go code writes
		// BOTH `topic` (legacy NOT NULL) and `topic_pattern` (new); this
		// exemplar mirrors the actual INSERT and would catch a future
		// migration that drops one column without a corresponding code change.
		{
			name: "function_pubsub_triggers INSERT (dual topic+topic_pattern)",
			sql: `INSERT INTO function_pubsub_triggers (
				id, function_id, topic, topic_pattern,
				enabled, created_at,
				aggregation_window_ms, aggregation_max_batch_size
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{"trig-1", "id-1", "presence:*", "presence:*", true, 0, 0, 0},
			exec: true,
		},

		// push_devices — created by migration 023; encrypted token storage.
		{
			name: "push_devices INSERT",
			sql: `INSERT INTO push_devices (
				id, namespace, user_id, device_id, provider,
				token_encrypted, platform, app_version,
				created_at, updated_at, last_seen
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			args: []any{
				"dev-1", "ns", "u1", "device-A", "ntfy",
				"enc:...", "ios", "1.0",
				0, 0, 0,
			},
			exec: true,
		},

		// namespace_publish_seq — sequence counter from plan 08.
		{
			name: "namespace_publish_seq UPSERT",
			sql: `INSERT INTO namespace_publish_seq (namespace, next_seq, updated_at)
				VALUES (?, ?, ?)
				ON CONFLICT(namespace) DO UPDATE SET
					next_seq = next_seq + 1,
					updated_at = excluded.updated_at`,
			args: []any{"ns", 2, 0},
			exec: true,
		},
	}

	for _, ex := range exemplars {
		t.Run(ex.name, func(t *testing.T) {
			if ex.exec {
				if _, err := db.Exec(ex.sql, ex.args...); err != nil {
					t.Errorf("schema drift: %v\nsql: %s", err, snippet(ex.sql))
				}
				return
			}
			stmt, err := db.Prepare(ex.sql)
			if err != nil {
				t.Errorf("schema drift (Prepare failed): %v\nsql: %s", err, snippet(ex.sql))
				return
			}
			defer func() { _ = stmt.Close() }()
		})
	}
}

// openRoundtripDB returns an in-memory SQLite. Closes automatically on
// test cleanup.
func openRoundtripDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// snippet trims a SQL string to fit on a single error line.
func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 140 {
		return s[:140] + "..."
	}
	return s
}
