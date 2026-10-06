//go:build unix

package rootfs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// DirOwner is the owner of the existing directory at path, reached without
// following a symlink in any component below the anchor. A symlink anywhere on
// the way, or at path itself, is an error matching ErrSymlink; so is anything
// at path that is not a directory.
//
// It is for root checking a directory that PID 1 is about to resolve by name —
// a unit's WorkingDirectory or a bind-mount source — in a tree another user can
// write.
func (r Root) DirOwner(path string) (uid, gid uint32, err error) {
	comps, err := r.components(path)
	if err != nil {
		return 0, 0, err
	}
	fd, err := r.walk(path, comps, false, 0)
	if err != nil {
		return 0, 0, err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return st.Uid, st.Gid, nil
}
