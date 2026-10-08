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

// previousDirPerm: only root reads a kept release.
const previousDirPerm = 0o700

// keepPrevious moves the entries a stage replaced (oldDir) to base's
// PreviousRelease, replacing the one kept before.
func keepPrevious(base, oldDir string) error {
	kept := filepath.Join(base, PreviousRelease)
	if err := os.RemoveAll(kept); err != nil {
		return fmt.Errorf("remove the release kept before: %w", err)
	}
	if err := os.Rename(oldDir, kept); err != nil {
		return fmt.Errorf("keep the replaced release at %s: %w", kept, err)
	}
	if err := os.Chmod(kept, previousDirPerm); err != nil {
		return fmt.Errorf("restrict %s: %w", kept, err)
	}
	return nil
}

// RestorePrevious puts back the archive the last KeepPrevious stage
// replaced. The kept release is verified as a stage verifies one before it is
// swapped in, so a tree someone changed meanwhile is not restored. The release
// that was running becomes the kept one.
func RestorePrevious() error {
	if err := clierr.RequireRoot("restoring the previous release"); err != nil {
		return err
	}
	if err := checkBaseOwnedByRoot(install.OramaBase); err != nil {
		return err
	}
	return restorePrevious(nodeTarget())
}

func restorePrevious(t stageTarget) (err error) {
	unlock, err := archivetrust.LockArchiveDir(t.base)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	kept := filepath.Join(t.base, PreviousRelease)
	if _, err := os.Lstat(kept); err != nil {
		return fmt.Errorf("there is no kept release to restore at %s: %w", kept, err)
	}
	if _, err := t.verify(kept); err != nil {
		return fmt.Errorf("refusing to restore the kept release, nothing under %s was changed: %w", t.base, err)
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
