package releaserepo

import (
	"path/filepath"
	"testing"
	"time"
)

func TestKeys_saveAndLoadRoundTripAndNeverOverwrite(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "keys")
	if err := keys.Save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 4 {
		t.Fatalf("loaded %d keys, want the four top-level roles", len(loaded))
	}
	for role, key := range keys {
		if !key.Equal(loaded[role]) {
			t.Errorf("the %s key changed in a round trip", role)
		}
	}
	if err := keys.Save(dir); err == nil {
		t.Fatal("saving over existing keys succeeded")
	}
}

func TestLoadKeys_refusals(t *testing.T) {
	if _, err := LoadKeys(t.TempDir()); err == nil {
		t.Error("an empty key directory loaded")
	}
}

func TestBuild_needsARootExpiryAndAKeyForEveryRole(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(keys, Spec{Version: 1}); err == nil {
		t.Error("a spec with no root expiry was built")
	}
	delete(keys, "snapshot")
	if _, err := Build(keys, Spec{Version: 1, RootValidUntil: time.Now().Add(time.Hour)}); err == nil {
		t.Error("a repository with no snapshot key was built")
	}
}

func TestBuild_listsTheTargetsFileInTheSnapshot(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	files, err := Build(keys, Spec{
		Version: 3, RootValidUntil: time.Now().Add(time.Hour),
		Targets: map[string][]byte{"nightly/x": []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{TimestampFile, SnapshotFile, TargetsFile} {
		if len(files[name]) == 0 {
			t.Errorf("%s was not built", name)
		}
	}
	if len(files) != 3 {
		t.Errorf("built %d files, want the three top-level metadata files", len(files))
	}
}
