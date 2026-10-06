package sfu

import (
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
)

// kickSkewMargin is how far two gateways' clocks may disagree. A kick carries
// the clock of the gateway that revoked (AtMs) and a ticket the clock of the
// gateway that issued it (IssuedAtMs); they may be different gateways.
const kickSkewMargin = 10 * time.Second

// kickWindow is how long after a kick this SFU refuses tickets of the kicked
// user to the room: a ticket lives TicketTTL, and the margin covers the clocks.
const kickWindow = ctrlauth.TicketTTL + kickSkewMargin

// kickLog remembers when a user was last removed from a room, so a join whose
// ticket was issued while the admission was still valid is refused when it
// arrives after the kick. Two clocks are involved and they are not the same
// clock: a ticket is refused when this SFU receives it within kickWindow of the
// kick (this SFU's own clock), unless its issue time is later than the kick's
// by more than kickSkewMargin (the gateways' clocks), which only a ticket
// issued after the revocation can have. Entries older than the window can
// refuse nothing, and are dropped.
type kickLog struct {
	mu    sync.Mutex
	now   func() time.Time
	kicks map[string]kickRecord // room + "\x00" + user
}

type kickRecord struct {
	atMs int64     // the kicking gateway's clock, unix ms
	seen time.Time // this SFU's clock when it received the latest kick
}

func newKickLog() *kickLog { return &kickLog{now: time.Now, kicks: make(map[string]kickRecord)} }

func kickKey(room, user string) string { return room + "\x00" + user }

func (k *kickLog) record(room, user string, atMs int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	for key, rec := range k.kicks {
		if now.Sub(rec.seen) > kickWindow {
			delete(k.kicks, key)
		}
	}
	key := kickKey(room, user)
	if prev, ok := k.kicks[key]; ok && prev.atMs > atMs {
		atMs = prev.atMs
	}
	k.kicks[key] = kickRecord{atMs: atMs, seen: now}
}

// refuses reports whether a ticket for (room, user), issued at issuedMs by a
// gateway and received now, predates a kick.
func (k *kickLog) refuses(room, user string, issuedMs int64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	rec, ok := k.kicks[kickKey(room, user)]
	if !ok || k.now().Sub(rec.seen) > kickWindow {
		return false
	}
	return issuedMs <= rec.atMs+kickSkewMargin.Milliseconds()
}
