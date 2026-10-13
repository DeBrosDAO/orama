package stages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// trapScript stands in for `go test`: it cleans up on SIGINT, and leaves a
// background child (the test binary's stand-in) that ignores SIGINT.
const trapScript = `#!/bin/sh
trap 'echo cleaned > "$1"; exit 3' INT
sleep 300 &
echo $! > "$2"
echo started > "$3"
wait
`

const pollEvery = 20 * time.Millisecond

func TestExecCommand_cancelSignalsGroupAndReapsIt(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-go")
	if err := os.WriteFile(script, []byte(trapScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cleaned, pidFile, started := filepath.Join(dir, "cleaned"), filepath.Join(dir, "pid"), filepath.Join(dir, "started")
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		exit int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		exit, err := execWithGrace(ctx, Command{Args: []string{script, cleaned, pidFile, started}}, 5*time.Second)
		done <- result{exit, err}
	}()
	eventually.Require(t, pollEvery, 10*time.Second, "fake go test to start", func() (bool, error) {
		_, err := os.Stat(started)
		return err == nil, err
	})
	cancel()
	res := <-done
	if res.err != nil || res.exit != 3 {
		t.Fatalf("exit %d err %v", res.exit, res.err)
	}
	if b, err := os.ReadFile(cleaned); err != nil || !strings.Contains(string(b), "cleaned") {
		t.Fatalf("the package did not get to clean up: %v", err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	eventually.Require(t, pollEvery, 10*time.Second, "the orphaned child to be killed", func() (bool, error) {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			return false, fmt.Errorf("pid %d still exists (%v)", pid, err)
		}
		return true, nil
	})
}

func TestExecCommand_exitCodeAndMissingBinary(t *testing.T) {
	exit, err := ExecCommand(context.Background(), Command{Args: []string{"/bin/sh", "-c", "exit 4"}})
	if err != nil || exit != 4 {
		t.Fatalf("exit %d err %v", exit, err)
	}
	if _, err := ExecCommand(context.Background(), Command{Args: []string{filepath.Join(t.TempDir(), "none")}}); err == nil {
		t.Fatal("a missing binary was not an error")
	}
}

// cleanupScript stands in for a package whose test overruns: it records the
// interrupt (its cleanup) and exits.
const cleanupScript = `#!/bin/sh
trap 'echo cleaned > "$1"; exit 2' INT
sleep 300 &
wait
`

// overrunBudget leaves the shell ample time to install its trap before the
// budget interrupts it, even on a loaded machine (the SIGINT of a shell that
// has not reached its trap yet kills it instead).
const overrunBudget = 5 * time.Second

func TestExecCommand_overrunBudgetInterruptsAndCleanupRuns(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-go")
	if err := os.WriteFile(script, []byte(cleanupScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cleaned := filepath.Join(dir, "cleaned")
	exit, err := execWithGrace(context.Background(), Command{Args: []string{script, cleaned}, Budget: overrunBudget}, 2*overrunBudget)
	if exit != 2 {
		t.Fatalf("exit %d, want the trap's 2 (%v)", exit, err)
	}
	if err == nil || !strings.Contains(err.Error(), "overran its stage timeout") {
		t.Fatalf("an overrun was not reported: %v", err)
	}
	if b, rerr := os.ReadFile(cleaned); rerr != nil || !strings.Contains(string(b), "cleaned") {
		t.Fatalf("the overrunning package did not get SIGINT and clean up: %v", rerr)
	}
}

func TestExecCommand_withinBudgetIsNotAnOverrun(t *testing.T) {
	exit, err := ExecCommand(context.Background(), Command{Args: []string{"/bin/sh", "-c", "exit 0"}, Budget: time.Minute})
	if err != nil || exit != 0 {
		t.Fatalf("exit %d err %v", exit, err)
	}
}
