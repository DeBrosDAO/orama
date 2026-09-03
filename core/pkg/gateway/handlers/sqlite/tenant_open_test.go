package sqlite

import (
	"path/filepath"
	"testing"
)

func TestRejectCrossDBSQL(t *testing.T) {
	denied := []string{
		"ATTACH DATABASE '/tmp/other.db' AS other",
		"attach database 'x' as y",
		"DETACH other",
		"SELECT 1; SELECT 2",
		"SELECT 1; ATTACH DATABASE 'x' AS y",
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
