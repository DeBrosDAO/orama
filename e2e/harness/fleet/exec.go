package fleet

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Budgets for node-level helpers.
const (
	// CommandBudget bounds one remote command.
	CommandBudget = 2 * time.Minute
	// CleanupBudget bounds one cleanup. Cleanups run after the test's context
	// is cancelled, so they use their own.
	CleanupBudget = 3 * time.Minute
	// JournalMaxLines bounds a journal excerpt.
	JournalMaxLines = 5000
)

// safeArg is what a unit name, process name or path may contain to be put in a
// remote shell command unquoted.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9@._:/=+-]+$`)

// ShellQuote single-quotes s for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func requireSafe(t testing.TB, what, s string) {
	t.Helper()
	if !safeArg.MatchString(s) {
		t.Fatalf("%s %q contains characters the harness does not put in a remote command", what, s)
	}
}

// Exec runs cmd on node and returns its output whatever the exit code. The
// test fails only when the command could not be run at all.
func (f *Fleet) Exec(t testing.TB, n Node, cmd string) Output {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), CommandBudget)
	defer cancel()
	out, err := f.shellFor(t.Name(), n).Run(ctx, cmd)
	if err != nil {
		t.Fatal(f.Redact(fmt.Sprintf("failed to run on %s (%s): %v", n.Name, cmd, err)))
	}
	return out
}

// MustExec is Exec that also fails the test on a non-zero exit.
func (f *Fleet) MustExec(t testing.TB, n Node, cmd string) Output {
	t.Helper()
	out := f.Exec(t, n, cmd)
	if out.Exit != 0 {
		t.Fatal(f.Redact(fmt.Sprintf("%s: %q exited %d\nstdout: %s\nstderr: %s", n.Name, cmd, out.Exit, out.Stdout, out.Stderr)))
	}
	return out
}

// cleanupExec runs cmd from a t.Cleanup, reporting a failure without Fatal
// (Fatal in a cleanup would skip the remaining cleanups).
func (f *Fleet) cleanupExec(t testing.TB, n Node, cmd string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CleanupBudget)
	defer cancel()
	out, err := f.shellFor(t.Name(), n).Run(ctx, cmd)
	if err != nil {
		t.Error(f.Redact(fmt.Sprintf("cleanup on %s failed to run (%s): %v — the node may be left disturbed", n.Name, cmd, err)))
		return
	}
	if out.Exit != 0 {
		t.Error(f.Redact(fmt.Sprintf("cleanup on %s exited %d (%s): %s — the node may be left disturbed", n.Name, out.Exit, cmd, out.Stderr)))
	}
}

// Journal returns the unit's journal since the given time, at most
// JournalMaxLines lines.
func (f *Fleet) Journal(t testing.TB, n Node, unit string, since time.Time) string {
	t.Helper()
	requireSafe(t, "unit", unit)
	cmd := fmt.Sprintf("journalctl -u %s --since @%d --no-pager -o short-iso -n %d", unit, since.Unix(), JournalMaxLines)
	return f.MustExec(t, n, cmd).Stdout
}

// Unit returns systemd's active state for unit: active, inactive, failed,
// activating, ... (systemctl is-active exits non-zero for all but active).
func (f *Fleet) Unit(t testing.TB, n Node, unit string) string {
	t.Helper()
	requireSafe(t, "unit", unit)
	return strings.TrimSpace(f.Exec(t, n, "systemctl is-active "+unit).Stdout)
}

// ReadFile reads a file on node.
func (f *Fleet) ReadFile(t testing.TB, n Node, path string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), CommandBudget)
	defer cancel()
	data, err := f.shellFor(t.Name(), n).Get(ctx, path)
	if err != nil {
		t.Fatalf("failed to read %s on %s: %v", path, n.Name, err)
	}
	return data
}
