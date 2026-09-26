//go:build unix

package rootfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	// dirFlags open one directory component, refusing a symlink.
	dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	// leafFlags open an existing leaf, refusing a symlink. O_NONBLOCK: a FIFO
	// planted at the leaf must not hang root; its type is checked before
	// anything is read.
	leafFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	// tempFlags create WriteFile's temporary file; O_EXCL never reuses an
	// existing entry, whatever it is.
	tempFlags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	// tempFileMode is the temporary file's mode until the requested one is
	// applied to it: nobody else may open it in the meantime.
	tempFileMode = 0o600
	// tempSuffixBytes of randomness name the temporary file.
	tempSuffixBytes = 8
)

// renameat installs WriteFile's temporary file; a variable so a test can fail
// it and check that nothing is left behind.
var renameat = unix.Renameat

// ReadFile reads the regular file at path, refusing one larger than limit
// bytes. A missing file is an error that matches fs.ErrNotExist.
func (r Root) ReadFile(path string, limit int64) ([]byte, error) {
	f, err := r.openLeaf(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%s)", path, info.Mode().Type())
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte limit for this file", path, info.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s grew past the %d-byte limit for this file while it was read", path, limit)
	}
	return data, nil
}

// WriteFile replaces the file at path with data, atomically: a temporary file
// is created beside it, written, given perm (and, when it replaces a file, that
// file's owner) through its descriptor, synced, and renamed over path. The
// parent directory must exist. A reader sees the old contents or the new, never
// a mix, and a failure leaves the old file as it was.
func (r Root) WriteFile(path string, data []byte, perm fs.FileMode) error {
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

	owner, err := existingOwner(dirFD, leaf, path)
	if err != nil {
		return err
	}
	tmpName, err := writeTemp(dirFD, leaf, path, data, perm, owner)
	if err != nil {
		return err
	}
	if err := renameat(dirFD, tmpName, dirFD, leaf); err != nil {
		unix.Unlinkat(dirFD, tmpName, 0)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := unix.Fsync(dirFD); err != nil {
		return fmt.Errorf("sync the directory of %s: %w", path, err)
	}
	return nil
}

// MkdirAll creates path and every missing directory above it, below the anchor,
// with perm (less the umask), like os.MkdirAll. A missing anchor is created too,
// with anchorPerm. Existing directories are left as they are.
func (r Root) MkdirAll(path string, perm fs.FileMode) error {
	comps, err := r.components(path)
	if err != nil {
		return err
	}
	fd, err := r.walk(path, comps, true, perm)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

// Chmod sets the mode of the regular file or directory at path.
func (r Root) Chmod(path string, mode fs.FileMode) error {
	f, err := r.openOwnable(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// Chown sets the owner of the regular file or directory at path.
func (r Root) Chown(path string, uid, gid int) error {
	f, err := r.openOwnable(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chown(uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// Remove removes the file or empty directory at path. A missing path is an
// error that matches fs.ErrNotExist.
func (r Root) Remove(path string) error {
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

	var st unix.Stat_t
	if err := unix.Fstatat(dirFD, leaf, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	flags := 0
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		return fmt.Errorf("remove %s: %w", path, ErrSymlink)
	case unix.S_IFDIR:
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(dirFD, leaf, flags); err != nil {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// fileOwner is who owns a file WriteFile replaces.
type fileOwner struct {
	uid, gid uint32
}

// existingOwner is the owner of the regular file WriteFile is about to
// replace, or nil when there is none.
func existingOwner(dirFD int, leaf, path string) (*fileOwner, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dirFD, leaf, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		return &fileOwner{uid: st.Uid, gid: st.Gid}, nil
	case unix.S_IFLNK:
		return nil, fmt.Errorf("%s %w", path, ErrSymlink)
	default:
		return nil, fmt.Errorf("%s exists and is not a regular file; refusing to replace it", path)
	}
}

// writeTemp creates a temporary file beside leaf holding data, with perm and
// owner applied through its descriptor, and returns its name. On failure the
// file is removed.
func writeTemp(dirFD int, leaf, path string, data []byte, perm fs.FileMode, owner *fileOwner) (string, error) {
	suffix := make([]byte, tempSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("name a temporary file for %s: %w", path, err)
	}
	name := "." + leaf + ".tmp-" + hex.EncodeToString(suffix)
	fd, err := unix.Openat(dirFD, name, tempFlags, tempFileMode)
	if err != nil {
		return "", fmt.Errorf("create a temporary file for %s: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(filepath.Dir(path), name))
	if err := fillTemp(f, data, perm, owner); err != nil {
		f.Close()
		unix.Unlinkat(dirFD, name, 0)
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		unix.Unlinkat(dirFD, name, 0)
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return name, nil
}

// fillTemp writes data to f, gives it owner (when set) and perm, and syncs it.
func fillTemp(f *os.File, data []byte, perm fs.FileMode, owner *fileOwner) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	if owner != nil {
		var st unix.Stat_t
		if err := unix.Fstat(int(f.Fd()), &st); err != nil {
			return err
		}
		if st.Uid != owner.uid || st.Gid != owner.gid {
			if err := f.Chown(int(owner.uid), int(owner.gid)); err != nil {
				return err
			}
		}
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	return f.Sync()
}
