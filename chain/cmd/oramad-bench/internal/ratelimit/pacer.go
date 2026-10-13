// Package ratelimit paces an open-loop load: the n-th transaction is due at start + n/rate, whatever
// became of the ones before it. A sender that falls behind sends at once until it has caught up,
// so the offered rate stays what the profile asked for and a slow chain cannot lower it (the
// coordinated-omission mistake of a loop that sleeps a fixed gap after every send).
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Clock is the time source a Pacer waits on. Tests give it a fake.
type Clock interface {
	Now() time.Time
	// Sleep blocks for d or until ctx is done, and returns ctx's error in that case.
	Sleep(ctx context.Context, d time.Duration) error
}

// SystemClock is the wall clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// Sleep waits for d or for ctx to end.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ErrBadRate is returned for a rate that is not a positive finite number of transactions a second.
var ErrBadRate = errors.New("rate must be a positive, finite number of tx/s")

// Pacer hands out send slots at a fixed rate. It is for one goroutine.
type Pacer struct {
	clock    Clock
	start    time.Time
	interval time.Duration
	taken    int64
}

// New returns a Pacer whose first slot is due now and whose slots follow every 1/rate seconds.
func New(rate float64, clock Clock) (*Pacer, error) {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return nil, fmt.Errorf("%w: %v", ErrBadRate, rate)
	}
	interval := time.Duration(float64(time.Second) / rate)
	if interval <= 0 {
		return nil, fmt.Errorf("%w: %v is faster than one per nanosecond", ErrBadRate, rate)
	}
	return &Pacer{clock: clock, start: clock.Now(), interval: interval}, nil
}

// Due is when slot n (counting from 0) is due.
func (p *Pacer) Due(n int64) time.Time {
	return p.start.Add(time.Duration(n) * p.interval)
}

// Wait blocks until the next slot is due and takes it. It returns at once when the slot is already
// past, which is how a sender catches up.
func (p *Pacer) Wait(ctx context.Context) error {
	due := p.Due(p.taken)
	if d := due.Sub(p.clock.Now()); d > 0 {
		if err := p.clock.Sleep(ctx, d); err != nil {
			return err
		}
	} else if err := ctx.Err(); err != nil {
		return err
	}
	p.taken++
	return nil
}

// Taken is how many slots Wait has handed out.
func (p *Pacer) Taken() int64 { return p.taken }

// SlotsIn is how many slots fall in the first d of a pacer started now: the transactions an
// open-loop step of that length offers.
func SlotsIn(rate float64, d time.Duration) (int, error) {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return 0, fmt.Errorf("%w: %v", ErrBadRate, rate)
	}
	if d < 0 {
		return 0, fmt.Errorf("duration %s is negative", d)
	}
	return int(math.Ceil(rate * d.Seconds())), nil
}
