package push

import (
	"errors"
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
