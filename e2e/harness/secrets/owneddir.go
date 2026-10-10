package secrets

import (
	"fmt"
	"os"
	"syscall"
)

// CheckOwnedDir refuses dir unless it is a real directory (not a link) owned
// by the current user with exactly mode: a directory another user created
// first at a predictable path (a work dir under the shared temp dir) is
// never used.
func CheckOwnedDir(dir string, mode os.FileMode) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm() != mode {
		return fmt.Errorf("%s must be a directory with mode %o, it is %s", dir, mode, info.Mode())
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not this user (uid %d)", dir, st.Uid, os.Getuid())
	}
	return nil
}

// MakePrivateDir makes dir 0700 when it is a real directory this user owns
// (a work dir the operator created with a looser mode), then checks it with
// CheckOwnedDir: a link, or a directory of another user, is refused, never
// changed.
func MakePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if info.IsDir() && ok && int(st.Uid) == os.Getuid() && info.Mode().Perm() != privateDirMode {
		if err := os.Chmod(dir, privateDirMode); err != nil {
			return fmt.Errorf("failed to make %s private (0700): %w", dir, err)
		}
	}
	return CheckOwnedDir(dir, privateDirMode)
}

// privateDirMode is the mode MakePrivateDir sets.
const privateDirMode = 0o700
