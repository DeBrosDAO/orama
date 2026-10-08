package push

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// recoverInterruptedSwap puts back the release a swap that was killed half-way
// had moved aside. swapArchive moves the current manifest out first and the new
// one in last, so a tree with no manifest, beside a staging directory whose
// old/ holds one, was caught between the two. Without this the next run would
// delete that staging directory and the machine would have no release at all.
// It runs under the archive lock, before leftovers are removed.
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
		return restoreEntries(old, base)
	}
	return nil
}

// restoreEntries moves each archive entry in from back to base, removing what
// the interrupted swap had put there.
func restoreEntries(from, base string) error {
	for _, p := range archivetrust.OwnedPaths {
		src := filepath.Join(from, p)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat %s: %w", src, err)
		}
		dst := filepath.Join(base, p)
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("remove the half-installed %s: %w", dst, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("put the previous %s back: %w", p, err)
		}
	}
	return nil
}
