package fleet

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Exit codes of `test -e`: 0 the path exists, 1 it does not. Anything else
// (ssh's 255, a shell error) says nothing about the file.
const (
	testExists = 0
	testAbsent = 1
)

// fileSnapshot is a file as it was before a test changed it.
type fileSnapshot struct {
	existed bool
	data    []byte
	mode    os.FileMode
}

// WriteFile writes a file on node and registers a cleanup that restores the
// previous content and mode, or removes the file when there was none, and
// then reads the file back to verify the restore.
func (f *Fleet) WriteFile(t testing.TB, n Node, path string, data []byte, mode os.FileMode) {
	t.Helper()
	requireSafe(t, "path", path)
	snap := f.snapshotFile(t, n, path)
	t.Cleanup(func() { f.restoreFile(t, n, path, snap) })
	ctx, cancel := context.WithTimeout(t.Context(), CommandBudget)
	defer cancel()
	if err := f.shellFor(t.Name(), n).Put(ctx, path, data, mode); err != nil {
		t.Fatal(f.Redact(fmt.Sprintf("failed to write %s on %s: %v", path, n.Name, err)))
	}
}

// snapshotFile reads path's content and mode, or records that it is absent.
// An existence probe that neither says "exists" nor "absent" fails the test:
// guessing "absent" would make the cleanup delete a file it never created.
func (f *Fleet) snapshotFile(t testing.TB, n Node, path string) fileSnapshot {
	t.Helper()
	probe := f.Exec(t, n, "test -e "+path)
	switch probe.Exit {
	case testAbsent:
		return fileSnapshot{}
	case testExists:
	default:
		t.Fatal(f.Redact(fmt.Sprintf("cannot tell whether %s exists on %s: `test -e` exited %d: %s", path, n.Name, probe.Exit, probe.Stderr)))
	}
	st := f.MustExec(t, n, "stat -c %a "+path)
	mode, err := parseMode(st.Stdout)
	if err != nil {
		t.Fatalf("failed to read the mode of %s on %s: %v", path, n.Name, err)
	}
	return fileSnapshot{existed: true, data: f.ReadFile(t, n, path), mode: mode}
}

func parseMode(stdout string) (os.FileMode, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(stdout), 8, 32)
	if err != nil {
		return 0, fmt.Errorf("stat printed %q: %w", stdout, err)
	}
	return os.FileMode(v), nil
}

func (f *Fleet) restoreFile(t testing.TB, n Node, path string, snap fileSnapshot) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CleanupBudget)
	defer cancel()
	sh := f.shellFor(t.Name(), n)
	if !snap.existed {
		f.cleanupExec(t, n, "rm -f "+path)
	} else if err := sh.Put(ctx, path, snap.data, snap.mode); err != nil {
		t.Error(f.Redact(fmt.Sprintf("cleanup failed to restore %s on %s: %v", path, n.Name, err)))
		return
	}
	if err := verifyRestored(ctx, sh, path, snap); err != nil {
		t.Error(f.Redact(fmt.Sprintf("cleanup did not restore %s on %s: %v — later tests will see a disturbed node", path, n.Name, err)))
	}
}

// verifyRestored reads path back: absent when it did not exist, the same
// content and mode when it did.
func verifyRestored(ctx context.Context, sh Shell, path string, snap fileSnapshot) error {
	probe, err := sh.Run(ctx, "test -e "+path)
	if err != nil {
		return fmt.Errorf("existence check failed to run: %w", err)
	}
	if !snap.existed {
		if probe.Exit != testAbsent {
			return fmt.Errorf("still present (`test -e` exited %d)", probe.Exit)
		}
		return nil
	}
	if probe.Exit != testExists {
		return fmt.Errorf("missing (`test -e` exited %d)", probe.Exit)
	}
	got, err := sh.Get(ctx, path)
	if err != nil {
		return fmt.Errorf("failed to read it back: %w", err)
	}
	if !bytes.Equal(got, snap.data) {
		return fmt.Errorf("content differs from before the test (%d bytes, want %d)", len(got), len(snap.data))
	}
	st, err := sh.Run(ctx, "stat -c %a "+path)
	if err != nil || st.Exit != 0 {
		return fmt.Errorf("failed to read the mode back: exit %d: %v", st.Exit, err)
	}
	mode, err := parseMode(st.Stdout)
	if err != nil {
		return err
	}
	if mode != snap.mode {
		return fmt.Errorf("mode is %o, want %o", mode, snap.mode)
	}
	return nil
}
