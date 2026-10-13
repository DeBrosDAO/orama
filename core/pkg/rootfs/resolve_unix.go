//go:build unix

package rootfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openAnchor opens the anchor, creating it first when create is set, and
// checks that only root can change it.
func (r Root) openAnchor(create bool) (int, error) {
	if !filepath.IsAbs(r.dir) {
		return -1, fmt.Errorf("anchor %q is not an absolute path", r.dir)
	}
	if create {
		// The anchor's own path is trusted (only root can write it or any
		// directory above it), so the kernel may resolve it.
		if err := os.MkdirAll(r.dir, anchorPerm); err != nil {
			return -1, fmt.Errorf("create %s: %w", r.dir, err)
		}
	}
	fd, err := unix.Open(r.dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", r.dir, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("stat %s: %w", r.dir, err)
	}
	if err := checkAnchor(r.dir, st.Uid, uint32(st.Mode), os.Geteuid()); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// walk opens the directory made of comps below the anchor, one component at a
// time, never following a symlink. With create, missing directories are made
// with perm. path is only for error messages.
func (r Root) walk(path string, comps []string, create bool, perm fs.FileMode) (int, error) {
	fd, err := r.openAnchor(create)
	if err != nil {
		return -1, err
	}
	cur := r.dir
	for _, name := range comps {
		cur = filepath.Join(cur, name)
		next, err := openDirAt(fd, name, cur, create, perm)
		unix.Close(fd)
		if err != nil {
			return -1, fmt.Errorf("%s: %w", path, err)
		}
		fd = next
	}
	return fd, nil
}

// openDirAt opens the directory name in dirFD without following a symlink,
// creating it with perm first when create is set and it is missing.
func openDirAt(dirFD int, name, path string, create bool, perm fs.FileMode) (int, error) {
	fd, err := unix.Openat(dirFD, name, dirFlags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if err := unix.Mkdirat(dirFD, name, uint32(perm.Perm())); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create directory %s: %w", path, err)
		}
		fd, err = unix.Openat(dirFD, name, dirFlags, 0)
	}
	if err != nil {
		return -1, describeOpenError(dirFD, name, path, err)
	}
	return fd, nil
}

// openLeaf opens the existing entry at path read-only, without following a
// symlink and without blocking on a FIFO.
func (r Root) openLeaf(path string) (*os.File, error) {
	comps, err := r.leafComponents(path)
	if err != nil {
		return nil, err
	}
	dirFD, err := r.walk(path, comps[:len(comps)-1], false, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	leaf := comps[len(comps)-1]
	fd, err := unix.Openat(dirFD, leaf, leafFlags, 0)
	if err != nil {
		return nil, describeOpenError(dirFD, leaf, path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openOwnable opens path for Chmod or Chown: a directory, or a regular file
// with a single link. A file with more than one link may be a hard link to a
// file outside the tree, whose mode or owner root would change.
func (r Root) openOwnable(path string) (*os.File, error) {
	f, err := r.openLeaf(path)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		f.Close()
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return f, nil
	case unix.S_IFREG:
		if st.Nlink != 1 {
			f.Close()
			return nil, fmt.Errorf("%s has %d hard links; root does not change a file that may also live outside this tree", path, st.Nlink)
		}
		return f, nil
	default:
		f.Close()
		return nil, fmt.Errorf("%s is neither a regular file nor a directory", path)
	}
}

// describeOpenError turns a failed no-follow open of name in dirFD into an
// error naming what was in the way.
func describeOpenError(dirFD int, name, path string, err error) error {
	var st unix.Stat_t
	if !errors.Is(err, unix.ENOENT) && unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fmt.Errorf("%s %w", path, ErrSymlink)
	}
	return fmt.Errorf("open %s: %w", path, err)
}
