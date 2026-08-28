package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// namespaceMigrationsTracker is the version-bookkeeping table used when applying
// the embedded core schema to a per-NAMESPACE RQLite (bugboard #150).
//
// It is deliberately NOT "schema_migrations". A namespace RQLite is ALSO the
// tenant app's own database (they read/write it via /v1/rqlite and
// /v1/db/sqlite), and "schema_migrations" is the near-universal name an app uses
// for its OWN migration tracker. Sharing that one name caused two failures:
//
//  1. shape collision — the tenant's schema_migrations(version,name,applied_at)
//     and core's schema_migrations(version,applied_at) are different tables under
//     one name; whichever is created first wins and the other's CREATE ... IF NOT
//     EXISTS silently no-ops. If core wins, the tenant's migrator can't add its
//     columns; if the tenant wins, core's INSERT (no name) fails NOT NULL.
//  2. version-number collision — both number migrations from 1, so core's
//     INSERT OR IGNORE and the tenant's inserts fight over the same primary keys,
//     making each think the other's migrations are already applied.
//
// Giving core its own prefixed tracker frees "schema_migrations" for the tenant.
const namespaceMigrationsTracker = "orama_schema_migrations"

// safeIdent guards the (internal, constant) table names we interpolate into DDL,
// since a table name can't be a bound parameter. Defense-in-depth only — every
// call site passes a compile-time constant.
var safeIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ApplyEmbeddedMigrationsNamespace applies the embedded core migrations to a
// per-namespace RQLite using an ISOLATED tracker table, then removes core-owned
// tables whose generic names collide with tenant app schema (bugboard #150).
//
// It is a self-contained sibling of ApplyEmbeddedMigrations; the main-cluster
// path is untouched. Safe to run repeatedly (idempotent) and against a DB that
// already holds either core's or the tenant's version of a colliding table.
func ApplyEmbeddedMigrationsNamespace(ctx context.Context, db *sql.DB, fsys fs.FS, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	if !safeIdent.MatchString(namespaceMigrationsTracker) {
		return fmt.Errorf("invalid tracker identifier %q", namespaceMigrationsTracker)
	}

	if err := ensureTrackerTable(ctx, db, namespaceMigrationsTracker); err != nil {
		return fmt.Errorf("ensure %s: %w", namespaceMigrationsTracker, err)
	}

	// If this namespace DB was previously migrated under the shared
	// "schema_migrations" tracker AND that table is core-owned (no "name"
	// column — a tenant's has one), seed our isolated tracker from it so we
	// don't needlessly re-run every migration. A tenant-owned schema_migrations
	// is never read here.
	if err := seedTrackerFromLegacy(ctx, db, namespaceMigrationsTracker, logger); err != nil {
		return fmt.Errorf("seed %s from legacy schema_migrations: %w", namespaceMigrationsTracker, err)
	}

	files, err := readMigrationFilesFromFS(fsys)
	if err != nil {
		return fmt.Errorf("read embedded migration files: %w", err)
	}
	applied, err := loadAppliedVersionsFrom(ctx, db, namespaceMigrationsTracker)
	if err != nil {
		return fmt.Errorf("load applied versions: %w", err)
	}

	insert := fmt.Sprintf(`INSERT OR IGNORE INTO %s(version) VALUES (?)`, namespaceMigrationsTracker)
	for _, mf := range files {
		if applied[mf.Version] {
			continue
		}
		sqlBytes, err := fs.ReadFile(fsys, mf.Path)
		if err != nil {
			return fmt.Errorf("read embedded migration %s: %w", mf.Path, err)
		}
		logger.Info("Applying namespace migration", zap.Int("version", mf.Version), zap.String("name", mf.Name))
		if err := applySQLNamespace(ctx, db, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", mf.Version, mf.Name, err)
		}
		if _, err := SafeExecContext(db, ctx, insert, mf.Version); err != nil {
			return fmt.Errorf("record migration %d: %w", mf.Version, err)
		}
	}

	// Free the generic table names the tenant app needs to own.
	if err := isolateNamespaceSchema(ctx, db, logger); err != nil {
		return fmt.Errorf("isolate namespace schema: %w", err)
	}
	return nil
}

// namespaceStrippedTables are the core tables whose DDL/DML must NOT be applied
// to a namespace RQLite (bugboard #150):
//
//   - schema_migrations: 13 migration files embed `INSERT OR IGNORE INTO
//     schema_migrations(version) VALUES (N)` as redundant self-recording. In the
//     namespace path core's bookkeeping lives in orama_schema_migrations, and
//     schema_migrations is owned by the tenant — so these writes must not run
//     (they either fail "no such table" on a fresh namespace or hit the tenant's
//     NOT NULL `name`). The runner records the version in the isolated tracker.
//   - subscriptions: the dead pubsub table from 002_core.sql (never read/written
//     by the gateway). Its CREATE and its indexes on subscriptions(namespace_id,
//     topic) must not run — on a namespace where the tenant owns a differently
//     shaped `subscriptions`, the index creation errors "no such column".
//
// Any statement whose target is one of these tables (INSERT INTO / CREATE TABLE /
// CREATE INDEX ... ON) is filtered out of a namespace migration before execution.
var namespaceStrippedTables = []string{"schema_migrations", "subscriptions"}

var stmtTargetRe = regexp.MustCompile(`(?is)\b(?:into|table|on)\s+(?:if\s+not\s+exists\s+)?["'` + "`" + `]?([A-Za-z_][A-Za-z0-9_]*)`)

// stmtTargetsStrippedTable reports whether a SQL statement's target table (the
// object of INTO/TABLE/ON) is one of namespaceStrippedTables.
func stmtTargetsStrippedTable(stmt string) bool {
	for _, m := range stmtTargetRe.FindAllStringSubmatch(stmt, -1) {
		if len(m) < 2 {
			continue
		}
		for _, t := range namespaceStrippedTables {
			if strings.EqualFold(m[1], t) {
				return true
			}
		}
	}
	return false
}

// applySQLNamespace is applySQL for the namespace path: it drops any statement
// that targets a namespace-stripped table (schema_migrations / subscriptions)
// before executing, so core's embedded self-recording and dead-table DDL never
// run against a tenant's database. Same "already applied" tolerance as applySQL.
func applySQLNamespace(ctx context.Context, db *sql.DB, script string) error {
	s := strings.TrimSpace(script)
	if s == "" {
		return nil
	}
	stmts := filterOutTxnControls(splitSQLStatements(s))
	for _, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if stmtTargetsStrippedTable(stmt) {
			continue // core self-recording or dead-table DDL — never in a namespace DB
		}
		if _, err := SafeExecContext(db, ctx, stmt); err != nil {
			if isAlreadyAppliedError(err) {
				continue
			}
			return fmt.Errorf("exec stmt failed: %w (stmt: %s)", err, snippet(stmt))
		}
	}
	return nil
}

// isolateNamespaceSchema removes core-owned tables from a namespace RQLite whose
// generic names collide with tenant app schema (bugboard #150). Both removals are
// strictly guarded so tenant data is NEVER touched.
func isolateNamespaceSchema(ctx context.Context, db *sql.DB, logger *zap.Logger) error {
	// 1) Free "schema_migrations": drop core's leftover tracker (2-column
	//    version/applied_at shape) now that core's bookkeeping lives in
	//    orama_schema_migrations. A tenant's schema_migrations has a "name"
	//    column — that one is left untouched.
	if exists, err := tableExists(ctx, db, "schema_migrations"); err != nil {
		return err
	} else if exists {
		cols, err := tableColumns(ctx, db, "schema_migrations")
		if err != nil {
			return err
		}
		if isCoreTrackerShape(cols) {
			if _, err := SafeExecContext(db, ctx, `DROP TABLE schema_migrations`); err != nil {
				return fmt.Errorf("drop core schema_migrations: %w", err)
			}
			logger.Info("namespace isolation: dropped core-owned schema_migrations (freed for tenant)")
		}
	}

	// 2) Free "subscriptions": drop core's dead pubsub-shape table (created by
	//    002_core.sql, never read/written by the gateway — pubsub is in-memory +
	//    libp2p) ONLY when it still has core's EXACT shape and is empty. A tenant
	//    table under this name (any other columns) or any rows → left untouched.
	if exists, err := tableExists(ctx, db, "subscriptions"); err != nil {
		return err
	} else if exists {
		cols, err := tableColumns(ctx, db, "subscriptions")
		if err != nil {
			return err
		}
		if isCoreSubscriptionsShape(cols) {
			n, err := tableRowCount(ctx, db, "subscriptions")
			if err != nil {
				return err
			}
			if n == 0 {
				if _, err := SafeExecContext(db, ctx, `DROP TABLE subscriptions`); err != nil {
					return fmt.Errorf("drop core subscriptions: %w", err)
				}
				logger.Info("namespace isolation: dropped dead core subscriptions table (freed for tenant)")
			} else {
				logger.Warn("namespace isolation: core-shape subscriptions has rows; leaving untouched",
					zap.Int("rows", n))
			}
		}
	}
	return nil
}

// isCoreSubscriptionsShape reports whether cols are EXACTLY the pubsub-shape
// subscriptions table from 002_core.sql. Exact-set match: any extra/missing
// column (i.e. a tenant's own subscriptions) fails, so we never drop tenant data.
func isCoreSubscriptionsShape(cols []string) bool {
	want := map[string]struct{}{
		"id": {}, "namespace_id": {}, "app_id": {},
		"topic": {}, "endpoint": {}, "created_at": {},
	}
	if len(cols) != len(want) {
		return false
	}
	for _, c := range cols {
		if _, ok := want[strings.ToLower(c)]; !ok {
			return false
		}
	}
	return true
}

// isCoreTrackerShape reports whether cols are EXACTLY core's migration-tracker
// shape {version, applied_at}. Used to decide whether a legacy "schema_migrations"
// on a namespace DB is core's (safe to seed-from and drop) rather than a tenant's
// own tracker. This is an exact-set match, NOT merely "lacks a name column", so a
// tenant tracker such as {version, checksum} or {version, migration_title, ran_at}
// is never seeded-from or dropped (bugboard #150 hardening).
func isCoreTrackerShape(cols []string) bool {
	want := map[string]struct{}{"version": {}, "applied_at": {}}
	if len(cols) != len(want) {
		return false
	}
	for _, c := range cols {
		if _, ok := want[strings.ToLower(c)]; !ok {
			return false
		}
	}
	return true
}

// seedTrackerFromLegacy copies applied versions from a core-owned legacy
// "schema_migrations" into tracker, so an existing namespace DB doesn't re-run
// every migration on the first isolated apply. A tenant-owned schema_migrations
// (has a "name" column) is left entirely alone.
func seedTrackerFromLegacy(ctx context.Context, db *sql.DB, tracker string, logger *zap.Logger) error {
	exists, err := tableExists(ctx, db, "schema_migrations")
	if err != nil || !exists {
		return err
	}
	cols, err := tableColumns(ctx, db, "schema_migrations")
	if err != nil {
		return err
	}
	if !isCoreTrackerShape(cols) {
		return nil // tenant-owned (any shape but core's exact {version,applied_at}); never read from it
	}
	q := fmt.Sprintf(`INSERT OR IGNORE INTO %s(version) SELECT version FROM schema_migrations`, tracker)
	if _, err := SafeExecContext(db, ctx, q); err != nil {
		return fmt.Errorf("seed from legacy schema_migrations: %w", err)
	}
	logger.Info("namespace isolation: seeded isolated tracker from core-owned schema_migrations")
	return nil
}

// ensureTrackerTable creates the given version-tracking table if absent. Mirrors
// ensureMigrationsTable but for an arbitrary (validated) tracker name.
func ensureTrackerTable(ctx context.Context, db *sql.DB, tracker string) error {
	if !safeIdent.MatchString(tracker) {
		return fmt.Errorf("invalid tracker identifier %q", tracker)
	}
	_, err := SafeExecContext(db, ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
	version     INTEGER PRIMARY KEY,
	applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`, tracker))
	return err
}

// loadAppliedVersionsFrom returns the set of applied versions recorded in the
// given tracker table.
func loadAppliedVersionsFrom(ctx context.Context, db *sql.DB, tracker string) (map[int]bool, error) {
	if !safeIdent.MatchString(tracker) {
		return nil, fmt.Errorf("invalid tracker identifier %q", tracker)
	}
	rows, err := SafeQueryContext(db, ctx, fmt.Sprintf(`SELECT version FROM %s`, tracker))
	if err != nil {
		if isNoSuchTable(err) {
			if err := ensureTrackerTable(ctx, db, tracker); err != nil {
				return nil, err
			}
			return map[int]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// AppliedVersionFromTracker returns the highest version recorded in the given
// tracker table (0 if empty). Used for the namespace schema-version contract
// check, which reads orama_schema_migrations rather than schema_migrations.
func AppliedVersionFromTracker(ctx context.Context, db *sql.DB, tracker string) (int, error) {
	if !safeIdent.MatchString(tracker) {
		return 0, fmt.Errorf("invalid tracker identifier %q", tracker)
	}
	row := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(version), 0) FROM %s`, tracker))
	var v int
	if err := row.Scan(&v); err != nil {
		return 0, fmt.Errorf("query %s: %w", tracker, err)
	}
	return v, nil
}

// NamespaceMigrationsTracker exposes the isolated tracker name for callers that
// run the namespace schema-version contract check (e.g. the gateway).
func NamespaceMigrationsTracker() string { return namespaceMigrationsTracker }

// --- small schema-introspection helpers (rqlite + sqlite compatible) ---

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	row := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name)
	var n int
	if err := row.Scan(&n); err != nil {
		return false, fmt.Errorf("check table %q: %w", name, err)
	}
	return n > 0, nil
}

func tableColumns(ctx context.Context, db *sql.DB, name string) ([]string, error) {
	if !safeIdent.MatchString(name) {
		return nil, fmt.Errorf("invalid table identifier %q", name)
	}
	rows, err := SafeQueryContext(db, ctx, fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, name))
	if err != nil {
		return nil, fmt.Errorf("read columns of %q: %w", name, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func tableRowCount(ctx context.Context, db *sql.DB, name string) (int, error) {
	if !safeIdent.MatchString(name) {
		return 0, fmt.Errorf("invalid table identifier %q", name)
	}
	row := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, name))
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("count rows of %q: %w", name, err)
	}
	return n, nil
}

func containsFold(ss []string, target string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
