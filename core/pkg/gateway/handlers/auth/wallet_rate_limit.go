package auth

import (
	"strings"
	"sync"
	"time"
)

// A challenge writes a nonce row for whatever wallet the body names, and can
// create a namespace on the way. The caller does not have to own that wallet,
// or hold anything at all — the endpoint is public, and it must be, because it
// is the first step of signing in.
//
// The per-address limit in the gateway caps one client. It does not cap a
// distributed grind against one victim's wallet, which fills the nonce table
// for that wallet and can push out the challenge they are about to answer. So
// the wallet named in the body is limited too, wherever it comes from.
//
// This is a small in-memory bucket per gateway rather than a shared counter:
// the point is to make grinding one wallet expensive, and an attacker who
// spreads across gateways is spreading across the whole fleet's capacity
// rather than concentrating on one victim.

// walletChallengeRate is how many challenges a single wallet may be issued per
// minute, and how many may arrive at once. A person signing in asks for one.
const (
	walletChallengeRate  = 10
	walletChallengeBurst = 5
)

// walletLimiter is a token bucket keyed by wallet.
type walletLimiter struct {
	mu      sync.Mutex
	buckets map[string]*walletBucket
	rate    float64 // tokens per second
	burst   float64
	now     func() time.Time // swappable so a test does not have to wait
}

type walletBucket struct {
	tokens float64
	last   time.Time
}

func newWalletLimiter(ratePerMinute, burst int) *walletLimiter {
	return &walletLimiter{
		buckets: map[string]*walletBucket{},
		rate:    float64(ratePerMinute) / 60.0,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// allow reports whether another challenge may be issued for this wallet.
func (l *walletLimiter) allow(wallet string) bool {
	key := strings.ToLower(strings.TrimSpace(wallet))
	if key == "" {
		// An empty wallet is refused before this is reached; not limiting it
		// here keeps the two failures from being confused for each other.
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &walletBucket{tokens: l.burst - 1, last: now}
		return true
	}

	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// forget drops buckets untouched for longer than idle, so a gateway that has
// served many wallets does not hold a row for each of them forever.
func (l *walletLimiter) forget(idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-idle)
	for wallet, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, wallet)
		}
	}
}

// challengeLimiterIdle is how long a wallet's bucket is kept after its last
// challenge. Longer than the refill takes, so nothing is reset early.
const challengeLimiterIdle = 30 * time.Minute

// startChallengeLimiterCleanup sweeps idle wallets in the background.
func (h *Handlers) startChallengeLimiterCleanup() {
	if h.challengeLimiter == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(challengeLimiterIdle)
		defer ticker.Stop()
		for range ticker.C {
			h.challengeLimiter.forget(challengeLimiterIdle)
		}
	}()
}
