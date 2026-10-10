package sfu

import (
	"sync/atomic"
	"testing"
)

type countingSink struct{ writes atomic.Int64 }

func (c *countingSink) Write(b []byte) (int, error) { c.writes.Add(1); return len(b), nil }

func TestForwardRTP_dropsWhileMutedAndResumesAfter(t *testing.T) {
	var muted atomic.Bool
	sink := &countingSink{}

	muted.Store(true)
	forwardRTP(&scriptedSource{n: 5}, sink, testLogger(), muted.Load)
	if got := sink.writes.Load(); got != 0 {
		t.Fatalf("a muted publisher's packets reached the subscribers: %d writes", got)
	}

	muted.Store(false)
	forwardRTP(&scriptedSource{n: 5}, sink, testLogger(), muted.Load)
	if got := sink.writes.Load(); got != 5 {
		t.Fatalf("writes after unmuting = %d, want 5", got)
	}
}

func TestForwardRTP_nilDropForwardsEverything(t *testing.T) {
	sink := &countingSink{}
	forwardRTP(&scriptedSource{n: 3}, sink, testLogger(), nil)
	if got := sink.writes.Load(); got != 3 {
		t.Fatalf("writes = %d, want 3", got)
	}
}
