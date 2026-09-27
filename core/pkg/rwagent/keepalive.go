package rwagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultKeepaliveInterval is how often KeepUnlocked touches the agent.
//
// The agent's auto-lock is a sliding window of 30 minutes that only its own
// traffic resets, so anything well inside that is enough. Five minutes keeps the
// cost to one Unix-socket round trip per five minutes of a rollout.
const DefaultKeepaliveInterval = 5 * time.Minute

// keepaliveTimeout bounds one touch. It is short on purpose: this is a
// background ping, not an operation anyone is waiting for.
const keepaliveTimeout = 5 * time.Second

// KeepUnlocked holds the agent's auto-lock window open for as long as an
// operation runs, and returns a function that stops it.
//
// The agent locks itself after 30 minutes of no traffic. A rolling upgrade
// fetches every SSH key up front and then spends twenty-five minutes or more in
// SSH sessions the agent never sees, so it would lock partway through and the
// next thing that needed it — the health gate between two nodes — stopped and
// waited for someone to answer an unlock prompt. Half a rollout is the worst
// possible place to pause.
//
// This is not a way around the lock. The window is a measure of whether the
// wallet is in use, and during a rollout it is: the person who started the
// command is sitting in front of it. The touch (POST /v1/touch — /v1/status is
// read-only and never reset anything) says so. The agent only honours it from
// an approved app, and it never unlocks a locked wallet. It stops the moment
// the operation ends, so a wallet is never held open longer than the work that
// needed it.
//
// When the window cannot be held — an agent too old to have /v1/touch, or this
// binary not approved — it says so once on stderr and stops: the operation
// carries on, but the person at the terminal knows the wallet may lock.
func (c *Client) KeepUnlocked(interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = DefaultKeepaliveInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, keepaliveTimeout)
				_, err := c.Touch(pingCtx)
				pingCancel()
				switch {
				// A socket that is not there will not come back within one
				// command, and pinging it every five minutes tells nobody
				// anything. Whatever needs the agent next will report it
				// properly.
				case IsNotRunning(err):
					return
				case errors.Is(err, ErrTouchUnsupported):
					fmt.Fprintln(c.warn, "warning: this RootWallet agent is too old to be kept unlocked (no /v1/touch), so it may auto-lock during this operation. Update RootWallet.")
					return
				case IsNotApproved(err):
					fmt.Fprintf(c.warn, "warning: the RootWallet agent will not keep the wallet unlocked for this binary, so it may auto-lock during this operation: %v\n", err)
					return
				}
			}
		}
	}()

	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}
