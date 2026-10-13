//go:build unix

package updateagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// runLockName is the file one agent run holds for its whole length.
const runLockName = "run.lock"

// errRunning is another agent run on this machine.
var errRunning = errors.New("another auto-update run is in progress on this machine")

// lockRun takes the machine's run lock without waiting, and returns how to free
// it. The timer, a person running the command and a run the timer fired late
// must not install at once: the rollout lock names the node, so it cannot tell
// two runs of one node apart.
func lockRun(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, workDirPerm); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, runLockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW, journalPerm)
	if err != nil {
		return nil, fmt.Errorf("open the run lock %s: %w", path, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Join(errRunning, f.Close())
	}
	return func() error { return errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close()) }, nil
}
