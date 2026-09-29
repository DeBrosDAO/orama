//go:build unix

package rootfs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// exclusiveFlags create a new leaf and fail when any entry, a symlink
// included, already has its name.
const exclusiveFlags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC

// CreateExclusive creates the file at path holding data, with perm, and fails
// with an error matching fs.ErrExist when anything is already there. The
// create is one openat with O_EXCL in the parent reached by the anchored
// walk, so of two callers racing for the same path exactly one succeeds. The
// parent directory must exist. On a failure after the create, the new file
// is removed.
func (r Root) CreateExclusive(path string, data []byte, perm fs.FileMode) error {
	comps, err := r.leafComponents(path)
	if err != nil {
		return err
	}
	dirFD, err := r.walk(path, comps[:len(comps)-1], false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	leaf := comps[len(comps)-1]
	fd, err := unix.Openat(dirFD, leaf, exclusiveFlags, uint32(perm.Perm()))
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(filepath.Dir(path), leaf))
	if err := fillTemp(f, data, perm, nil); err != nil {
		f.Close()
		unix.Unlinkat(dirFD, leaf, 0)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		unix.Unlinkat(dirFD, leaf, 0)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("sync the directory of %s: %w", path, err)
	}
	return nil
}
