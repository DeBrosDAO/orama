package monitor

import (
	"math/rand/v2"
	"time"
)

// Reconnect backoff bounds for the live stream.
const (
	// reconnectMin is the first wait after a stream drops, and the pause
	// before reconnecting after the gateway ends a stream on schedule.
	reconnectMin = time.Second
	// reconnectMax caps the wait while a gateway stays unreachable.
	reconnectMax = 30 * time.Second
	// reconnectJitter spreads each wait by up to ±20%, so every monitor
	// watching a gateway that restarts does not reconnect in the same instant.
	reconnectJitter = 0.2
)

// backoff doubles the wait between reconnects up to a cap, and starts over
// once a connection has delivered data.
type backoff struct {
	min, max, cur time.Duration
	// random returns a number in [0, 1); tests fix it.
	random func() float64
}

func newBackoff(random func() float64) *backoff {
	if random == nil {
		random = rand.Float64
	}
	return &backoff{min: reconnectMin, max: reconnectMax, random: random}
}

// Next is the wait before the next attempt: the doubled base, jittered.
func (b *backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.min
	} else {
		b.cur = min(b.cur*2, b.max)
	}
	return b.jittered(b.cur)
}

// Reset starts the sequence over.
func (b *backoff) Reset() {
	b.cur = 0
}

// jittered scales d by a random factor in [1-reconnectJitter, 1+reconnectJitter).
func (b *backoff) jittered(d time.Duration) time.Duration {
	factor := 1 + reconnectJitter*(2*b.random()-1)
	return time.Duration(float64(d) * factor)
}
