package ns

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

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
