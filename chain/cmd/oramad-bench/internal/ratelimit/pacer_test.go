package ratelimit

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// fakeClock advances only when Sleep is called, so a test of the pacing needs no real time.
type fakeClock struct {
	now    time.Time
	slept  time.Duration
	sleeps int
}

func (f *fakeClock) Now() time.Time { return f.now }

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.now = f.now.Add(d)
	f.slept += d
	f.sleeps++
	return nil
}

func newFake() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func TestPacer_slotsAreEvenlySpaced(t *testing.T) {
	clock := newFake()
	p, err := New(100, clock)
	if err != nil {
		t.Fatal(err)
	}
	start := clock.Now()
	for i := 0; i < 101; i++ {
		if err := p.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := clock.Now().Sub(start); got != time.Second {
		t.Fatalf("101 slots at 100/s took %s, want exactly 1s (the first slot is immediate)", got)
	}
	if p.Taken() != 101 {
		t.Fatalf("taken %d", p.Taken())
	}
}

func TestPacer_catchesUpInsteadOfLoweringTheRate(t *testing.T) {
	clock := newFake()
	p, err := New(10, clock)
	if err != nil {
		t.Fatal(err)
	}
	// The sender stalls for 5s (a slow chain): slots 0 to 50 are now due or past due.
	clock.now = clock.now.Add(5 * time.Second)
	sleepsBefore := clock.sleeps
	for i := 0; i < 51; i++ {
		if err := p.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if clock.sleeps != sleepsBefore {
		t.Fatalf("a sender behind schedule slept %d times; it must send at once", clock.sleeps-sleepsBefore)
	}
	if err := p.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if clock.sleeps != sleepsBefore+1 {
		t.Fatal("after catching up the pacer did not wait for the next slot")
	}
}

func TestNew_rejectsBadRates(t *testing.T) {
	for _, rate := range []float64{0, -5, math.NaN(), math.Inf(1), 2e9} {
		if _, err := New(rate, newFake()); !errors.Is(err, ErrBadRate) {
			t.Errorf("rate %v: %v", rate, err)
		}
	}
}

func TestPacer_waitStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := newFake()
	p, err := New(1, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := p.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	// A slot already past due is not handed out once the context is done either.
	clock.now = clock.now.Add(time.Hour)
	if err := p.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("past-due slot after cancel: %v", err)
	}
}

func TestPacer_dueIsStartPlusMultiples(t *testing.T) {
	clock := newFake()
	p, err := New(4, clock)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Due(4).Sub(p.Due(0)); got != time.Second {
		t.Fatalf("4 slots at 4/s span %s", got)
	}
}

func TestSlotsIn(t *testing.T) {
	cases := []struct {
		rate float64
		d    time.Duration
		want int
	}{
		{10, 3 * time.Second, 30}, {2.5, 3 * time.Second, 8}, {1000, 0, 0}, {0.1, 5 * time.Second, 1},
	}
	for _, c := range cases {
		got, err := SlotsIn(c.rate, c.d)
		if err != nil || got != c.want {
			t.Errorf("SlotsIn(%v, %s) = %d, %v; want %d", c.rate, c.d, got, err, c.want)
		}
	}
	if _, err := SlotsIn(0, time.Second); !errors.Is(err, ErrBadRate) {
		t.Fatalf("zero rate: %v", err)
	}
	if _, err := SlotsIn(1, -time.Second); err == nil {
		t.Fatal("negative duration accepted")
	}
}
