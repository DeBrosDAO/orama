package push

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A swap killed after the old release was moved aside and before the new
// manifest was moved in: the next stage must put the old release back before it
// deletes the staging directory that holds it.
func TestStage_aSwapKilledHalfWayIsRecoveredBeforeLeftoversAreRemoved(t *testing.T) {
	base := installedNode(t)
	staging := filepath.Join(base, stagingPrefix+"dead")
	// The old release is aside; a part of the new one is in place; no manifest.
	writeTree(t, staging, map[string]string{
		"old/" + archivetrust.ManifestName: "old manifest",
		"old/manifest.sig":                 "old sig",
		"old/bin/orama":                    "old cli",
		"new/systemd/unit.service":         "the new release's copy",
		stagedSwapping:                     "",
	})
	for _, gone := range []string{"manifest.json", "manifest.sig", "bin"} {
		if err := os.RemoveAll(filepath.Join(base, gone)); err != nil {
			t.Fatal(err)
		}
	}
	writeTree(t, base, map[string]string{"bin/orama": "half of the new cli", "bin/extra": "new"})

	if err := removeLeftoverStaging(base); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(base, "bin", "orama")); got != "old cli" {
		t.Fatalf("bin/orama = %q, want the old release back", got)
	}
	if _, err := os.Stat(filepath.Join(base, "bin", "extra")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file of the half-installed release survived: %v", err)
	}
	if got := read(t, filepath.Join(base, "manifest.json")); got != "old manifest" {
		t.Fatalf("manifest = %q", got)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the staging directory was not removed: %v", err)
	}
	if got := read(t, filepath.Join(base, ".orama", "data", "node.key")); got != "node identity" {
		t.Fatalf("the node's data was touched: %q", got)
	}
}

func TestStage_aCompleteSwapIsNotUndoneByLeftoverRemoval(t *testing.T) {
	base := installedNode(t)
	writeTree(t, base, map[string]string{"manifest.json": "new manifest", "bin/orama": "new cli"})
	writeTree(t, filepath.Join(base, stagingPrefix+"done"), map[string]string{"old/manifest.json": "old manifest", "old/bin/orama": "old cli"})
	if err := removeLeftoverStaging(base); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(base, "bin", "orama")); got != "new cli" {
		t.Fatalf("a finished swap was undone: %q", got)
	}
}

func TestStage_aMachineWithNoReleaseAndNoLeftoversIsLeftAlone(t *testing.T) {
	base := t.TempDir()
	if err := recoverInterruptedSwap(base); err != nil {
		t.Fatal(err)
	}
}

// When the replaced release cannot be kept, the stage puts it back instead of
// reporting a stage that left the node on the new release with no way back.
func TestStageKeepPrevious_putsTheOldReleaseBackWhenItCannotBeKept(t *testing.T) {
	n := newReleaseOnlyNode(t)
	prev := keepRename
	t.Cleanup(func() { keepRename = prev })
	keepRename = func(oldpath, newpath string) error {
		if strings.HasSuffix(newpath, PreviousRelease) {
			return errors.New("disk full")
		}
		return os.Rename(oldpath, newpath)
	}
	err := n.stageUnsigned(t, newBuild, true)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	assertUntouched(t, n.base)
	if _, statErr := os.Stat(filepath.Join(n.base, PreviousRelease)); statErr == nil {
		t.Fatal("a release that could not be kept is there")
	}
	leftovers, _ := filepath.Glob(filepath.Join(n.base, stagingPrefix+"*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging directories left behind: %v", leftovers)
	}
}

// Deleting the release kept before happens after the replaced release is kept.
// Failing it is not a failed install: rolling back there would move the new
// release out of /opt/orama while nothing moves in, and the staging cleanup
// would delete it.
func TestStageKeepPrevious_aFailedDeleteOfTheOlderKeptReleaseIsNotAFailedStage(t *testing.T) {
	n := newReleaseOnlyNode(t)
	key, addr := newSigner(t)
	n.anchor = []string{addr}
	first := writeTarball(t, signedEntries(t, key, map[string]string{"bin/orama": "first cli"}))
	if err := stageArchive(n.stageTarget, StageOptions{Archive: first}); err != nil {
		t.Fatal(err)
	}
	if err := n.stageUnsigned(t, newBuild, true); err != nil {
		t.Fatal(err)
	}
	prevRemove, prevLog := keepRemoveAll, keepLog
	t.Cleanup(func() { keepRemoveAll, keepLog = prevRemove, prevLog })
	keepRemoveAll = func(string) error { return errors.New("device busy") }
	var logged strings.Builder
	keepLog = &logged

	newer := map[string]string{"bin/orama": "newer cli", "systemd/orama-namespace-x.service": "[Unit]\n"}
	if err := n.stageUnsigned(t, newer, true); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, "bin", "orama")); string(b) != "newer cli" {
		t.Fatalf("the new release is not installed: bin/orama = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(n.base, PreviousRelease, "bin", "orama")); string(b) != "new cli" {
		t.Fatalf("the replaced release was not kept: %q", b)
	}
	if !strings.Contains(logged.String(), "device busy") || !strings.Contains(logged.String(), stagedAside) {
		t.Fatalf("the failed delete was not reported with its path: %q", logged.String())
	}
}

// copyTree copies the tree at from to to, as a crash would leave it.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// snapshotBeforeEveryRename copies base before each rename the swap and the
// keep make, and once more when they are done: each copy is the tree a run
// killed at that step leaves.
func snapshotBeforeEveryRename(t *testing.T, base string, run func() error) []string {
	t.Helper()
	var snaps []string
	snap := func() {
		dir := t.TempDir()
		copyTree(t, base, dir)
		snaps = append(snaps, dir)
	}
	prevRename, prevKeep := renameEntry, keepRename
	t.Cleanup(func() { renameEntry, keepRename = prevRename, prevKeep })
	renameEntry = func(o, n string) error { snap(); return os.Rename(o, n) }
	keepRename = func(o, n string) error { snap(); return os.Rename(o, n) }
	if err := run(); err != nil {
		t.Fatal(err)
	}
	snap()
	return snaps
}

// A restore killed at any step must leave a node that recovers to the release
// it was running with the kept release whole, or already on the kept one. The
// kept release is the only copy of what the node is rolling back to: recovery
// deleting what the restore had moved out of it made the rollback impossible
// for ever.
func TestRecoverInterruptedSwap_aRestoreKilledAtAnyStepCanBeRunAgain(t *testing.T) {
	n := newReleaseOnlyNode(t)
	key, addr := newSigner(t)
	n.anchor = []string{addr}
	first := map[string]string{"bin/orama": "first cli", "bin/helper": "h", "systemd/orama-namespace-x.service": "[Unit]\n", "packages/p.deb": "pkg"}
	if err := stageArchive(n.stageTarget, StageOptions{Archive: writeTarball(t, signedEntries(t, key, first))}); err != nil {
		t.Fatal(err)
	}
	if err := n.stageUnsigned(t, newBuild, true); err != nil {
		t.Fatal(err)
	}

	snaps := snapshotBeforeEveryRename(t, n.base, func() error { return restorePrevious(n.stageTarget, builtVersion) })
	// The running release has 3 entries to move aside (manifest, bin, systemd),
	// the kept one 5 to move in, then the keep and the finished tree.
	if want := 3 + 5 + 1 + 1; len(snaps) != want {
		t.Fatalf("%d kill points, want %d", len(snaps), want)
	}
	for i, snap := range snaps {
		target := n.stageTarget
		target.base = snap
		if err := removeLeftoverStaging(snap); err != nil {
			t.Fatalf("kill point %d: recover: %v", i, err)
		}
		if read(t, filepath.Join(snap, "bin", "orama")) != "first cli" {
			if err := restorePrevious(target, builtVersion); err != nil {
				t.Fatalf("kill point %d: the restore cannot be run again: %v", i, err)
			}
		}
		v, err := archivetrust.VerifyTree(snap, []string{addr})
		if err != nil {
			t.Fatalf("kill point %d: the node is not on a whole previous release: %v", i, err)
		}
		if v.Manifest.Version == "" || read(t, filepath.Join(snap, "bin", "helper")) != "h" ||
			read(t, filepath.Join(snap, "packages", "p.deb")) != "pkg" {
			t.Fatalf("kill point %d: the previous release is incomplete: %+v", i, v.Manifest)
		}
	}
}

// A stage killed half-way discards what it had moved in; it does not need it.
func TestRecoverInterruptedSwap_aStageKilledHalfWayKeepsNothingOfTheNewRelease(t *testing.T) {
	n := newReleaseOnlyNode(t)
	snaps := snapshotBeforeEveryRename(t, n.base, func() error { return n.stageUnsigned(t, newBuild, false) })
	for i, snap := range snaps {
		if err := removeLeftoverStaging(snap); err != nil {
			t.Fatalf("kill point %d: %v", i, err)
		}
		if _, err := os.Lstat(filepath.Join(snap, PreviousRelease)); err == nil {
			t.Fatalf("kill point %d: a stage with nothing to keep left %s", i, PreviousRelease)
		}
		_, retiredErr := os.Stat(filepath.Join(snap, "bin", "retired-binary"))
		_, unitErr := os.Stat(filepath.Join(snap, "systemd", "orama-namespace-x.service"))
		switch cli := read(t, filepath.Join(snap, "bin", "orama")); {
		case cli == "old cli" && retiredErr == nil && unitErr != nil:
		case cli == "new cli" && retiredErr != nil && unitErr == nil:
		default:
			t.Fatalf("kill point %d: a mix of the two releases (bin/orama %q, retired binary %v, unit %v)", i, cli, retiredErr, unitErr)
		}
	}
}

func TestRecoverUnderLock_putsTheReleaseBackAndFreesTheLock(t *testing.T) {
	base := installedNode(t)
	writeTree(t, filepath.Join(base, stagingPrefix+"dead"), map[string]string{
		"old/" + archivetrust.ManifestName: "old manifest",
		"old/bin/orama":                    "old cli",
		"new/placeholder":                  "",
		stagedSwapping:                     "",
	})
	if err := os.Remove(filepath.Join(base, archivetrust.ManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := recoverUnderLock(base); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(base, archivetrust.ManifestName)); got != "old manifest" {
		t.Fatalf("manifest = %q", got)
	}
	unlock, err := archivetrust.LockArchiveDir(base)
	if err != nil {
		t.Fatalf("the archive lock was not freed: %v", err)
	}
	_ = unlock()
}

func TestRecoverUnderLock_nothingToRecoverIsNotAnError(t *testing.T) {
	if err := recoverUnderLock(installedNode(t)); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverUnderLock_aMissingBaseIsAnError(t *testing.T) {
	if err := recoverUnderLock(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a base that does not exist was recovered")
	}
}

func TestCheckBaseOwnedByRoot_refusesABaseAnotherUserOwns(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the test directory is root's")
	}
	if err := checkBaseOwnedByRoot(t.TempDir()); err == nil {
		t.Fatal("a directory owned by a user was accepted as root's")
	}
}

// A stage that keeps the release it replaces, killed at any step, leaves the
// node on a whole release and the release it replaced is either still running
// or kept: removing the kept release before the replaced one was in its place
// lost the only way back when the run died between the two.
func TestRecoverInterruptedSwap_aKeepingStageKilledAtAnyStepNeverLosesTheReplacedRelease(t *testing.T) {
	n := newReleaseOnlyNode(t)
	// A first stage leaves "old cli" kept, so the keep under test replaces a
	// kept release.
	if err := n.stageUnsigned(t, newBuild, true); err != nil {
		t.Fatal(err)
	}
	third := map[string]string{}
	for k, v := range newBuild {
		third[k] = v
	}
	third["bin/orama"] = "third cli"

	snaps := snapshotBeforeEveryRename(t, n.base, func() error { return n.stageUnsigned(t, third, true) })
	var sawKeptAfterSwap bool
	for i, snap := range snaps {
		if err := removeLeftoverStaging(snap); err != nil {
			t.Fatalf("kill point %d: recover: %v", i, err)
		}
		kept := read(t, filepath.Join(snap, PreviousRelease, "bin", "orama"))
		switch running := read(t, filepath.Join(snap, "bin", "orama")); running {
		case "new cli":
			if kept != "old cli" {
				t.Fatalf("kill point %d: still on the second release, but the kept one is %q, want the first", i, kept)
			}
		case "third cli":
			sawKeptAfterSwap = true
			if kept != "new cli" {
				t.Fatalf("kill point %d: on the third release, but the kept one is %q: the release it replaced is lost", i, kept)
			}
		default:
			t.Fatalf("kill point %d: bin/orama = %q, not a whole release", i, running)
		}
		leftovers, _ := filepath.Glob(filepath.Join(snap, stagingPrefix+"*"))
		if len(leftovers) != 0 {
			t.Fatalf("kill point %d: staging left behind: %v", i, leftovers)
		}
	}
	if !sawKeptAfterSwap {
		t.Fatal("no kill point after the swap")
	}
}
