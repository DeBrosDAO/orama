package sfu

import (
	"sync"
	"time"
)

// keyframeMinInterval is the shortest gap between two PLIs the SFU sends a
// publisher for one track. Every subscriber of a track asks for keyframes
// (a join, a loss burst); unthrottled, N subscribers would make the publisher
// encode N keyframes a second.
const keyframeMinInterval = 500 * time.Millisecond

type keyframeAction int

const (
	// keyframeSendNow: the interval has passed; send a PLI.
	keyframeSendNow keyframeAction = iota
	// keyframeDefer: inside the interval; send one PLI after the returned wait.
	keyframeDefer
	// keyframeCoalesced: inside the interval and a deferred PLI is already
	// scheduled, which serves this request too.
	keyframeCoalesced
)

// keyframeLimiter rate-limits the PLIs for one published track. A request
// inside the interval is not dropped but deferred to its end, so a subscriber
// that joined right after another's PLI still gets a keyframe promptly.
type keyframeLimiter struct {
	interval time.Duration

	mu        sync.Mutex
	last      time.Time
	scheduled bool
}

func newKeyframeLimiter(interval time.Duration) *keyframeLimiter {
	return &keyframeLimiter{interval: interval}
}

// reserve decides what to do with a keyframe request made at now.
func (l *keyframeLimiter) reserve(now time.Time) (keyframeAction, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.scheduled {
		return keyframeCoalesced, 0
	}
	if l.last.IsZero() || now.Sub(l.last) >= l.interval {
		l.last = now
		return keyframeSendNow, 0
	}
	l.scheduled = true
	return keyframeDefer, l.interval - now.Sub(l.last)
}

// fired records that the deferred PLI is being sent at now.
func (l *keyframeLimiter) fired(now time.Time) {
	l.mu.Lock()
	l.last = now
	l.scheduled = false
	l.mu.Unlock()
}
