// Package runctx is a feature package's run-wide context: cancelled once
// the package is interrupted (harness watches SIGINT and SIGTERM), so the
// harness's waits in a running test (eventually.Require and Eventually,
// namespace readiness, extra servers) stop at once and the test goes on to
// its cleanups within the runner's stop grace. Cleanups do not use it: they
// run on contexts of their own.
package runctx

import (
	"context"
	"sync"
)

var state struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

func init() { Reset() }

// Context is the run-wide context.
func Context() context.Context {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.ctx
}

// Cancel ends the run-wide context: the package was interrupted.
func Cancel() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.cancel()
}

// Reset starts a fresh run-wide context (for tests of code that uses it).
func Reset() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.ctx, state.cancel = context.WithCancel(context.Background())
}

// With returns a context that ends with parent or with the run-wide
// context, whichever ends first; release it with the returned cancel.
func With(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	run := Context()
	stop := context.AfterFunc(run, func() { cancel(context.Cause(run)) })
	return ctx, func() { stop(); cancel(context.Canceled) }
}
