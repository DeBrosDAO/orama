//go:build unix

package updateagent

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// writableByOthers are the mode bits that would let someone other than the
// owner change the entries of a directory.
const writableByOthers = 0o022

// checkWorkDir creates dir when it is absent and refuses one that is not a
// directory (a symlink is not) owned by owner and writable by owner alone: the
// install intent and the fetched releases are kept in it, and the agent acts
// as root on what it reads there.
func checkWorkDir(dir string, owner uint32) error {
	if err := os.MkdirAll(dir, workDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s (%T)", dir, info.Sys())
	}
	if !info.IsDir() || st.Uid != owner || info.Mode().Perm()&writableByOthers != 0 {
		return fmt.Errorf("%s must be a directory owned by uid %d and writable only by its owner (it is %s, uid %d); "+
			"refusing to keep the install intent there", dir, owner, info.Mode(), st.Uid)
	}
	return nil
}

// openNoFollow opens path for reading without following a symlink at its end.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
}
