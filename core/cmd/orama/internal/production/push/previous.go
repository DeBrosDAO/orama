package push

import (
	"errors"
	"fmt"
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
// replacing the one kept before. When it fails, oldDir still holds the replaced
// entries, so the caller can put them back.
func keepPrevious(base, oldDir string) error {
	kept := filepath.Join(base, PreviousRelease)
	if err := os.RemoveAll(kept); err != nil {
		return fmt.Errorf("remove the release kept before: %w", err)
	}
	if err := keepRename(oldDir, kept); err != nil {
		return fmt.Errorf("keep the replaced release at %s: %w", kept, err)
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
	if err := swapArchive(t.base, kept, oldDir); err != nil {
		return err
	}
	if err := os.RemoveAll(kept); err != nil {
		return fmt.Errorf("remove the restored copy %s: %w", kept, err)
	}
	return keepPrevious(t.base, oldDir)
}
