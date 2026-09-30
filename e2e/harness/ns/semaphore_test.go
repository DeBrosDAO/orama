package ns

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestMaxLiveForTarget_stagenetHasItsOwnDefault(t *testing.T) {
	none := func(string) (string, bool) { return "", false }
	if n, err := MaxLiveForTarget(none, true, 3); err != nil || n != StagenetMaxLive {
		t.Fatalf("stagenet default = %d, %v; want %d", n, err, StagenetMaxLive)
	}
	if n, err := MaxLiveForTarget(none, false, 3); err != nil || n != DefaultMaxLive(3) {
		t.Fatalf("fleet default = %d, %v; want %d", n, err, DefaultMaxLive(3))
	}
	two := func(string) (string, bool) { return "2", true }
	if n, err := MaxLiveForTarget(two, true, 3); err != nil || n != 2 {
		t.Fatalf("override on stagenet = %d, %v; want 2", n, err)
	}
	bad := func(string) (string, bool) { return "0", true }
	if _, err := MaxLiveForTarget(bad, true, 3); err == nil {
		t.Fatal("a zero override was accepted")
	}
}
