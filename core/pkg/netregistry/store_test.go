package netregistry

import (
	"os"
	"path/filepath"
	"testing"
)

func testNetwork(t *testing.T) *Network {
	t.Helper()
	m := validManifest()
	return &Network{Manifest: &m, Root: testRoot, Source: "https://example.org/nets/teststage/manifest.json"}
}

func TestStore_saveLoadRemove(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "networks")}
	if err := s.Save(testNetwork(t)); err != nil {
		t.Fatal(err)
	}
	r, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Get("teststage")
	if err != nil || n.Source != "https://example.org/nets/teststage/manifest.json" || n.Builtin {
		t.Fatalf("loaded %+v, %v", n, err)
	}
	removed, err := s.Remove("teststage")
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if removed, err := s.Remove("teststage"); err != nil || removed {
		t.Errorf("a second Remove = %v, %v; want false, nil", removed, err)
	}
}

func TestStore_loadOfMissingDirIsEmpty(t *testing.T) {
	r, err := Store{Dir: filepath.Join(t.TempDir(), "absent")}.Load()
	if err != nil || len(r.Names()) != 0 {
		t.Errorf("Load = %v, %v; want an empty registry", r, err)
	}
}

func TestStore_saveReplacesAndLeavesNoTempDirs(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	n := testNetwork(t)
	if err := s.Save(n); err != nil {
		t.Fatal(err)
	}
	n.Manifest.Faucet = false
	if err := s.Save(n); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Load()
	got, _ := r.Get("teststage")
	if got.Manifest.Faucet {
		t.Error("Save did not replace the stored network")
	}
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) != 1 {
		t.Errorf("store holds %d entries, want 1", len(entries))
	}
}

func TestStore_loadRefusesACorruptNetwork(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Save(testNetwork(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "teststage", ReleaseRootFile), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("Load accepted a release root that is not the pinned one")
	}
}

func TestStore_saveRefusesAnUnsafeName(t *testing.T) {
	n := testNetwork(t)
	n.Manifest.Name = "../escape"
	if err := (Store{Dir: t.TempDir()}).Save(n); err == nil {
		t.Fatal("Save accepted a name that leaves the store")
	}
	if removed, err := (Store{Dir: t.TempDir()}).Remove("../.."); err != nil || removed {
		t.Errorf("Remove(../..) = %v, %v", removed, err)
	}
}

func TestStore_aTorNetworkSurvivesTheStoreAndIsVerifiedOnLoad(t *testing.T) {
	file := testTorNetwork(t)
	n := testNetwork(t)
	m := withTorNetwork(file)
	n.Manifest, n.TorNetwork = &m, file
	s := Store{Dir: t.TempDir()}
	if err := s.Save(n); err != nil {
		t.Fatal(err)
	}
	r, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("teststage")
	if string(got.TorNetwork) != string(file) {
		t.Errorf("stored Tor network = %q", got.TorNetwork)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "teststage", TorNetworkFile), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Error("a stored Tor network file that no longer matches its pin was loaded")
	}
}
