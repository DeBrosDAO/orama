// Package eventually waits for a condition by polling a readiness signal with a
// timeout. It is the only way a feature test waits: fixed sleeps are banned by
// the lint, because a sleep long enough to be reliable is too long for the
// fast case and still too short for the slow one.
package eventually

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

// errNotMet is the observation recorded when a probe says "not yet" without
// saying why.
var errNotMet = errors.New("condition not met yet")

// stopError marks an error that ends polling at once.
type stopError struct{ err error }

func (s stopError) Error() string { return s.err.Error() }
func (s stopError) Unwrap() error { return s.err }

// Stop wraps err so Poll returns it immediately instead of polling on. Use it
// for failures no amount of waiting fixes: a 403, a malformed response.
func Stop(err error) error {
	if err == nil {
		return nil
	}
	return stopError{err: err}
}

// TimeoutError is returned when the condition did not hold in time. Last is the
// final observation, so the failure says what the system looked like, not just
// that it was slow.
type TimeoutError struct {
	Description string
	Timeout     time.Duration
	Attempts    int
	Last        error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s (%d attempts) waiting for %s; last observed: %v",
		e.Timeout, e.Attempts, e.Description, e.Last)
}

func (e *TimeoutError) Unwrap() error { return e.Last }

// Poll calls fn immediately and then every interval until fn reports done,
// fn returns a Stop error, ctx ends, or timeout elapses. A plain error from fn
// is an observation ("status=provisioning"), kept as the last observed state
// and reported on timeout.
func Poll(ctx context.Context, interval, timeout time.Duration, description string, fn func() (bool, error)) error {
	if interval <= 0 || timeout <= 0 {
		return fmt.Errorf("invalid poll for %s: interval %s and timeout %s must be positive", description, interval, timeout)
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var last error = errNotMet
	for attempts := 1; ; attempts++ {
		done, err := fn()
		var stop stopError
		switch {
		case errors.As(err, &stop):
			return fmt.Errorf("gave up waiting for %s: %w", description, stop.err)
		case done && err == nil:
			return nil
		case err != nil:
			last = err
		default:
			last = errNotMet
		}
		select {
		case <-deadline.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("stopped waiting for %s: %w (last observed: %v)", description, ctx.Err(), last)
			}
			return &TimeoutError{Description: description, Timeout: timeout, Attempts: attempts, Last: last}
		case <-ticker.C:
		}
	}
}

// Require polls and fails the test at once when the condition does not hold.
// It stops when the test's context or the run-wide context (runctx: the
// package was interrupted) ends.
func Require(t testing.TB, interval, timeout time.Duration, description string, fn func() (bool, error)) {
	t.Helper()
	ctx, release := runctx.With(t.Context())
	defer release()
	if err := Poll(ctx, interval, timeout, description, fn); err != nil {
		t.Fatal(err)
	}
}

// Eventually polls and records a failure without stopping the test, returning
// whether the condition held. It stops as Require does.
func Eventually(t testing.TB, interval, timeout time.Duration, description string, fn func() (bool, error)) bool {
	t.Helper()
	ctx, release := runctx.With(t.Context())
	defer release()
	if err := Poll(ctx, interval, timeout, description, fn); err != nil {
		t.Error(err)
		return false
	}
	return true
}
