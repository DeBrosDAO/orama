package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StopDir stops the agent serving dir and shreds dir, from a process that did
// not start it (a teardown run after a crash). The pid comes from the ready
// file and is signalled only when that process's command line names dir, so a
// recycled pid is never killed. A dir that is already gone is not an error.
func StopDir(ctx context.Context, dir string) error {
	if !strings.HasPrefix(filepath.Base(dir), DirPrefix) {
		return fmt.Errorf("refusing to remove %s: it is not a test agent directory (%s*)", dir, DirPrefix)
	}
	if err := checkIsolation(dir); err != nil {
		return err
	}
	rf, err := readReady(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("failed to read the agent's ready file in %s: %w", dir, err)
	default:
		if err := stopPID(ctx, rf.PID, dir); err != nil {
			return err
		}
	}
	return shredTree(dir)
}

// stopPID sends SIGTERM to pid if it is the agent of dir and waits for it to
// exit, killing it after stopTimeout.
func stopPID(ctx context.Context, pid int, dir string) error {
	if pid <= 0 || !alive(pid) {
		return nil
	}
	ours, err := commandNames(ctx, pid, dir)
	if err != nil {
		return err
	}
	if !ours {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to signal the test agent (pid %d): %w", pid, err)
	}
	if waitExit(ctx, pid, stopTimeout) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to kill the test agent (pid %d): %w", pid, err)
	}
	if !waitExit(ctx, pid, stopTimeout) {
		return fmt.Errorf("the test agent (pid %d) survived SIGKILL", pid)
	}
	return fmt.Errorf("the test agent (pid %d) ignored SIGTERM for %s and was killed", pid, stopTimeout)
}

// commandNames reports whether pid's command line passes dir as the
// argument of --home, exactly as launch does: a process that merely mentions
// dir, or a directory dir is a prefix of, is not the agent. ps joins the
// arguments with spaces, so a dir holding whitespace never matches (agent
// directories come from os.MkdirTemp under DirPrefix and hold none).
func commandNames(ctx context.Context, pid int, dir string) (bool, error) {
	out, err := exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// ps exits 1 when the pid is gone.
			return false, nil
		}
		return false, fmt.Errorf("failed to read the command line of pid %d: %w", pid, err)
	}
	return passesHome(string(out), dir), nil
}

// passesHome reports whether the command line holds `--home dir`.
func passesHome(cmdline, dir string) bool {
	args := strings.Fields(cmdline)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == homeFlag && args[i+1] == dir {
			return true
		}
	}
	return false
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// waitExit polls until pid is gone or timeout passes.
func waitExit(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for alive(pid) {
		select {
		case <-deadline.C:
			return false
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
	return true
}
