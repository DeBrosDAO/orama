package ctrlauth

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

const (
	// replayWindow is how long a stamp can still verify: it is accepted from
	// maxSkew before its timestamp to maxSkew after it, so a copy of it made at
	// the earliest moment stays replayable for twice the skew.
	replayWindow = 2 * maxSkew

	// DefaultReplayCapacity bounds the stamps a ReplayGuard remembers. Only
	// stamps that verified are remembered, so only a holder of the namespace's
	// key can fill it.
	DefaultReplayCapacity = 8192
)

// ErrReplayed means a stamp that already authenticated one request was
// presented again.
var ErrReplayed = errors.New("request is a replay: its MAC was already used")

// ReplayGuard remembers the stamps it has seen for as long as they could still
// verify, and refuses the second use of one. It is a bounded LRU (oldest first): when full, the
// oldest stamp is forgotten first.
type ReplayGuard struct {
	mu       sync.Mutex
	capacity int
	order    *list.List // front = most recent; values are *seenStamp
	seen     map[string]*list.Element
}

type seenStamp struct {
	key string
	at  time.Time
}

// NewReplayGuard returns a guard that remembers up to capacity stamps.
func NewReplayGuard(capacity int) *ReplayGuard {
	if capacity <= 0 {
		capacity = DefaultReplayCapacity
	}
	return &ReplayGuard{capacity: capacity, order: list.New(), seen: make(map[string]*list.Element)}
}

// Use records the stamp in header, which must already have passed Verify. It
// returns ErrReplayed when the same stamp was used before and could still verify.
func (g *ReplayGuard) Use(header string, now time.Time) error {
	_, nonce, sig, ok := splitStamp(header)
	if !ok {
		return ErrBadMAC
	}
	key := nonce + "." + sig
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expire(now)
	if _, dup := g.seen[key]; dup {
		return ErrReplayed
	}
	g.seen[key] = g.order.PushFront(&seenStamp{key: key, at: now})
	for g.order.Len() > g.capacity {
		oldest := g.order.Back()
		delete(g.seen, oldest.Value.(*seenStamp).key)
		g.order.Remove(oldest)
	}
	return nil
}

// expire drops stamps that can no longer verify. Entries are ordered by first
// use, so it drops from the back while they are stale.
func (g *ReplayGuard) expire(now time.Time) {
	for el := g.order.Back(); el != nil; el = g.order.Back() {
		s := el.Value.(*seenStamp)
		if now.Sub(s.at) <= replayWindow {
			return
		}
		delete(g.seen, s.key)
		g.order.Remove(el)
	}
}
