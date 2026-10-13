//go:build unix

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
// under a generated root that names the good bytes. Running unprivileged,
// the test's own uid stands in for root; foreign names a component to
// treat as someone else's.
type fixture struct {
	layout  Layout
	binary  string
	verify  Verify
	foreign string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	etc, meta := t.TempDir(), t.TempDir()
	good := []byte("oramad v2\n")
	repo := releasetest.NewRepo(t, stageNow)
	repo.WriteRoot(t, filepath.Join(etc, "release-root.json"))
	repo.Publish(t, meta, 1, time.Time{}, map[string][]byte{oramadTarget: good})

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{binary: filepath.Join(t.TempDir(), "oramad")}
	self := uint32(os.Getuid())
	f.layout = Layout{
		Home: home, Daemon: "oramad", ChainUID: os.Getuid(), ChainGID: os.Getgid(),
		trusted: func(uid uint32, name string) bool { return (uid == 0 || uid == self) && name != f.foreign },
	}
	f.verify = func(file *os.File) error {
		_, err := releaseverify.CheckFile(releaseverify.FileCheck{
			RootPath:    filepath.Join(etc, "release-root.json"),
			SeenPath:    filepath.Join(etc, "release-seen.json"),
			MetadataDir: meta,
			Target:      oramadTarget,
			File:        file,
			Now:         stageNow,
		})
		return err
	}
	if err := os.WriteFile(f.binary, good, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) mkdir(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(f.layout.Root(), rel)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertNoBinary fails if any oramad exists below the layout.
func (f *fixture) assertNoBinary(t *testing.T) {
	t.Helper()
	filepath.Walk(f.layout.Home, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "oramad" && info.Mode().IsRegular() {
			t.Fatalf("a refused stage left %s", p)
		}
		return nil
	})
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
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm() != binaryPerm {
		t.Fatalf("staged binary %v, %v", info, err)
	}
	root, err := os.Stat(f.layout.Root())
	if err != nil || root.Mode()&os.ModeSticky == 0 || root.Mode().Perm() != 0o775 {
		t.Fatalf("cosmovisor/ mode %v, %v; want sticky 0775", root.Mode(), err)
	}
	link, err := os.Readlink(filepath.Join(f.layout.Root(), "upgrades", "v2", "upgrade-info.json"))
	if err != nil || link != "../../../cosmovisor-upgrade-info-v2.json" {
		t.Fatalf("upgrade-info.json -> %q, %v", link, err)
	}
	staging, _ := filepath.Glob(filepath.Join(f.layout.Root(), stagingPrefix+"*"))
	if len(staging) != 0 {
		t.Fatalf("staging left behind: %v", staging)
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
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); !errors.Is(err, releaseverify.ErrTargetHash) {
		t.Fatalf("tampered oramad: %v", err)
	}
	f.assertNoBinary(t)
}

func TestStageUpgrade_refusedStageLeavesNoUpgradeDirectory(t *testing.T) {
	f := newFixture(t)
	refuse := func(*os.File) error { return errors.New("not in the release") }
	upgrades := filepath.Join(f.layout.Root(), "upgrades")
	if _, err := f.layout.StageUpgrade("v2", f.binary, refuse); err == nil {
		t.Fatal("staged a binary that did not verify")
	}
	if _, err := os.Stat(filepath.Join(upgrades, "v2")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused stage left upgrades/v2: %v", err)
	}
	if _, err := os.Stat(upgrades); err != nil {
		t.Fatalf("a refused stage removed upgrades/ itself: %v", err)
	}
}

func TestStageUpgrade_refusedStageKeepsDirectoriesItDidNotCreate(t *testing.T) {
	f := newFixture(t)
	bin := f.mkdir(t, "upgrades/v2/bin")
	refuse := func(*os.File) error { return errors.New("not in the release") }
	if _, err := f.layout.StageUpgrade("v2", f.binary, refuse); err == nil {
		t.Fatal("staged a binary that did not verify")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("a refused stage removed a directory that was already there: %v", err)
	}
}

func TestStageUpgrade_symlinkedUpgradesDirIsRefused(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	f.mkdir(t, "")
	if err := os.Symlink(elsewhere, filepath.Join(f.layout.Root(), "upgrades")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
		t.Fatal("staged through a symlinked upgrades/")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("root wrote through the symlink: %v", entries)
	}
}

func TestStageUpgrade_symlinkedBinDirIsRefused(t *testing.T) {
	f := newFixture(t)
	elsewhere := t.TempDir()
	f.mkdir(t, "upgrades/v2")
	if err := os.Symlink(elsewhere, filepath.Join(f.layout.Root(), "upgrades", "v2", "bin")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
		t.Fatal("staged through a symlinked bin/")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("root wrote through the symlink: %v", entries)
	}
}

func TestStageUpgrade_nonRootOwnedComponentIsRefused(t *testing.T) {
	for _, name := range []string{"upgrades", "v2", "bin", "cosmovisor"} {
		f := newFixture(t)
		f.mkdir(t, "upgrades/v2/bin")
		f.foreign = name
		if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
			t.Fatalf("staged below a %s/ that root does not own", name)
		}
		f.assertNoBinary(t)
	}
}

func TestStageUpgrade_groupWritableComponentIsRefused(t *testing.T) {
	f := newFixture(t)
	p := f.mkdir(t, "upgrades")
	if err := os.Chmod(p, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
		t.Fatal("staged below a group-writable upgrades/")
	}
}

func TestStageUpgrade_refusesANameCosmovisorWouldRewrite(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"", "V2", "../v2", "v2/x", "v 2", ".hidden"} {
		if _, err := f.layout.StageUpgrade(name, f.binary, f.verify); err == nil {
			t.Fatalf("upgrade name %q accepted", name)
		}
	}
	if _, err := os.Stat(f.layout.Root()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused name touched the layout: %v", err)
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
	if info, err := os.Stat(filepath.Join(f.layout.Root(), "upgrades")); err != nil || !info.IsDir() {
		t.Fatalf("upgrades/ was not created for the read-only mount: %v", err)
	}
	if _, err := f.layout.StageGenesis(f.binary, f.verify); err == nil {
		t.Fatal("staged over an existing genesis binary")
	}
}

func TestStageGenesis_leavesAnExistingCurrentAlone(t *testing.T) {
	f := newFixture(t)
	f.mkdir(t, "")
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

func TestStage_needsAVerificationAndAnUnprivilegedAccount(t *testing.T) {
	f := newFixture(t)
	if _, err := f.layout.StageUpgrade("v2", f.binary, nil); err == nil {
		t.Fatal("staged without a verification")
	}
	f.layout.ChainUID = 0
	if _, err := f.layout.StageUpgrade("v2", f.binary, f.verify); err == nil {
		t.Fatal("staged for a chain account that is root")
	}
}
