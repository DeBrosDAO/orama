package namespace

import (
	"path/filepath"
	"testing"
)

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

func TestIndexHostDataPaths_adoptInPlace(t *testing.T) {
	s := NewIndexSupervisor("/opt/orama/.orama", nil)
	if s.CoreRQLiteDir() != "/opt/orama/.orama/data/rqlite" {
		t.Errorf("CoreRQLiteDir = %s", s.CoreRQLiteDir())
	}
	ipfsRepo := filepath.Join(s.dataDir, "ipfs", "repo")
	if ipfsRepo != "/opt/orama/.orama/data/ipfs/repo" {
		t.Errorf("ipfs repo = %s", ipfsRepo)
	}
	vault := filepath.Join(s.dataDir, "vault", "vault.yaml")
	if vault != "/opt/orama/.orama/data/vault/vault.yaml" {
		t.Errorf("vault yaml = %s", vault)
	}
}
