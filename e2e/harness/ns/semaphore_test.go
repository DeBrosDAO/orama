package ns

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Child process protocol: a child reserves one slot of childCap in
// childDir, creates ready-<id>, waits for a line on stdin, removes its file
// and exits (releasing the slot with its descriptors).
const (
	childEnv   = "E2E_NS_SEM_CHILD"
	childDir   = "E2E_NS_SEM_DIR"
	childCap   = "E2E_NS_SEM_CAP"
	childID    = "E2E_NS_SEM_ID"
	waitBudget = 30 * time.Second
	pollEvery  = 10 * time.Millisecond
)

func TestReserve_childProcess(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		return // only meaningful as a child of the tests below
	}
	var capacity int
	fmt.Sscan(os.Getenv(childCap), &capacity)
	dir := os.Getenv(childDir)
	s, err := Reserve(context.Background(), dir, 1, capacity)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "ready-"+os.Getenv(childID))
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	if err := os.Remove(ready); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(); err != nil {
		t.Fatal(err)
	}
}

type child struct {
	cmd   *exec.Cmd
	stdin interface{ Close() error }
}

func startChild(t *testing.T, dir string, capacity, id int) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReserve_childProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), childEnv+"=1", childDir+"="+dir, fmt.Sprint(childCap, "=", capacity), fmt.Sprint(childID, "=", id))
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &child{cmd: cmd, stdin: in}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return c
}

func readyCount(t *testing.T, dir string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "ready-*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

// waitReady polls until n children are ready, failing if more ever are.
func waitReady(t *testing.T, dir string, n, capacity int) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for {
		got := readyCount(t, dir)
		if got > capacity {
			t.Fatalf("%d processes hold a slot at once, the cap is %d", got, capacity)
		}
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d children ready after %s", got, n, waitBudget)
		}
		time.Sleep(pollEvery)
	}
}

func TestReserve_crossProcessCapHolds(t *testing.T) {
	const capacity, total = 2, 5
	dir := t.TempDir()
	var children []*child
	for i := range total {
		children = append(children, startChild(t, dir, capacity, i))
	}
	waitReady(t, dir, capacity, capacity)
	if s, err := tryReserve(filepath.Join(dir, slotDirName), 1, capacity); err != nil || s != nil {
		t.Fatalf("a slot was free while %d children held the cap: %v %v", capacity, s, err)
	}
	for released := 0; released < total; released++ {
		for _, c := range children {
			if c.cmd.ProcessState == nil && readyFile(dir, c) {
				c.stdin.Close()
				if err := c.cmd.Wait(); err != nil {
					t.Fatalf("child failed: %v", err)
				}
				break
			}
		}
		waitReady(t, dir, min(capacity, total-released-1), capacity)
	}
}

func readyFile(dir string, c *child) bool {
	for _, kv := range c.cmd.Env {
		var id int
		if n, _ := fmt.Sscanf(kv, childID+"=%d", &id); n == 1 {
			_, err := os.Stat(filepath.Join(dir, fmt.Sprint("ready-", id)))
			return err == nil
		}
	}
	return false
}

func TestReserve_killedProcessReleasesItsSlot(t *testing.T) {
	dir := t.TempDir()
	c := startChild(t, dir, 1, 0)
	waitReady(t, dir, 1, 1)
	if err := c.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.cmd.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), waitBudget)
	defer cancel()
	s, err := Reserve(ctx, dir, 1, 1)
	if err != nil {
		t.Fatalf("the killed child's slot was not released: %v", err)
	}
	if err := s.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestReserve_boundsAndCancel(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{0, -1, 4} {
		if _, err := Reserve(context.Background(), dir, n, 3); err == nil {
			t.Errorf("count %d of 3 accepted", n)
		}
	}
	all, err := Reserve(context.Background(), dir, 3, 3)
	if err != nil || all.Held() != 3 {
		t.Fatalf("held %v err %v", all, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Reserve(ctx, dir, 1, 3); err == nil {
		t.Fatal("a slot was handed out beyond the cap")
	}
	if err := all.Release(); err != nil || all.Release() != nil {
		t.Fatalf("release: %v", err)
	}
	var none *Slots
	if none.Release() != nil {
		t.Fatal("nil release")
	}
}

func TestMaxLiveFromEnv_defaultAndOverride(t *testing.T) {
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if n, err := MaxLiveFromEnv(lookup, 3); err != nil || n != 16 {
		t.Fatalf("default for three nodes: %d %v", n, err)
	}
	if DefaultMaxLive(0) != 1 {
		t.Fatal("no nodes must still allow one")
	}
	env[EnvMaxLive] = "5"
	if n, err := MaxLiveFromEnv(lookup, 3); err != nil || n != 5 {
		t.Fatalf("override: %d %v", n, err)
	}
	for _, bad := range []string{"0", "-2", "x"} {
		env[EnvMaxLive] = bad
		if _, err := MaxLiveFromEnv(lookup, 3); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHold_newUsesHeldSlotsFirst(t *testing.T) {
	work := t.TempDir()
	t.Setenv("E2E_FLEET_STATE", filepath.Join(work, "state.json"))
	t.Setenv(EnvMaxLive, "3")
	f := fleet.NewWithDialer(&fleet.State{RunID: "abcd1234"}, nil, nil)
	t.Run("holder", func(t *testing.T) {
		Hold(t, f, 2)
		fleetSlot(t, f)
		fleetSlot(t, f)
		// Both came from the hold: one of the three slots is still free.
		s, err := tryReserve(filepath.Join(work, slotDirName), 1, 3)
		if err != nil || s == nil {
			t.Fatalf("New took slots beyond its hold: %v %v", s, err)
		}
		if err := s.Release(); err != nil {
			t.Fatal(err)
		}
	})
	s, err := tryReserve(filepath.Join(work, slotDirName), 3, 3)
	if err != nil || s == nil {
		t.Fatalf("the test's slots were not released at its end: %v", err)
	}
	_ = s.Release()
}

// TestNew_holdAndWaitFails: a test that holds a slot and asks for another
// without a Hold would wait while holding; it fails instead, and so does a
// Hold taken after a slot.
func TestNew_holdAndWaitFails(t *testing.T) {
	t.Setenv("E2E_FLEET_STATE", filepath.Join(t.TempDir(), "state.json"))
	t.Setenv(EnvMaxLive, "3")
	f := fleet.NewWithDialer(&fleet.State{RunID: "abcd1234"}, nil, nil)
	for name, second := range map[string]func(testing.TB){
		"new":  func(tb testing.TB) { fleetSlot(tb, f) },
		"hold": func(tb testing.TB) { Hold(tb, f, 1) },
	} {
		tb := &fatalTB{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			fleetSlot(tb, f)
			second(tb)
		}()
		<-done
		if !strings.Contains(tb.msg, "call ns.Hold") {
			t.Errorf("%s: the second slot was taken while holding one: %q", name, tb.msg)
		}
	}
}

// TestReserve_fifoNeverStarvesALargeRequest: with one slot taken, a waiter
// for all three queues first; a later waiter for one must not overtake it
// when the slot frees up.
func TestReserve_fifoNeverStarvesALargeRequest(t *testing.T) {
	old := slotPoll
	slotPoll = 20 * time.Millisecond
	t.Cleanup(func() { slotPoll = old })
	work := t.TempDir()
	first, err := Reserve(context.Background(), work, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	big := make(chan *Slots, 1)
	go func() { s, _ := Reserve(context.Background(), work, 3, 3); big <- s }()
	eventually.Require(t, pollEvery, waitBudget, "the large waiter to queue", func() (bool, error) {
		m, err := filepath.Glob(filepath.Join(work, slotDirName, ticketPrefix+"*"+ticketSuffix))
		return len(m) == 1, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if s, err := Reserve(ctx, work, 1, 3); err == nil {
		_ = s.Release()
		t.Fatal("a later single-slot waiter overtook the queued large one")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-big:
		if s.Held() != 3 {
			t.Fatalf("the large waiter got %d slots", s.Held())
		}
		_ = s.Release()
	case <-time.After(waitBudget):
		t.Fatal("the large waiter never got its slots")
	}
}

// tryReserve takes count free slots regardless of the queue (a probe).
func tryReserve(dir string, count, capacity int) (*Slots, error) {
	guard, err := lockFile(filepath.Join(dir, guardName), syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	return takeSlots(dir, count, capacity)
}
