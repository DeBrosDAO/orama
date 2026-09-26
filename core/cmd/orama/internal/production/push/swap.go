package push

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
)

// renameEntry is os.Rename; a test fails it to exercise the rollback.
var renameEntry = os.Rename

// swapArchive replaces the archive paths under base with the verified ones in
// newDir, keeping the current ones in oldDir until every new path is in place.
// The current manifest leaves first and the new one arrives last, so a tree
// caught half-way by a crash has no manifest and install refuses it rather
// than trusting a manifest beside binaries it does not describe. Any failure
// puts back what was there.
func swapArchive(base, newDir, oldDir string) error {
	if err := os.Mkdir(oldDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", oldDir, err)
	}
	var movedOut []string
	for _, p := range archivetrust.OwnedPaths {
		ok, err := moveIfPresent(filepath.Join(base, p), filepath.Join(oldDir, p))
		if err != nil {
			return errors.Join(fmt.Errorf("move the previous %s aside: %w", p, err), restore(base, oldDir, movedOut))
		}
		if ok {
			movedOut = append(movedOut, p)
		}
	}
	var movedIn []string
	for _, p := range slices.Backward(archivetrust.OwnedPaths) {
		ok, err := moveIfPresent(filepath.Join(newDir, p), filepath.Join(base, p))
		if err != nil {
			rollback := errors.Join(withdraw(base, newDir, movedIn), restore(base, oldDir, movedOut))
			return errors.Join(fmt.Errorf("move the verified %s into place: %w", p, err), rollback)
		}
		if ok {
			movedIn = append(movedIn, p)
		}
	}
	return nil
}

// moveIfPresent renames from to to when from exists, and reports whether it
// did.
func moveIfPresent(from, to string) (bool, error) {
	if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, renameEntry(from, to)
}

// withdraw moves the new paths already placed back out of base.
func withdraw(base, newDir string, placed []string) error {
	var errs []error
	for _, p := range placed {
		if err := renameEntry(filepath.Join(base, p), filepath.Join(newDir, p)); err != nil {
			errs = append(errs, fmt.Errorf("roll back %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

// restore puts the previous paths back into base.
func restore(base, oldDir string, moved []string) error {
	var errs []error
	for _, p := range moved {
		if err := renameEntry(filepath.Join(oldDir, p), filepath.Join(base, p)); err != nil {
			errs = append(errs, fmt.Errorf("restore the previous %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}
