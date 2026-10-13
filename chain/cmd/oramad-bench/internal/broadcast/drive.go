package broadcast

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ratelimit"
)

// closedPoll bounds how long a closed-loop sender waits for a completion before it looks again, in
// case a wake-up was coalesced away.
const closedPoll = 20 * time.Millisecond

// Open offers jobs at a fixed rate for at most dur, in order, and returns how many it offered. The
// n-th job is due n/rate seconds after the start whatever happened to the earlier ones (see
// ratelimit). It stops early when it runs out of jobs.
func (d *Dispatcher) Open(ctx context.Context, jobs []Job, rate float64, dur time.Duration, clock ratelimit.Clock) (int, error) {
	pacer, err := ratelimit.New(rate, clock)
	if err != nil {
		return 0, err
	}
	end := clock.Now().Add(dur)
	offered := 0
	for _, j := range jobs {
		if !pacer.Due(int64(offered)).Before(end) {
			break
		}
		if err := pacer.Wait(ctx); err != nil {
			return offered, err
		}
		if err := d.Enqueue(ctx, j, pacer.Due(int64(offered))); err != nil {
			return offered, fmt.Errorf("enqueue job %d: %w", offered, err)
		}
		offered++
	}
	return offered, nil
}

// Closed keeps window jobs in flight for at most dur: it offers the next job as soon as one that
// is in flight has an outcome (was included, or refused). It returns how many it offered.
func (d *Dispatcher) Closed(ctx context.Context, jobs []Job, window int, dur time.Duration, clock ratelimit.Clock) (int, error) {
	if window < 1 {
		return 0, fmt.Errorf("closed-loop window %d must be at least 1", window)
	}
	end := clock.Now().Add(dur)
	offered := 0
	base := d.ledger.Done()
	for _, j := range jobs {
		for offered-(d.ledger.Done()-base) >= window {
			if !clock.Now().Before(end) {
				return offered, nil
			}
			select {
			case <-ctx.Done():
				return offered, ctx.Err()
			case <-d.ledger.Resolved():
			case <-time.After(closedPoll):
			}
		}
		if !clock.Now().Before(end) {
			return offered, nil
		}
		if err := d.Enqueue(ctx, j, clock.Now()); err != nil {
			return offered, fmt.Errorf("enqueue job %d: %w", offered, err)
		}
		offered++
	}
	return offered, nil
}
