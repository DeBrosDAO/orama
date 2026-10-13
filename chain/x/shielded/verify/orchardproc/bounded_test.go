package orchardproc

import (
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
)

func TestTimedMutex(t *testing.T) {
	m := newTimedMutex()
	if !m.TryLock() {
		t.Fatal("a free mutex was not taken")
	}
	if m.TryLock() {
		t.Fatal("a held mutex was taken")
	}
	start := time.Now()
	if m.LockWithin(30 * time.Millisecond) {
		t.Fatal("a held mutex was taken within the wait")
	}
	if took := time.Since(start); took < 25*time.Millisecond || took > time.Second {
		t.Fatalf("waited %s, want about 30ms", took)
	}
	go func() { time.Sleep(20 * time.Millisecond); m.Unlock() }()
	if !m.LockWithin(2 * time.Second) {
		t.Fatal("a mutex released inside the wait was not taken")
	}
	m.Unlock()
	if m.LockWithin(0) != true {
		t.Fatal("a free mutex is taken with no wait")
	}
}

// A caller whose deadline falls before the child answers is answered ErrBusy at the deadline and the
// request is left to run: the child is not killed for the caller's deadline, it stays the process
// that is alive and ready, and only its own request timeout ends a request for good.
func TestVerifyRunningWithin_aCallersDeadlineNeverKillsAHealthyChild(t *testing.T) {
	f := newFixture(t, time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	child := f.v.proc
	f.v.mu.Unlock()
	f.mode(modeHang)
	start := time.Now()
	err := f.v.VerifyRunningWithin(fakeBundle(), nil, 400*time.Millisecond)
	if !errors.Is(err, ErrBusy) || errors.Is(err, verify.ErrVerifierFault) {
		t.Fatalf("got %v, want ErrBusy at the caller's deadline and no fault", err)
	}
	if took := time.Since(start); took < 350*time.Millisecond || took > 900*time.Millisecond {
		t.Fatalf("answered after %s, want about 400ms", took)
	}
	if child.exited() {
		t.Fatal("the child was killed for the caller's deadline")
	}
	// While the abandoned request runs the verifier is busy, not gone.
	if err := f.v.VerifyRunningWithin(fakeBundle(), nil, 300*time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("while the request runs: got %v, want ErrBusy", err)
	}
	// The child's own request timeout (1 s) ends it: that, and only that, is a fault.
	deadline := time.Now().Add(5 * time.Second)
	for !child.exited() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !child.exited() {
		t.Fatal("the request timeout did not end the hung request")
	}
}

// A deadline nearer than the floor is answered at once and the child is not asked, killed or marked
// anything: it is alive and answers the next request.
func TestVerifyRunningWithin_aDeadlineUnderTheFloorAsksNothing(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	child := f.v.proc
	f.v.mu.Unlock()
	f.mode(modeHang) // if it were asked, it would hang
	start := time.Now()
	if err := f.v.VerifyRunningWithin(fakeBundle(), nil, minRequest-time.Millisecond); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("a deadline under the floor was waited for")
	}
	f.mode(modeAccept)
	if child.exited() {
		t.Fatal("the child was killed")
	}
	if err := f.v.VerifyRunning(fakeBundle(), nil); err != nil {
		t.Fatalf("the same child answers the next request: %v", err)
	}
	f.v.mu.Lock()
	same := f.v.proc == child
	f.v.mu.Unlock()
	if !same {
		t.Fatal("the child was replaced")
	}
}

// The wait for a busy verifier is cut to the deadline too.
func TestVerifyRunningWithin_theWaitIsCutToTheDeadline(t *testing.T) {
	f := newFixture(t, 30*time.Second)
	if err := f.v.Warm(); err != nil {
		t.Fatal(err)
	}
	f.v.mu.Lock()
	defer f.v.mu.Unlock()
	start := time.Now()
	err := f.v.VerifyRunningWithin(fakeBundle(), nil, 40*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v, want ErrBusy", err)
	}
	if took := time.Since(start); took > DefaultBusyWait-50*time.Millisecond {
		t.Fatalf("waited %s, want about 40ms and not the %s busy wait", took, DefaultBusyWait)
	}
}
