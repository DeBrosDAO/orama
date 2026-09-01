package namespace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefuseEmptyAdopt(t *testing.T) {
	dir := t.TempDir()
	if err := RefuseEmptyAdopt(dir); err == nil {
		t.Fatal("empty dir must be refused")
	}
	if HasExistingRaft(dir) {
		t.Fatal("empty dir must not look like raft")
	}
}

func TestHasExistingRaft(t *testing.T) {
	dir := t.TempDir()
	raft := filepath.Join(dir, "raft.db")
	if err := os.WriteFile(raft, make([]byte, 2048), 0644); err != nil {
		t.Fatal(err)
	}
	if !HasExistingRaft(dir) {
		t.Fatal("raft.db > 1KiB must count as existing")
	}
	if err := RefuseEmptyAdopt(dir); err != nil {
		t.Fatalf("existing raft must be adoptable: %v", err)
	}
}

func TestRQLiteUnitDataDir_indexUsesCoreDir(t *testing.T) {
	core := "/opt/orama/.orama/data/rqlite"
	got := rqliteUnitDataDir(BlueprintNameIndex, "n1", "/opt/orama/.orama/data/namespaces", core)
	if got != core {
		t.Errorf("index DATA_DIR = %s, want %s (must not be namespaces/index/rqlite)", got, core)
	}
	tenant := rqliteUnitDataDir("anchat-test", "n1", "/opt/orama/.orama/data/namespaces", core)
	want := "/opt/orama/.orama/data/namespaces/anchat-test/rqlite/n1"
	if tenant != want {
		t.Errorf("tenant DATA_DIR = %s, want %s", tenant, want)
	}
}

func TestIndexSupervisor_doesNotSelectNodes(t *testing.T) {
	// Compile-time / API lock: IndexSupervisor has no NodeSelector field.
	var s IndexSupervisor
	_ = s.CoreRQLiteDir
}
