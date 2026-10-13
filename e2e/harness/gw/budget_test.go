package gw

import (
	"testing"
	"time"
)

// A 20 MiB upload from a runner sharing its uplink took 29 s; the flat 30 s
// budget timed it out before the gateway had the body (stagenet 2026-10-04).
func TestRequestBodyAllowance_aLargeBodyGetsTimeToCrossASlowUplink(t *testing.T) {
	if got := RequestBudget + requestBodyAllowance(20<<20); got < 90*time.Second {
		t.Fatalf("a 20 MiB request has %s, want at least 90s", got)
	}
}

func TestRequestBodyAllowance_noBodyAddsNothing(t *testing.T) {
	for _, n := range []int64{0, -1} {
		if got := requestBodyAllowance(n); got != 0 {
			t.Errorf("length %d: allowance %s, want 0", n, got)
		}
	}
}

func TestRequestBodyAllowance_aSmallBodyStaysNearTheBudget(t *testing.T) {
	if got := requestBodyAllowance(4 << 10); got > time.Second {
		t.Fatalf("a 4 KiB body adds %s, want under a second", got)
	}
}
