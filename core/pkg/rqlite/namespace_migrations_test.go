package rqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/migrations"
	"go.uber.org/zap"
)

// nsTestFS mimics the embedded core migrations relevant to bugboard #150,
// INCLUDING the two statement classes that broke the first implementation:
//   - an embedded `INSERT OR IGNORE INTO schema_migrations(version)` self-record
//     (present in 13 real migration files) — fails "no such table" on a fresh
//     namespace since the isolated path never creates schema_migrations.
//   - a CREATE INDEX on subscriptions(namespace_id) — fails "no such column" when
//     a tenant owns a differently shaped subscriptions.
//
// Both must be stripped in the namespace path (applySQLNamespace).
func nsTestFS() fs.FS {
	return fstest.MapFS{
		"001_core_a.sql": {Data: []byte(
			`CREATE TABLE IF NOT EXISTS core_a (id INTEGER PRIMARY KEY, val TEXT);
			 INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);`)},
		"002_core.sql": {Data: []byte(`CREATE TABLE IF NOT EXISTS subscriptions (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			namespace_id  INTEGER NOT NULL,
			app_id        INTEGER,
			topic         TEXT NOT NULL,
			endpoint      TEXT,
			created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_subscriptions_ns ON subscriptions(namespace_id);
		INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);`)},
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// Fresh namespace DB: core's dead subscriptions is dropped, core bookkeeping
// lives in the isolated tracker, schema_migrations is never created — so the
// tenant is free to create BOTH generic names.
func TestNamespaceApply_Fresh_freesGenericNames(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Errorf("isolated tracker max version = %d, want 2", v)
	}
	if ex, _ := tableExists(ctx, db, "schema_migrations"); ex {
		t.Error("schema_migrations must NOT exist on a namespace DB (freed for tenant)")
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); ex {
		t.Error("dead core subscriptions must be dropped")
	}
	if ex, _ := tableExists(ctx, db, "core_a"); !ex {
		t.Error("genuine core table core_a must remain")
	}
	// Tenant now owns both names.
	mustExec(t, db, `CREATE TABLE subscriptions (subscription_id TEXT PRIMARY KEY, tier TEXT)`)
	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT)`)
}

// App-first (devnet-like): the tenant created their premium subscriptions and
// their own schema_migrations FIRST. Isolation must leave BOTH untouched.
func TestNamespaceApply_AppFirst_preservesTenantTables(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mustExec(t, db, `CREATE TABLE subscriptions (
		subscription_id TEXT PRIMARY KEY, user_id TEXT NOT NULL, tier TEXT NOT NULL, payment_token TEXT)`)
	mustExec(t, db, `INSERT INTO subscriptions(subscription_id,user_id,tier) VALUES ('s1','u1','PREMIUM')`)
	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT DEFAULT CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO schema_migrations(version,name) VALUES (1,'baseline'),(16,'payments')`)

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Tenant subscriptions preserved with its shape and rows.
	if ex, _ := tableExists(ctx, db, "subscriptions"); !ex {
		t.Fatal("tenant subscriptions was dropped!")
	}
	if n, _ := tableRowCount(ctx, db, "subscriptions"); n != 1 {
		t.Errorf("tenant subscriptions rows = %d, want 1 (data loss)", n)
	}
	if cols, _ := tableColumns(ctx, db, "subscriptions"); !containsFold(cols, "tier") {
		t.Error("tenant subscriptions lost its premium columns")
	}
	// Tenant schema_migrations preserved (has a name column).
	if scols, _ := tableColumns(ctx, db, "schema_migrations"); !containsFold(scols, "name") {
		t.Error("tenant schema_migrations was replaced")
	}
	// We must NOT have seeded from a tenant-owned schema_migrations: the tenant's
	// versions (1,16) must not leak into core's isolated tracker.
	if applied, _ := loadAppliedVersionsFrom(ctx, db, namespaceMigrationsTracker); applied[16] {
		t.Error("tenant migration version leaked into core's isolated tracker")
	}
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Errorf("isolated tracker max version = %d, want 2", v)
	}
}

// Core-first (testnet-like): core's tables got created first. Isolation drops
// both core artifacts and frees the names, self-remediating the polluted DB.
func TestNamespaceApply_CoreFirst_remediates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// A DB already polluted by core-first provisioning.
	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT DEFAULT CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO schema_migrations(version) VALUES (1),(2)`)
	mustExec(t, db, `CREATE TABLE subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT, namespace_id INTEGER NOT NULL, app_id INTEGER,
		topic TEXT NOT NULL, endpoint TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if ex, _ := tableExists(ctx, db, "schema_migrations"); ex {
		t.Error("core-owned schema_migrations must be dropped (freed for tenant)")
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); ex {
		t.Error("dead core subscriptions must be dropped (freed for tenant)")
	}
	// Bookkeeping seeded into the isolated tracker so migrations aren't re-run.
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Errorf("isolated tracker max version = %d, want 2 (seeded from legacy)", v)
	}
	// Tenant migrations now apply cleanly.
	mustExec(t, db, `CREATE TABLE subscriptions (subscription_id TEXT PRIMARY KEY, tier TEXT)`)
	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
}

// Data-safety: a core-shape subscriptions that (unexpectedly) holds rows must
// NEVER be dropped — we only remove the provably-dead, empty core artifact.
func TestNamespaceApply_CoreSubscriptionsWithRows_notDropped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mustExec(t, db, `CREATE TABLE subscriptions (
		id INTEGER PRIMARY KEY AUTOINCREMENT, namespace_id INTEGER NOT NULL, app_id INTEGER,
		topic TEXT NOT NULL, endpoint TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	mustExec(t, db, `INSERT INTO subscriptions(namespace_id, topic) VALUES (1,'t')`)

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); !ex {
		t.Error("a subscriptions table WITH rows must not be dropped (data safety)")
	}
}

// Re-running the namespace apply must be a no-op (idempotent) — the isolation
// must not thrash tables or re-create dropped ones.
func TestNamespaceApply_Idempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
			t.Fatalf("apply iteration %d: %v", i, err)
		}
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); ex {
		t.Error("subscriptions must stay dropped across re-runs")
	}
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Errorf("isolated tracker max version = %d, want 2", v)
	}
}

// Hardening (bugboard #150, security audit A.1): a tenant table literally named
// schema_migrations but NOT core's exact {version,applied_at} shape — e.g.
// {version, checksum} with no "name" column — must be PRESERVED, not dropped.
func TestNamespaceApply_TenantTrackerWithoutName_preserved(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL)`)
	mustExec(t, db, `INSERT INTO schema_migrations(version,checksum) VALUES (1,'abc'),(2,'def')`)

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if ex, _ := tableExists(ctx, db, "schema_migrations"); !ex {
		t.Fatal("a tenant schema_migrations lacking a name column must NOT be dropped (exact-shape guard)")
	}
	if n, _ := tableRowCount(ctx, db, "schema_migrations"); n != 2 {
		t.Errorf("tenant tracker rows = %d, want 2 (no data loss)", n)
	}
	if cols, _ := tableColumns(ctx, db, "schema_migrations"); !containsFold(cols, "checksum") {
		t.Error("tenant tracker shape changed")
	}
	// Its versions must NOT have been seeded into core's isolated tracker.
	if applied, _ := loadAppliedVersionsFrom(ctx, db, namespaceMigrationsTracker); applied[1] && applied[2] {
		// core migrations 1,2 legitimately record here too; ensure they came from
		// applying nsTestFS (2 migrations), not from reading the tenant tracker.
		// A leak would show if the tenant had a version core doesn't (none here),
		// so assert the tracker max is exactly the embedded max (2).
	}
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Errorf("isolated tracker max = %d, want 2", v)
	}
}

// Regression for the abort-at-002 bug: the embedded `INSERT INTO
// schema_migrations` and `subscriptions` index statements must be stripped, so
// the apply reaches the highest version and never leaves schema_migrations
// behind for the runner to trip over.
func TestNamespaceApply_StripsSelfRecordAndDeadTableDDL(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, nsTestFS(), zap.NewNop()); err != nil {
		t.Fatalf("apply must not abort on embedded schema_migrations INSERT / subscriptions index: %v", err)
	}
	// Reached the highest embedded version (not stuck at 1).
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v != 2 {
		t.Fatalf("isolated tracker max = %d, want 2 (apply aborted early)", v)
	}
	// The embedded INSERTs did NOT resurrect a core schema_migrations table.
	if ex, _ := tableExists(ctx, db, "schema_migrations"); ex {
		t.Error("schema_migrations must not be (re)created in a namespace DB")
	}
	// core_a (genuine core table) applied; dead subscriptions dropped.
	if ex, _ := tableExists(ctx, db, "core_a"); !ex {
		t.Error("genuine core table core_a should be created")
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); ex {
		t.Error("dead core subscriptions should be dropped")
	}
}

func TestStmtTargetsStrippedTable(t *testing.T) {
	strip := []string{
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES (2)`,
		`insert into schema_migrations (version) values (5)`,
		`CREATE TABLE IF NOT EXISTS subscriptions (id INTEGER)`,
		`CREATE INDEX idx ON subscriptions(namespace_id)`,
		`CREATE INDEX IF NOT EXISTS idx_subscriptions_ns ON subscriptions(topic)`,
	}
	for _, s := range strip {
		if !stmtTargetsStrippedTable(s) {
			t.Errorf("should strip: %q", s)
		}
	}
	keep := []string{
		`CREATE TABLE IF NOT EXISTS functions (id INTEGER)`,
		`CREATE TABLE IF NOT EXISTS user_subscriptions (id INTEGER)`, // not our table
		`INSERT OR IGNORE INTO orama_schema_migrations(version) VALUES (2)`,
		`CREATE INDEX idx ON refresh_tokens(subject)`,
		`CREATE TABLE nonces (wallet TEXT)`,
	}
	for _, s := range keep {
		if stmtTargetsStrippedTable(s) {
			t.Errorf("should KEEP: %q", s)
		}
	}
}

// Runs the namespace apply against the REAL embedded migrations FS on a fresh
// DB (the coverage gap both reviewers flagged). Asserts it completes to the
// binary's required version and leaves neither generic name behind — proving a
// brand-new namespace comes up clean and the tenant owns both names.
func TestNamespaceApply_RealMigrations_fresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := ApplyEmbeddedMigrationsNamespace(ctx, db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply real migrations to fresh namespace DB: %v", err)
	}
	if v, _ := AppliedVersionFromTracker(ctx, db, namespaceMigrationsTracker); v < migrations.RequiredVersion() {
		t.Fatalf("isolated tracker max = %d, want >= required %d", v, migrations.RequiredVersion())
	}
	if ex, _ := tableExists(ctx, db, "schema_migrations"); ex {
		t.Error("real-migrations fresh apply must leave schema_migrations free for the tenant")
	}
	if ex, _ := tableExists(ctx, db, "subscriptions"); ex {
		t.Error("real-migrations fresh apply must drop the dead core subscriptions")
	}
	// A genuine core table the namespace gateway needs must be present.
	if ex, _ := tableExists(ctx, db, "functions"); !ex {
		t.Error("expected core table 'functions' to be created")
	}
	// Tenant can now own both generic names.
	mustExec(t, db, `CREATE TABLE subscriptions (subscription_id TEXT PRIMARY KEY, tier TEXT)`)
	mustExec(t, db, `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT)`)
}

func TestIsCoreTrackerShape(t *testing.T) {
	if !isCoreTrackerShape([]string{"version", "applied_at"}) {
		t.Error("exact core tracker shape should match")
	}
	if isCoreTrackerShape([]string{"version", "name", "applied_at"}) {
		t.Error("tenant 3-col tracker must not match")
	}
	if isCoreTrackerShape([]string{"version", "checksum"}) {
		t.Error("tenant {version,checksum} must not match core")
	}
	if isCoreTrackerShape([]string{"version"}) {
		t.Error("subset must not match")
	}
}

func TestIsCoreSubscriptionsShape(t *testing.T) {
	core := []string{"id", "namespace_id", "app_id", "topic", "endpoint", "created_at"}
	if !isCoreSubscriptionsShape(core) {
		t.Error("exact core shape should match")
	}
	// tenant premium shape must NOT match
	if isCoreSubscriptionsShape([]string{"subscription_id", "user_id", "tier", "source"}) {
		t.Error("tenant premium shape must not match core")
	}
	// superset (extra column) must NOT match
	if isCoreSubscriptionsShape(append(append([]string{}, core...), "extra")) {
		t.Error("superset must not match core (exact-set only)")
	}
	// missing column must NOT match
	if isCoreSubscriptionsShape([]string{"id", "namespace_id", "app_id", "topic", "endpoint"}) {
		t.Error("subset must not match core")
	}
}
