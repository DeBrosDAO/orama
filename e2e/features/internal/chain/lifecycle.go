//go:build e2e_fleet

package chain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Waits after a validator has been stopped and started again.
const (
	// CatchUpBudget bounds a restarted validator's return to the head.
	CatchUpBudget = 5 * time.Minute
	// advanceBlocks is how many blocks every validator must commit past the
	// highest head seen once all of them answer.
	advanceBlocks = 2
)

// WaitAllAdvance is WaitAllAdvanceWithin the catch-up budget.
func (c *Chain) WaitAllAdvance(t testing.TB) error {
	t.Helper()
	return c.WaitAllAdvanceWithin(t, CatchUpBudget)
}

// WaitAllAdvanceWithin waits up to budget, on a context of its own (it runs
// from cleanups too), until every validator answers, is not catching up, and
// has committed advanceBlocks blocks past the highest head seen when all of
// them first answered: the chain moves again on all of them.
func (c *Chain) WaitAllAdvanceWithin(t testing.TB, budget time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	target := int64(0)
	return eventually.Poll(ctx, PollEvery, budget, "every validator to commit new blocks", func() (bool, error) {
		heights := map[string]int64{}
		for _, n := range c.Nodes() {
			s, err := c.NodeStatus(t, n)
			if err != nil {
				return false, err
			}
			if s.CatchingUp {
				return false, fmt.Errorf("%s is catching up at %d", n.Name, s.Height)
			}
			heights[n.Name] = s.Height
		}
		if target == 0 {
			for _, h := range heights {
				target = max(target, h+advanceBlocks)
			}
		}
		for name, h := range heights {
			if h < target {
				return false, fmt.Errorf("%s is at %d, want %d", name, h, target)
			}
		}
		return true, nil
	})
}

// AdvanceAtCleanup registers a cleanup that waits until the chain moves on
// every validator. Register it BEFORE stopping a validator: cleanups run last
// in first out, so it runs after the cleanup that starts the validator again,
// and the next package starts on a chain that moves even when the test failed
// before its own check.
func (c *Chain) AdvanceAtCleanup(t testing.TB) { c.AdvanceAtCleanupWithin(t, CatchUpBudget) }

// AdvanceAtCleanupWithin is AdvanceAtCleanup with a budget of its own: a
// validator healed after a long partition takes longer than CatchUpBudget to
// rejoin.
func (c *Chain) AdvanceAtCleanupWithin(t testing.TB, budget time.Duration) {
	t.Helper()
	t.Cleanup(func() {
		if err := c.WaitAllAdvanceWithin(t, budget); err != nil {
			t.Errorf("cleanup: the chain did not advance on every validator: %v", err)
		}
	})
}

// TryRun runs cmd on n under budget and returns the error instead of failing
// the test: for cleanups, which run after the test's context is cancelled and
// must report a failure without stopping the cleanups that follow.
func (c *Chain) TryRun(t testing.TB, n fleet.Node, budget time.Duration, cmd string) (fleet.Output, error) {
	t.Helper()
	return c.run(t, n, budget, cmd)
}

// MustRun is Run that also fails the test on a non-zero exit.
func (c *Chain) MustRun(t testing.TB, n fleet.Node, budget time.Duration, cmd string) fleet.Output {
	t.Helper()
	out := c.Run(t, n, budget, cmd)
	if out.Exit != 0 {
		t.Fatal(c.F.Redact(fmt.Sprintf("%s: %q exited %d\nstdout: %s\nstderr: %s", n.Name, cmd, out.Exit, out.Stdout, out.Stderr)))
	}
	return out
}
