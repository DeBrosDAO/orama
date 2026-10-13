package watch

import (
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func TestLedger_includedTransactionHasItsLatency(t *testing.T) {
	l := NewLedger()
	l.Register("A")
	l.Sent("A", t0)
	if !l.Included("A", t0.Add(2500*time.Millisecond), 7, 0, "") {
		t.Fatal("the ledger did not know a registered transaction")
	}
	r := l.Snapshot()["A"]
	if r.Outcome != Included || r.Latency() != 2500*time.Millisecond || r.Height != 7 {
		t.Fatalf("record %+v", r)
	}
	if l.Pending() != 0 || l.Done() != 1 {
		t.Fatalf("pending %d done %d", l.Pending(), l.Done())
	}
}

func TestLedger_failedAndRejectedAreDistinct(t *testing.T) {
	l := NewLedger()
	for _, h := range []string{"F", "R", "P"} {
		l.Register(h)
		l.Sent(h, t0)
	}
	l.Included("F", t0.Add(time.Second), 3, 11, "out of gas")
	l.Rejected("R", t0.Add(time.Millisecond), 19, "insufficient fee")
	snap := l.Snapshot()
	if snap["F"].Outcome != FailedInBlock || snap["F"].Code != 11 || snap["F"].Latency() != time.Second {
		t.Fatalf("failed record %+v", snap["F"])
	}
	if snap["R"].Outcome != Rejected || snap["R"].Latency() != 0 || snap["R"].Log != "insufficient fee" {
		t.Fatalf("rejected record %+v", snap["R"])
	}
	if snap["P"].Outcome != Pending || l.Pending() != 1 || l.Done() != 2 {
		t.Fatalf("pending %d done %d", l.Pending(), l.Done())
	}
}

func TestLedger_notSentIsItsOwnOutcome(t *testing.T) {
	l := NewLedger()
	l.Register("A")
	l.NotSent("A", t0, "held back")
	r := l.Snapshot()["A"]
	if r.Outcome != NotSent || r.Latency() != 0 || l.Pending() != 0 {
		t.Fatalf("record %+v, pending %d", r, l.Pending())
	}
}

func TestLedger_unknownAndRepeatedResolutionsChangeNothing(t *testing.T) {
	l := NewLedger()
	if l.Included("ghost", t0, 1, 0, "") {
		t.Fatal("a transaction the ledger never saw was reported as known")
	}
	l.Register("A")
	l.Register("A") // registering twice must not count it twice
	l.Sent("A", t0)
	l.Sent("A", t0.Add(time.Hour)) // the first stamp stands
	l.Included("A", t0.Add(time.Second), 1, 0, "")
	l.Included("A", t0.Add(time.Hour), 2, 5, "again") // a transaction is included once
	r := l.Snapshot()["A"]
	if r.Outcome != Included || r.Height != 1 || r.Latency() != time.Second {
		t.Fatalf("record changed after resolution: %+v", r)
	}
	if l.Done() != 1 || l.Pending() != 0 {
		t.Fatalf("done %d pending %d", l.Done(), l.Pending())
	}
}

func TestLedger_resolutionWakesAWaiter(t *testing.T) {
	l := NewLedger()
	l.Register("A")
	l.Sent("A", t0)
	l.Included("A", t0, 1, 0, "")
	select {
	case <-l.Resolved():
	default:
		t.Fatal("no wake-up after a resolution")
	}
	// Two resolutions before a reader looks coalesce into one wake-up.
	l.Register("B")
	l.Register("C")
	l.Included("B", t0, 1, 0, "")
	l.Included("C", t0, 1, 0, "")
	<-l.Resolved()
	select {
	case <-l.Resolved():
		t.Fatal("wake-ups did not coalesce")
	default:
	}
}

func TestOutcomeString(t *testing.T) {
	want := map[Outcome]string{Pending: "pending", Included: "included", FailedInBlock: "failed-in-block", Rejected: "rejected", NotSent: "not-sent"}
	for o, s := range want {
		if o.String() != s {
			t.Errorf("%d = %q, want %q", o, o.String(), s)
		}
	}
}
