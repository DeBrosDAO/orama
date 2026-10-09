package push

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// PreviousRelease is where a stage with KeepPrevious leaves the archive it
// replaced, under /opt/orama. It holds the same entries an archive does
// (archivetrust.OwnedPaths), so putting it back is the swap a stage makes. It
// is not in OwnedPaths: an archive cannot write it, and a stage does not
// replace it.
const PreviousRelease = ".release-previous"

// keepRename is os.Rename; a test fails it to exercise keepPrevious's caller.
var keepRename = os.Rename

// keepPrevious moves the entries a stage replaced (oldDir, which swapArchive
// made 0700: only root reads a kept release) to base's PreviousRelease,
// replacing the one kept before. The release kept before is moved aside, into
// the staging directory oldDir is in, and deleted only once oldDir is in its
// place, so a kill at any step leaves a whole kept release (or oldDir, which
// completePendingKeep keeps). When it fails, oldDir still holds the replaced
// entries, so the caller can put them back, and the release kept before is
// where it was.
func keepPrevious(base, oldDir string) error {
	kept := filepath.Join(base, PreviousRelease)
	aside := filepath.Join(filepath.Dir(oldDir), stagedAside)
	asideKept := false
	if _, err := os.Lstat(kept); err == nil {
		if err := keepRename(kept, aside); err != nil {
			return fmt.Errorf("move the release kept before aside: %w", err)
		}
		asideKept = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat the release kept before: %w", err)
	}
	if err := keepRename(oldDir, kept); err != nil {
		err = fmt.Errorf("keep the replaced release at %s: %w", kept, err)
		if asideKept {
			if backErr := os.Rename(aside, kept); backErr != nil {
				err = errors.Join(err, fmt.Errorf("put the release kept before back at %s: %w", kept, backErr))
			}
		}
		return err
	}
	if err := os.RemoveAll(aside); err != nil {
		return fmt.Errorf("remove the release kept before, at %s: %w", aside, err)
	}
	return nil
}

// markKeep records in staging that the release its swap replaces is to be
// kept, before the swap makes it the only copy.
func markKeep(staging string) error {
	if err := os.WriteFile(filepath.Join(staging, stagedKeep), nil, 0o600); err != nil {
		return fmt.Errorf("record that the replaced release is to be kept: %w", err)
	}
	return nil
}

// completePendingKeep finishes a keep that a killed run left undone: a staging
// directory marked stagedKeep whose old/ still holds a release, beside an
// installed manifest (the swap had finished), and a PreviousRelease that is not
// that release. It runs under the archive lock, before leftovers are removed.
func completePendingKeep(base string) error {
	markers, err := filepath.Glob(filepath.Join(base, stagingPrefix+"*", stagedKeep))
	if err != nil {
		return fmt.Errorf("list leftover staging directories: %w", err)
	}
	for _, marker := range markers {
		old := filepath.Join(filepath.Dir(marker), stagedOld)
		oldManifest, err := os.ReadFile(filepath.Join(old, archivetrust.ManifestName))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("read the manifest of the release to keep: %w", err)
		}
		keptManifest, err := os.ReadFile(filepath.Join(base, PreviousRelease, archivetrust.ManifestName))
		if err == nil && bytes.Equal(oldManifest, keptManifest) {
			continue
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read the manifest of the kept release: %w", err)
		}
		if err := keepPrevious(base, old); err != nil {
			return fmt.Errorf("complete the keep of the release a killed run replaced: %w", err)
		}
	}
	return nil
}

// RestorePrevious puts back the archive the last KeepPrevious stage
// replaced. The kept release is verified as a stage verifies one before it is
// swapped in, so a tree someone changed meanwhile is not restored, and it must
// be version: the caller names the release it is rolling back to, and a kept
// release that is another one (a stage killed before it kept the release it
// replaced leaves an older one) is refused. The release that was running
// becomes the kept one.
func RestorePrevious(version string) error {
	if err := clierr.RequireRoot("restoring the previous release"); err != nil {
		return err
	}
	if err := checkBaseOwnedByRoot(install.OramaBase); err != nil {
		return err
	}
	return restorePrevious(nodeTarget(), version)
}

func restorePrevious(t stageTarget, version string) (err error) {
	unlock, err := archivetrust.LockArchiveDir(t.base)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	kept := filepath.Join(t.base, PreviousRelease)
	if _, err := os.Lstat(kept); err != nil {
		return fmt.Errorf("there is no kept release to restore at %s: %w", kept, err)
	}
	verified, err := t.verify(kept)
	if err != nil {
		return fmt.Errorf("refusing to restore the kept release, nothing under %s was changed: %w", t.base, err)
	}
	if verified.Manifest.Version != version {
		return fmt.Errorf("refusing to restore the kept release, nothing under %s was changed: it is release %s and the release to go back to is %s",
			t.base, verified.Manifest.Version, version)
	}
	staging, err := os.MkdirTemp(t.base, stagingPrefix)
	if err != nil {
		return fmt.Errorf("create a staging directory in %s: %w", t.base, err)
	}
	defer func() {
		if rmErr := os.RemoveAll(staging); rmErr != nil {
			err = errors.Join(err, fmt.Errorf("remove the staging directory %s: %w", staging, rmErr))
		}
	}()
	oldDir := filepath.Join(staging, stagedOld)
	if err := markKeep(staging); err != nil {
		return err
	}
	if err := swapArchive(t.base, kept, oldDir); err != nil {
		return err
	}
	if err := os.RemoveAll(kept); err != nil {
		return fmt.Errorf("remove the restored copy %s: %w", kept, err)
	}
	return keepPrevious(t.base, oldDir)
}
