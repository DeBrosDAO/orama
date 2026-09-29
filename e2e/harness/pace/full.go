package pace

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// WaitFull blocks until it holds a whole burst of bucket for host and keeps
// it: later paced callers wait for tokens to come back at the budget's rate.
// It drains the bucket as it refills (fractions included), so no other paced
// caller spends from it meanwhile and a busy run cannot starve it; it
// returns once a burst's worth has come back, which takes at most one refill
// period.
//
// The pacer's budgets sit under the product's (a slower rate, a smaller
// burst), so once a whole pacer burst has been collected with nothing paced
// spent in between, the product's bucket for this address has refilled too.
// A rate-limiter test calls it before it floods (the first 429 it sees is its
// own) and from a cleanup after (the next paced request of the run finds a
// refilled product bucket, not a *gw.PacingError). When ctx ends first the
// tokens collected so far are given back and ctx's error (wrapped) is
// returned. A nil *Pacer returns at once.
func (p *Pacer) WaitFull(ctx context.Context, host, bucket string) error {
	if p == nil {
		return nil
	}
	b, err := p.budgetFor(bucket)
	if err != nil {
		return err
	}
	key := stateKey(host, bucket)
	held := 0.0
	for {
		delay, err := p.collect(key, b, &held)
		if err != nil || delay == 0 {
			return err
		}
		if err := p.sleep(ctx, delay); err != nil {
			werr := fmt.Errorf("waiting %s for the %s bucket of %s to refill: %w", delay, bucket, host, err)
			return errors.Join(werr, p.giveBack(key, b, held))
		}
	}
}

// collect moves what key's bucket holds into held. Once held reaches the
// burst it returns what is over it to the bucket and 0; otherwise how long
// until the rest will be there.
func (p *Pacer) collect(key string, b Budget, held *float64) (time.Duration, error) {
	var delay time.Duration
	err := p.update(func(st fileState, now time.Time) {
		cur := refill(st[key], b, now)
		*held += max(cur.Tokens, 0)
		cur.Tokens = min(cur.Tokens, 0)
		if *held >= float64(b.Burst) {
			cur.Tokens += *held - float64(b.Burst)
		} else {
			delay = untilHeld(*held, b)
		}
		st[key] = cur
		prune(st, p.budgets, now, key)
	})
	return delay, err
}

// giveBack returns tokens held by a WaitFull that gave up.
func (p *Pacer) giveBack(key string, b Budget, held float64) error {
	return p.update(func(st fileState, now time.Time) {
		cur := refill(st[key], b, now)
		cur.Tokens = math.Min(float64(b.Burst), cur.Tokens+held)
		st[key] = cur
	})
}

// untilHeld is how long until held plus the refill makes a whole burst.
func untilHeld(held float64, b Budget) time.Duration {
	d := time.Duration(math.Ceil((float64(b.Burst) - held) / perNano(b)))
	return max(d, minDelay)
}
