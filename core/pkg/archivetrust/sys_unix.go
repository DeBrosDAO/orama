//go:build unix

package archivetrust

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// noFollowFlags open an existing file read-only without following a symlink
// at the last component, and without blocking on a FIFO planted there.
const noFollowFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC

// lockFileName is the lock stage-archive and Phase 2b take on /opt/orama.
const lockFileName = ".archive.lock"

// lockFilePerm is the lock file's mode: only root opens it.
const lockFilePerm = 0o600

// ownerOf is the numeric owner of the file info describes.
func ownerOf(info fs.FileInfo) (fileOwnerIDs, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileOwnerIDs{}, fmt.Errorf("cannot read its owner (%T)", info.Sys())
	}
	return fileOwnerIDs{uid: st.Uid, gid: st.Gid}, nil
}

// lstat describes path itself, not what a symlink there points at.
func lstat(path string) (fs.FileInfo, error) {
	return os.Lstat(path)
}

// openNoFollow opens the regular file at path, refusing a symlink, and
// describes the descriptor it opened.
func openNoFollow(path string) (*os.File, fs.FileInfo, error) {
	f, err := os.OpenFile(path, noFollowFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file (%s)", path, info.Mode().Type())
	}
	return f, info, nil
}

// LockArchiveDir takes the exclusive lock on the archive under dir that
// stage-archive (replacing it) and install/upgrade Phase 2b (verifying and
// copying from it) share, so neither sees the other's half-done work. It
// waits for a holder to finish. The returned func releases it.
func LockArchiveDir(dir string) (func() error, error) {
	path := filepath.Join(dir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, lockFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open the archive lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f.Close, nil
}
