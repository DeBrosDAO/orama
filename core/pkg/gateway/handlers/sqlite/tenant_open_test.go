package sqlite

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectCrossDBSQL(t *testing.T) {
	denied := []string{
		"ATTACH DATABASE '/tmp/other.db' AS other",
		"attach database 'x' as y",
		"DETACH other",
		"SELECT 1; SELECT 2",
		"SELECT 1; ATTACH DATABASE 'x' AS y",
		"/**/ATTACH/**/'x'/**/AS y",
		"VACUUM INTO '/opt/orama/.orama/data/sqlite/other/planted.db'",
		"vacuum main into 'x'",
		"VACUUM/**/INTO 'x'",
		"",
		"   ",
		";",
	}
	for _, q := range denied {
		if err := rejectCrossDBSQL(q); err == nil {
			t.Errorf("rejectCrossDBSQL(%q) = nil, want error", q)
		}
	}
	allowed := []string{
		"SELECT * FROM users",
		"INSERT INTO t (a) VALUES (1)",
		"UPDATE t SET a = 1 WHERE id = 2",
		"SELECT 'attach' AS label",
		"SELECT * FROM t;",
		"INSERT INTO w VALUES ('please ATTACH this; now')",
		"INSERT INTO w VALUES ('VACUUM INTO x')",
		"SELECT 1 -- ATTACH x",
		"SELECT \"attach\" FROM t",
		"SELECT vacuum, into_col FROM t",
		"VACUUM",
	}
	for _, q := range allowed {
		if err := rejectCrossDBSQL(q); err != nil {
			t.Errorf("rejectCrossDBSQL(%q) = %v, want nil", q, err)
		}
	}
}

func TestOpenTenantDB_attachDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tenant.db")
	db, err := openTenantDB(path)
	if err != nil {
		t.Fatalf("openTenantDB: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	other := filepath.Join(dir, "other.db")
	if _, err := db.Exec("ATTACH DATABASE ? AS other", other); err == nil {
		t.Fatal("ATTACH must fail when SQLITE_LIMIT_ATTACHED=0")
	}
}

// Extension loading stays off on a tenant connection. The guard does not refuse
// load_extension by name: the engine refusing it is the control, and it holds
// only while the driver is built and registered without extension loading.
func TestOpenTenantDB_loadExtensionDenied(t *testing.T) {
	db, err := openTenantDB(filepath.Join(t.TempDir(), "tenant.db"))
	if err != nil {
		t.Fatalf("openTenantDB: %v", err)
	}
	defer db.Close()
	var out any
	if err := db.QueryRow("SELECT load_extension('/nonexistent/ext')").Scan(&out); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("load_extension = %v, want the engine's \"not authorized\"", err)
	}
}
