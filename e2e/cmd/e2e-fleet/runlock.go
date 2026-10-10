package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// runLockFile is the lock a runner holds on its run directory while it runs.
// The lock is an flock: the kernel releases it when the runner dies, however
// it dies, so a held lock means a live runner and a free one means none.
const runLockFile = "run.lock"

// holdRun takes the run directory's lock, shared (any number of runners of
// one directory may hold it), and returns its release. `sweep-namespaces`
// leaves the namespaces of a held directory alone: its tests are still
// running and still own them.
func holdRun(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create the run directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, runLockFile), os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open the run lock in %s: %w", dir, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to lock the run directory %s: %w", dir, err)
	}
	return func() { f.Close() }, nil
}

// runHeld reports whether a runner holds dir's lock. A directory with no lock
// file has had no runner of this version.
func runHeld(dir string) (bool, error) {
	f, err := os.Open(filepath.Join(dir, runLockFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to open the run lock in %s: %w", dir, err)
	}
	defer f.Close()
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, unix.EWOULDBLOCK):
		return true, nil
	}
	return false, fmt.Errorf("failed to probe the run lock in %s: %w", dir, err)
}

// idleRoots splits roots into the ones no runner holds and the ones a runner
// holds.
func idleRoots(roots []string) (idle, held []string, err error) {
	for _, root := range roots {
		h, herr := runHeld(root)
		if herr != nil {
			return nil, nil, herr
		}
		if h {
			held = append(held, root)
		} else {
			idle = append(idle, root)
		}
	}
	return idle, held, nil
}
