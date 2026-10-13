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
	// SlotPoll and SlotDeadline: every machine of the run claiming its nameserver
	// slot, which a node does a moment after it joins.
	SlotPoll, SlotDeadline time.Duration
	// BalancePoll and BalanceDeadline: the node setup reads the chain through seeing the faucet's
	// drip, which the seed that made it saw in a block a moment before.
	BalancePoll, BalanceDeadline time.Duration
	// TorPoll asks a Tor role how it is doing. TorConsensusDeadline bounds each wait for
	// the network's Tor consensus: the first one comes at least 30 minutes after the
	// authorities start (tornet.AuthorityLearnWindow: they cast no Running vote before),
	// then up to one voting interval and its delays. TorRollDeadline bounds the wait to restart one authority:
	// the others' 30 minutes, then the consensus that lists it again.
	TorPoll, TorConsensusDeadline, TorRollDeadline time.Duration
}

// DefaultTiming is what a run on real machines waits.
func DefaultTiming() Timing {
	return Timing{
		SyncPoll: 10 * time.Second, SyncDeadline: 45 * time.Minute,
		RestartBudget: 10 * time.Minute, ReadyBudget: 10 * time.Minute,
		DNSPoll: 30 * time.Second, DNSDeadline: 60 * time.Minute,
		SlotPoll: 5 * time.Second, SlotDeadline: 3 * time.Minute,
		BalancePoll: 2 * time.Second, BalanceDeadline: 2 * time.Minute,
		TorPoll: time.Minute, TorConsensusDeadline: 2 * time.Hour, TorRollDeadline: 2 * time.Hour,
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
