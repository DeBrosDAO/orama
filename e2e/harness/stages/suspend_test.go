package stages

import (
	"strings"
	"testing"
	"time"
)

func TestHostSuspended_awakeHostIsZero(t *testing.T) {
	start := time.Now()
	if got := hostSuspended(start, start.Add(20*time.Minute)); got != 0 {
		t.Fatalf("an awake host (wall and monotonic advanced alike) slept %s", got)
	}
	if got := hostSuspended(start, time.Now()); got.Abs() >= SuspendTolerance {
		t.Fatalf("a host that never slept reports %s asleep", got)
	}
}

func TestHostSuspended_noMonotonicReadingIsZero(t *testing.T) {
	start := time.Now()
	if got := hostSuspended(start.UTC(), start.Add(time.Hour).UTC()); got != 0 {
		t.Fatalf("times without a monotonic reading reported %s asleep", got)
	}
	if got := hostSuspended(start, start.Round(0).Add(time.Hour)); got != 0 {
		t.Fatalf("an end without a monotonic reading reported %s asleep", got)
	}
}

func TestSuspendedError_belowToleranceIsAwake(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, SuspendTolerance - time.Millisecond} {
		if msg := suspendedError(d); msg != "" {
			t.Errorf("suspendedError(%s) = %q, want empty", d, msg)
		}
	}
}

func TestSuspendedError_sleepIsNotAVerdict(t *testing.T) {
	msg := suspendedError(14*time.Minute + 3*time.Second)
	if !strings.Contains(msg, "asleep for 14m3s") || !strings.Contains(msg, "not a verdict on the fleet") {
		t.Fatalf("suspendedError(14m3s) = %q, want the duration and that the result is not a verdict", msg)
	}
	if suspendedError(SuspendTolerance) == "" {
		t.Fatal("a sleep of exactly the tolerance was not reported")
	}
}
