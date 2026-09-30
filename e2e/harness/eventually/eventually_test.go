package eventually

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

const (
	fast = time.Millisecond
	long = 2 * time.Second
)

func TestPoll_succeedsAfterSomeAttempts(t *testing.T) {
	n := 0
	err := Poll(context.Background(), fast, long, "counter", func() (bool, error) {
		n++
		if n < 3 {
			return false, fmt.Errorf("n=%d", n)
		}
		return true, nil
	})
	if err != nil || n != 3 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestPoll_timeoutReportsLastObservation(t *testing.T) {
	err := Poll(context.Background(), fast, 20*time.Millisecond, "the thing", func() (bool, error) {
		return false, errors.New("status=provisioning")
	})
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("want TimeoutError, got %v", err)
	}
	if !strings.Contains(err.Error(), "status=provisioning") || !strings.Contains(err.Error(), "the thing") || te.Attempts < 1 {
		t.Fatalf("timeout error lacks the last state: %v", err)
	}
}

func TestPoll_notMetWithoutReason(t *testing.T) {
	err := Poll(context.Background(), fast, 10*time.Millisecond, "x", func() (bool, error) { return false, nil })
	if !errors.Is(err, errNotMet) {
		t.Fatalf("got %v", err)
	}
}

func TestPoll_stopEndsImmediately(t *testing.T) {
	n := 0
	sentinel := errors.New("403 forbidden")
	err := Poll(context.Background(), fast, long, "x", func() (bool, error) {
		n++
		return false, Stop(sentinel)
	})
	if n != 1 || !errors.Is(err, sentinel) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if Stop(nil) != nil {
		t.Fatal("Stop(nil) must be nil")
	}
}

func TestPoll_doneWithErrorIsNotDone(t *testing.T) {
	calls := 0
	err := Poll(context.Background(), fast, 10*time.Millisecond, "x", func() (bool, error) {
		calls++
		return true, errors.New("inconsistent")
	})
	if err == nil {
		t.Fatal("done with an error must not count as done")
	}
}

func TestPoll_parentContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Poll(ctx, fast, long, "x", func() (bool, error) { return false, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestPoll_invalidDurations(t *testing.T) {
	for _, c := range [][2]time.Duration{{0, long}, {fast, 0}, {-1, -1}} {
		if err := Poll(context.Background(), c[0], c[1], "x", func() (bool, error) { return true, nil }); err == nil {
			t.Errorf("interval=%s timeout=%s accepted", c[0], c[1])
		}
	}
}

func TestEventually_returnsResult(t *testing.T) {
	if !Eventually(t, fast, long, "immediate", func() (bool, error) { return true, nil }) {
		t.Fatal("Eventually returned false for a met condition")
	}
	Require(t, fast, long, "immediate", func() (bool, error) { return true, nil })
}

// errorTB records Errorf/Error instead of failing the real test.
type errorTB struct {
	testing.TB
	msg string
}

func (e *errorTB) Helper()           {}
func (e *errorTB) Error(args ...any) { e.msg = fmt.Sprint(args...) }

// TestEventually_stopsWhenTheRunIsInterrupted: an interrupted package
// (runctx cancelled) ends a long wait at once instead of after its timeout.
func TestEventually_stopsWhenTheRunIsInterrupted(t *testing.T) {
	runctx.Reset()
	t.Cleanup(runctx.Reset)
	runctx.Cancel()
	tb := &errorTB{TB: t}
	start := time.Now()
	if Eventually(tb, fast, time.Hour, "never", func() (bool, error) { return false, nil }) {
		t.Fatal("a condition that never holds was reported met")
	}
	if time.Since(start) > 10*time.Second || !strings.Contains(tb.msg, "stopped waiting") {
		t.Fatalf("the wait did not stop on the interrupt: %q after %s", tb.msg, time.Since(start))
	}
}
