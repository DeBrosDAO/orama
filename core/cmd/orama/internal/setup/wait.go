package setup

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Timing is how long run.go waits for each thing, and how often it asks. Every
// wait polls the real readiness indicator and ends at its deadline; none sleeps.
type Timing struct {
	// SyncPoll and SyncDeadline: a joining chain node restoring a snapshot.
	SyncPoll, SyncDeadline time.Duration
	// RestartBudget: a cluster node coming back after `orama node restart`.
	RestartBudget time.Duration
	// ReadyBudget: a freshly installed cluster node carrying its share.
	ReadyBudget time.Duration
	// DNSPoll and DNSDeadline: the parent zone returning the NS and glue records.
	DNSPoll, DNSDeadline time.Duration
}

// DefaultTiming is what a run on real machines waits.
func DefaultTiming() Timing {
	return Timing{
		SyncPoll: 10 * time.Second, SyncDeadline: 45 * time.Minute,
		RestartBudget: 10 * time.Minute, ReadyBudget: 10 * time.Minute,
		DNSPoll: 30 * time.Second, DNSDeadline: 60 * time.Minute,
	}
}

// pollUntil calls check every interval until it reports done, fails, or the
// deadline passes. The first call is immediate. A check that returns an error is
// a failure, not another try: a condition that may still come true is
// (false, nil).
func pollUntil(ctx context.Context, interval, deadline time.Duration, what string, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		done, err := check(ctx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("gave up waiting for %s after %s", what, deadline)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
