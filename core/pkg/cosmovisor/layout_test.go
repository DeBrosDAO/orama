package cosmovisor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releasetest"
)

var stageNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const oramadTarget = "oramad-linux-amd64"

// fixture is a chain home, a candidate oramad on disk, and TUF metadata
// under a generated root that names the good bytes.
type fixture struct {
	layout  Layout
	binary  string
	verify  func(string) error
	chowned []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	etc, meta := t.TempDir(), t.TempDir()
	good := []byte("oramad v2\n")
	repo := releasetest.NewRepo(t, stageNow)
	repo.WriteRoot(t, filepath.Join(etc, "release-root.json"))
	repo.Publish(t, meta, 1, time.Time{}, map[string][]byte{oramadTarget: good})

	f := &fixture{binary: filepath.Join(t.TempDir(), "oramad")}
	f.layout = Layout{Home: t.TempDir(), Daemon: "oramad", Chown: func(p string) error {
		f.chowned = append(f.chowned, p)
		return nil
	}}
	f.verify = func(path string) error {
		_, err := releaseverify.CheckFile(releaseverify.FileCheck{
			RootPath:    filepath.Join(etc, "release-root.json"),
			SeenPath:    filepath.Join(etc, "release-seen.json"),
			MetadataDir: meta,
			Target:      oramadTarget,
			File:        path,
			Now:         stageNow,
		})
		return err
	}
	if err := os.WriteFile(f.binary, good, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestStageUpgrade_placesAVerifiedBinaryWhereCosmovisorLooks(t *testing.T) {
	f := newFixture(t)
	dst, err := f.layout.StageUpgrade("v2", f.binary, f.verify)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.layout.Home, "cosmovisor", "upgrades", "v2", "bin", "oramad")
	if dst != want {
		t.Fatalf("staged at %s, want %s", dst, want)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != binaryPerm {
		t.Fatalf("staged binary %v, %v", info, err)
	}
	wantChown := []string{f.layout.Root(), filepath.Join(f.layout.Root(), "upgrades", "v2")}
	if len(f.chowned) != 2 || f.chowned[0] != wantChown[0] || f.chowned[1] != wantChown[1] {
		t.Fatalf("chowned %v, want %v", f.chowned, wantChown)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
		t.Fatal("staged over an existing upgrade binary")
	}
}

func TestStageUpgrade_tamperedBinaryIsNotStaged(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.binary, []byte("oramad backdoor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.layout.StageUpgrade("v2", f.binary, f.verify)
	if !errors.Is(err, releaseverify.ErrTargetHash) {
		t.Fatalf("tampered oramad: %v", err)
	}
	binDir := filepath.Join(f.layout.Root(), "upgrades", "v2", "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused binary left %v in %s", entries, binDir)
	}
}

func TestStageUpgrade_refusesANameCosmovisorWouldRewrite(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"", "V2", "../v2", "v2/x", "v 2", ".hidden"} {
		if _, err := f.layout.StageUpgrade(name, f.binary, f.verify); err == nil {
			t.Fatalf("upgrade name %q accepted", name)
		}
	}
	if len(f.chowned) != 0 {
		t.Fatalf("a refused name touched the layout: %v", f.chowned)
	}
}

func TestStageGenesis_pointsCurrentAtGenesisOnce(t *testing.T) {
	f := newFixture(t)
	if _, err := f.layout.StageGenesis(f.binary, f.verify); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(f.layout.Current())
	if err != nil || target != "genesis" {
		t.Fatalf("current -> %q, %v", target, err)
	}
	if b, err := os.ReadFile(filepath.Join(f.layout.Current(), "bin", "oramad")); err != nil || string(b) != "oramad v2\n" {
		t.Fatalf("current/bin/oramad %q, %v", b, err)
	}
	if _, err := f.layout.StageGenesis(f.binary, f.verify); err == nil {
		t.Fatal("staged over an existing genesis binary")
	}
}

func TestStageGenesis_leavesAnExistingCurrentAlone(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.layout.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("upgrades/v1", f.layout.Current()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageGenesis(f.binary, f.verify); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(f.layout.Current()); target != "upgrades/v1" {
		t.Fatalf("current was repointed to %q", target)
	}
}

func TestStage_needsAVerification(t *testing.T) {
	f := newFixture(t)
	if _, err := f.layout.StageUpgrade("v2", f.binary, nil); err == nil {
		t.Fatal("staged without a verification")
	}
}
