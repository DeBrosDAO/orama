//go:build e2e_fleet

package edge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
)

// holdSlack is how long past the hold window the poll may run: one slow
// observation must not turn a held condition into a timeout.
const holdSlack = time.Minute

// Hold fails the test unless cond is true at every observation, every
// interval, for the whole window d: "this must not happen for two minutes".
// It is the negative of eventually.Require and waits the same way (polling,
// never sleeping). An error from cond is a failure, not an observation.
func Hold(t testing.TB, interval, d time.Duration, what string, cond func() (bool, error)) {
	t.Helper()
	start := time.Now()
	err := eventually.Poll(t.Context(), interval, d+holdSlack, what, func() (bool, error) {
		ok, err := cond()
		if err != nil {
			return false, eventually.Stop(fmt.Errorf("%s: observation failed after %s: %w", what, time.Since(start).Round(time.Second), err))
		}
		if !ok {
			return false, eventually.Stop(fmt.Errorf("%s: broke after %s", what, time.Since(start).Round(time.Second)))
		}
		return time.Since(start) >= d, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// credBurstRefill is how long the product's credential bucket (30 a minute,
// burst 10) takes to refill from empty: 10 tokens at half a token a second
// (core/pkg/gateway/gateway.go configureRateLimiters).
const credBurstRefill = 20 * time.Second

// Quiesce blocks until the run's shared credential pacer bucket is full and
// holds its whole burst (pace.(*Pacer).WaitFull): nothing paced has spent this
// address's product budget for a refill period, so the product bucket of every
// gateway is full again. A rate-limiter test calls it before
// it floods (so the first 429 it sees is its own) and again in a cleanup
// after (so the next paced request of the run finds a refilled bucket
// instead of a *gw.PacingError).
func Quiesce(ctx context.Context) error {
	p, err := pace.FromEnv(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("the run's pacer: %w", err)
	}
	if p == nil {
		return fmt.Errorf("no run pacer: a rate-limiter test needs the fleet run's pacing state")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*credBurstRefill)
	defer cancel()
	if err := p.WaitFull(ctx, pace.BucketCred); err != nil {
		return fmt.Errorf("waiting for the run's credential budget to be free: %w", err)
	}
	return nil
}
