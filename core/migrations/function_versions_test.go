package migrations_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// functionVersionsMigration is the file that re-keys `functions` by version.
const functionVersionsMigration = "068_function_versions.sql"

// Migration 068 rebuilds `functions` to change its UNIQUE key. Rows written
// under the old key must come through it, ids unchanged (the trigger, env and
// log tables reference them), and the table must then accept a second version
// of the same function while still refusing a duplicate version.
func TestFunctionVersionsMigration_keepsExistingRowsAndAllowsVersions(t *testing.T) {
	before := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, e := range entries {
		if e.Name() == functionVersionsMigration || !strings.HasSuffix(e.Name(), ".sql") || e.Name() > functionVersionsMigration {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		before[e.Name()] = &fstest.MapFile{Data: data}
	}

	db := openRoundtripDB(t)
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, before, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations before 068: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO functions (id, name, namespace, version, wasm_cid, created_by, is_internal, ws_auth, ws_persistent)
		VALUES ('f-old', 'fn', 'ns', 3, 'cid-old', 'ns', 1, 'capability', 1)`); err != nil {
		t.Fatalf("seed old-shape row: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO function_env_vars (id, function_id, key, value) VALUES ('e1', 'f-old', 'K', 'V')`); err != nil {
		t.Fatalf("seed env var: %v", err)
	}

	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply 068: %v", err)
	}

	var cid, wsAuth string
	var internal, persistent bool
	if err := db.QueryRow(`SELECT wasm_cid, ws_auth, is_internal, ws_persistent FROM functions WHERE id = 'f-old' AND version = 3`).
		Scan(&cid, &wsAuth, &internal, &persistent); err != nil {
		t.Fatalf("the existing row did not survive the rebuild: %v", err)
	}
	if cid != "cid-old" || wsAuth != "capability" || !internal || !persistent {
		t.Errorf("row changed in the rebuild: cid=%q ws_auth=%q internal=%v persistent=%v", cid, wsAuth, internal, persistent)
	}
	var env string
	if err := db.QueryRow(`SELECT value FROM function_env_vars WHERE function_id = 'f-old' AND key = 'K'`).Scan(&env); err != nil || env != "V" {
		t.Errorf("env var of the existing row lost: %q, %v", env, err)
	}

	if _, err := db.Exec(`INSERT INTO functions (id, name, namespace, version, wasm_cid, created_by) VALUES ('f-new', 'fn', 'ns', 4, 'cid-new', 'ns')`); err != nil {
		t.Errorf("a second version of the same function was refused: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO functions (id, name, namespace, version, wasm_cid, created_by) VALUES ('f-dup', 'fn', 'ns', 4, 'cid-dup', 'ns')`); err == nil {
		t.Error("a duplicate (namespace, name, version) was accepted")
	}
	for _, idx := range []string{"idx_functions_namespace", "idx_functions_name", "idx_functions_status"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s missing after the rebuild", idx)
		}
	}
}
