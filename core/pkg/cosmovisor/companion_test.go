//go:build unix

package cosmovisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const verifierBody = "orchard verifier\n"

// verifierFile writes a companion's source and returns it with a verification
// that accepts exactly those bytes.
func (f *fixture) verifierFile(t *testing.T) Companion {
	t.Helper()
	src := filepath.Join(t.TempDir(), "orama-orchard-verifier")
	if err := os.WriteFile(src, []byte(verifierBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return Companion{Name: "orama-orchard-verifier", Src: src, Verify: func(file *os.File) error {
		buf := make([]byte, len(verifierBody)+8)
		n, _ := file.ReadAt(buf, 0)
		if string(buf[:n]) != verifierBody {
			return errors.New("not the release's verifier")
		}
		return nil
	}}
}

func TestStageUpgrade_placesACompanionBesideTheBinary(t *testing.T) {
	f := newFixture(t)
	c := f.verifierFile(t)
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify, c); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(f.layout.Root(), "upgrades", "v2", "bin", c.Name)
	info, err := os.Stat(staged)
	if err != nil || info.Mode().Perm() != binaryPerm {
		t.Fatalf("companion %v, %v", info, err)
	}
	if b, _ := os.ReadFile(staged); string(b) != verifierBody {
		t.Fatalf("companion bytes %q", b)
	}
	if _, err := os.Stat(filepath.Join(f.layout.Root(), "upgrades", "v2", "bin", "oramad")); err != nil {
		t.Fatalf("the binary is missing: %v", err)
	}
}

func TestStageGenesis_placesACompanionAndCurrentReachesIt(t *testing.T) {
	f := newFixture(t)
	c := f.verifierFile(t)
	if _, err := f.layout.StageGenesis(f.binary, f.verify, c); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(f.layout.CurrentBinDir(), c.Name)); err != nil || string(b) != verifierBody {
		t.Fatalf("current/bin/%s: %q, %v", c.Name, b, err)
	}
}

func TestStage_aCompanionThatDoesNotVerifyLeavesNothing(t *testing.T) {
	f := newFixture(t)
	c := f.verifierFile(t)
	if err := os.WriteFile(c.Src, []byte("a different verifier"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify, c); err == nil {
		t.Fatal("staged a companion that did not verify")
	}
	f.assertNoBinary(t)
	if _, err := os.Stat(filepath.Join(f.layout.Root(), "upgrades", "v2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused stage left upgrades/v2: %v", err)
	}
}

// The daemon goes last: when it is the file that fails, the companions already
// placed are removed, so the version is not left half staged.
func TestStage_aBinaryThatDoesNotVerifyTakesItsCompanionsDown(t *testing.T) {
	f := newFixture(t)
	c := f.verifierFile(t)
	if err := os.WriteFile(f.binary, []byte("oramad backdoor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify, c); err == nil {
		t.Fatal("staged a binary that did not verify")
	}
	if _, err := os.Stat(filepath.Join(f.layout.Root(), "upgrades", "v2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused stage left upgrades/v2: %v", err)
	}
	if _, err := f.layout.StageGenesis(f.binary, f.verify, c); err == nil {
		t.Fatal("staged a genesis binary that did not verify")
	}
	entries, _ := os.ReadDir(f.layout.GenesisBinDir())
	if len(entries) != 0 {
		t.Fatalf("genesis/bin holds %v after a refused stage", entries)
	}
}

func TestStage_aCompanionNameCannotNameAnotherFile(t *testing.T) {
	f := newFixture(t)
	c := f.verifierFile(t)
	for _, name := range []string{"", ".", "..", "../x", "a/b", "oramad"} {
		c.Name = name
		if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify, c); err == nil {
			t.Errorf("companion name %q accepted", name)
		}
	}
	f.assertNoBinary(t)
}

func TestStageGenesisCompanions_addsAFileBesideAnInstalledBinaryOnce(t *testing.T) {
	f := newFixture(t)
	if _, err := f.layout.StageGenesis(f.binary, f.verify); err != nil {
		t.Fatal(err)
	}
	c := f.verifierFile(t)
	if err := f.layout.StageGenesisCompanions(c); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(f.layout.GenesisBinDir(), c.Name)); err != nil || string(b) != verifierBody {
		t.Fatalf("%q, %v", b, err)
	}
	if err := f.layout.StageGenesisCompanions(c); err == nil || !strings.Contains(err.Error(), "already staged") {
		t.Fatalf("a second stage over the first: %v", err)
	}
}

func TestStageGenesisCompanions_needsAStagedGenesis(t *testing.T) {
	f := newFixture(t)
	if err := f.layout.StageGenesisCompanions(f.verifierFile(t)); err == nil {
		t.Fatal("a companion was staged where no genesis binary is")
	}
}
