//go:build unix

package releaseverify

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// seenLockSuffix names the lock file beside the rollback record. The
// record itself is replaced by rename, so it cannot carry the lock.
const seenLockSuffix = ".lock"

// seenLockPerm: only root opens the lock.
const seenLockPerm = 0o600

// lockSeen takes an exclusive lock on the rollback record's lock file and
// returns its release.
func lockSeen(seenPath string) (func() error, error) {
	path := seenPath + seenLockSuffix
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW, seenLockPerm)
	if err != nil {
		return nil, fmt.Errorf("open the release rollback lock %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("lock %s: %w", path, err), f.Close())
	}
	return func() error {
		return errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close())
	}, nil
}
