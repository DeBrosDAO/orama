package stages

import (
	"fmt"
	"time"
)

// SuspendTolerance is how far a package's wall-clock time may run ahead of its
// monotonic time before the runner treats the host as having slept. NTP slews
// the wall clock by milliseconds; anything this large is a suspend.
const SuspendTolerance = 30 * time.Second

// hostSuspended is how long the host running the suite was suspended between
// start and end, both read by time.Now in this process. The monotonic clock
// Go reads stops while the host sleeps (CLOCK_UPTIME_RAW on macOS,
// CLOCK_MONOTONIC on Linux) and the wall clock does not, so the difference is
// the time spent asleep. Times without a monotonic reading give 0.
func hostSuspended(start, end time.Time) time.Duration {
	// Round(0) strips the monotonic reading, so a time it leaves equal had none.
	if start == start.Round(0) || end == end.Round(0) {
		return 0
	}
	return end.Round(0).Sub(start.Round(0)) - end.Sub(start)
}

// suspendedError is the package error for a run during which the host slept,
// empty when it stayed awake.
func suspendedError(slept time.Duration) string {
	if slept < SuspendTolerance {
		return ""
	}
	return fmt.Sprintf("the host running the suite was asleep for %s while this package ran: connections, deadlines "+
		"and timings in its tests broke on this machine, so its result is not a verdict on the fleet; "+
		"rerun it on a host that stays awake", slept.Round(time.Second))
}
