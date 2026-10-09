package push

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// salvageDirPerm: only root reads a release.
const salvageDirPerm = 0o700

// RecoverInterrupted undoes a swap of the node's /opt/orama that a killed run
// left half-done, under the archive lock, so the installed release can be read
// again. Nothing to undo is not an error.
func RecoverInterrupted() error {
	if err := clierr.RequireRoot("recovering an interrupted release swap"); err != nil {
		return err
	}
	if err := checkBaseOwnedByRoot(install.OramaBase); err != nil {
		return err
	}
	return recoverUnderLock(install.OramaBase)
}

// recoverUnderLock is RecoverInterrupted for the archive base base, once base
// is known to be root's.
func recoverUnderLock(base string) (err error) {
	unlock, err := archivetrust.LockArchiveDir(base)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return recoverInterruptedSwap(base)
}

// recoverInterruptedSwap puts back the release a swap that was killed half-way
// had moved aside. swapArchive moves the current manifest out first and the new
// one in last, so a tree with no manifest, beside a staging directory whose
// old/ holds one, was caught between the two. Without this the next run would
// delete that staging directory and the machine would have no release at all.
// It runs under the archive lock, before leftovers are removed.
//
// The entries the swap had already moved in are not deleted: a stage moves them
// back into its staging copy of the new release, and a restore (which has no
// such copy: it swaps in the kept release itself) back into PreviousRelease,
// which is then whole again for the restore to be run again. That is done only
// once the swap had every current entry aside (stagedSwapping): before that,
// what is in base is still the current release.
func recoverInterruptedSwap(base string) error {
	if _, err := os.Lstat(filepath.Join(base, archivetrust.ManifestName)); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat the installed manifest: %w", err)
	}
	olds, err := filepath.Glob(filepath.Join(base, stagingPrefix+"*", stagedOld))
	if err != nil {
		return fmt.Errorf("list leftover staging directories: %w", err)
	}
	for _, old := range olds {
		if _, err := os.Lstat(filepath.Join(old, archivetrust.ManifestName)); err != nil {
			continue
		}
		staging := filepath.Dir(old)
		return restoreEntries(old, base, swapSource(base, staging), movedIn(staging))
	}
	return nil
}

// movedIn reports whether the swap in staging had begun to move the new release
// into base.
func movedIn(staging string) bool {
	_, err := os.Lstat(filepath.Join(staging, stagedSwapping))
	return err == nil
}

// swapSource is where the entries a swap in staging had moved into base came
// from: the staging copy of the new release a stage extracts (stagedNew), or,
// for a restore, which swaps the kept release in, PreviousRelease.
func swapSource(base, staging string) string {
	if _, err := os.Lstat(filepath.Join(staging, stagedNew)); err == nil {
		return filepath.Join(staging, stagedNew)
	}
	return filepath.Join(base, PreviousRelease)
}

// restoreEntries moves each archive entry in from back to base. When the
// interrupted swap had begun moving the new release in (moved), what it had put
// in the place of an entry goes back to source, whole, not away.
func restoreEntries(from, base, source string, moved bool) error {
	for _, p := range archivetrust.OwnedPaths {
		dst := filepath.Join(base, p)
		if moved {
			if err := returnEntry(dst, filepath.Join(source, p)); err != nil {
				return err
			}
		}
		src := filepath.Join(from, p)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("put the previous %s back: %w", p, err)
		}
	}
	return nil
}

// returnEntry moves the half-installed entry at dst to to, when there is one.
func returnEntry(dst, to string) error {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), salvageDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(to), err)
	}
	if err := os.Rename(dst, to); err != nil {
		return fmt.Errorf("move the half-installed %s back to %s: %w", dst, to, err)
	}
	return nil
}
