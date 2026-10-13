package sfu

import (
	"errors"
	"sync"
	"time"
)

const (
	// signalBurst and signalRefillPerSecond bound the signaling messages that
	// make the SFU work (offers, ICE candidates) for one peer. A client
	// trickles a few dozen candidates when it connects and offers a handful of
	// times a call; this leaves both room to breathe and stops a client that
	// floods the SFU with work.
	signalBurst           = 40
	signalRefillPerSecond = 10

	// glareYieldsPerWindow caps how often one peer may make the SFU yield an
	// outstanding offer of its own (glare, glare.go), which costs a throwaway
	// PeerConnection each time. A client that keeps offering while withholding
	// its answers would otherwise make the SFU build them without end.
	glareYieldsPerWindow = 6
	glareYieldWindow     = time.Minute

	// rateLimitedCode is the error frame a peer gets before it is closed for
	// exceeding either limit.
	rateLimitedCode = "rate_limited"
)

// ErrSignalRateLimited ends a peer that exceeded its signaling allowance.
var ErrSignalRateLimited = errors.New("signaling rate limit exceeded")

// signalLimiter is the signaling allowance of one peer: a token bucket for
// offers and ICE candidates, and a sliding-window cap on glare yields.
type signalLimiter struct {
	mu     sync.Mutex
	now    func() time.Time // a field so a test drives time
	tokens float64
	last   time.Time
	yields []time.Time
}

func newSignalLimiter() *signalLimiter {
	return &signalLimiter{now: time.Now, tokens: signalBurst}
}

// allowSignal takes one token; false when the bucket is empty.
func (l *signalLimiter) allowSignal() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens += now.Sub(l.last).Seconds() * signalRefillPerSecond
		if l.tokens > signalBurst {
			l.tokens = signalBurst
		}
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// allowGlareYield records one glare yield; false when the window already holds
// glareYieldsPerWindow of them.
func (l *signalLimiter) allowGlareYield() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.yields[:0]
	for _, at := range l.yields {
		if now.Sub(at) < glareYieldWindow {
			kept = append(kept, at)
		}
	}
	l.yields = kept
	if len(l.yields) >= glareYieldsPerWindow {
		return false
	}
	l.yields = append(l.yields, now)
	return true
}
