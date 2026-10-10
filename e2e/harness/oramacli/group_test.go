package oramacli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// shortWaitDelay shortens cliWaitDelay for one test.
func shortWaitDelay(t *testing.T) {
	t.Helper()
	old := cliWaitDelay
	cliWaitDelay = 300 * time.Millisecond
	t.Cleanup(func() { cliWaitDelay = old })
}

// strayScript starts a grandchild that holds stdout (and stderr) open for a
// minute, records its pid, and exits at once.
const strayScript = "sleep 60 &\necho \"$!\" > \"$HOME/stray.pid\"\necho started\nexit 0\n"

func TestRun_grandchildHoldingStdoutNeverHangs(t *testing.T) {
	shortWaitDelay(t)
	r, _ := newRunner(t)
	writeBin(t, r, strayScript)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := r.Run(ctx, "stray")
	if ctx.Err() != nil {
		t.Fatal("Run hung on a grandchild holding stdout")
	}
	if err == nil || !strings.Contains(err.Error(), "kept its output open") || !strings.Contains(res.Stdout, "started") {
		t.Fatalf("res %+v err %v", res, err)
	}
	assertStrayKilled(t, r)
}

func TestStart_grandchildHoldingStdoutNeverHangs(t *testing.T) {
	shortWaitDelay(t)
	r, _ := newRunner(t)
	writeBin(t, r, strayScript)
	p, err := r.Start(context.Background(), "stray")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var res Result
	go func() { res, err = p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Wait hung on a grandchild holding stdout")
	}
	if err == nil || !strings.Contains(res.Stdout, "started") {
		t.Fatalf("res %+v err %v", res, err)
	}
	assertStrayKilled(t, r)
}

// TestKill_killsTheWholeGroup: Kill ends the CLI and what it started.
func TestKill_killsTheWholeGroup(t *testing.T) {
	shortWaitDelay(t)
	r, _ := newRunner(t)
	writeBin(t, r, "sleep 60 &\necho \"$!\" > \"$HOME/stray.pid\"\necho started\nwait\n")
	p, err := r.Start(context.Background(), "hold")
	if err != nil {
		t.Fatal(err)
	}
	if line := <-p.StdoutLines(); line != "started" {
		t.Fatalf("line %q", line)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Wait(); err != nil && strings.Contains(err.Error(), "kept its output open") {
		t.Fatalf("the killed group still held the output: %v", err)
	}
	assertStrayKilled(t, r)
}

// assertStrayKilled checks the grandchild recorded in $HOME/stray.pid is gone.
func assertStrayKilled(t *testing.T, r *Runner) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.Home, "stray.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	eventually.Require(t, 20*time.Millisecond, 5*time.Second, "the grandchild to die with its group", func() (bool, error) {
		return syscall.Kill(pid, 0) != nil, nil
	})
}
