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
// arrives after the kick. Where the namespace admits users, the kick and the
// ticket each carry the generation of an admission (ctrlauth.Ticket.AdmitGen)
// and a ticket is refused exactly when its generation is not newer than the
// kick's: no clock is involved, so a user admitted again right after a kick,
// whose ticket is of a newer generation, is let in. Otherwise (a namespace
// without admission, a kick from a gateway that predates generations, a ticket
// issued on an admission that predates them) two clocks are involved and they
// are not the same clock: a ticket is refused when this SFU receives it within
// kickWindow of the kick (this SFU's own clock), unless its issue time is later
// than the kick's by more than kickSkewMargin (the gateways' clocks), which only
// a ticket issued after the revocation can have. Entries older than the window
// can refuse nothing, and are dropped.
type kickLog struct {
	mu    sync.Mutex
	now   func() time.Time
	kicks map[string]kickRecord // room + "\x00" + user
}

type kickRecord struct {
	atMs int64     // the kicking gateway's clock, unix ms
	gen  int64     // the newest admission generation a kick revoked (0: none known)
	seen time.Time // this SFU's clock when it received the latest kick
}

func newKickLog() *kickLog { return &kickLog{now: time.Now, kicks: make(map[string]kickRecord)} }

func kickKey(room, user string) string { return room + "\x00" + user }

func (k *kickLog) record(room, user string, atMs, gen int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.now()
	for key, rec := range k.kicks {
		if now.Sub(rec.seen) > kickWindow {
			delete(k.kicks, key)
		}
	}
	key := kickKey(room, user)
	if prev, ok := k.kicks[key]; ok {
		atMs = max(atMs, prev.atMs)
		// A kick that carries no generation (an old gateway, or no admission
		// on record) puts the entry back on the clock rule. Keeping the
		// earlier kick's generation instead let a ticket of an admission made
		// between the two kicks through the second one.
		if gen > 0 {
			gen = max(gen, prev.gen)
		}
	}
	k.kicks[key] = kickRecord{atMs: atMs, gen: gen, seen: now}
}

// refuses reports whether a ticket for (room, user), issued at issuedMs by a
// gateway on the admission of generation gen (0: none) and received now,
// predates a kick.
func (k *kickLog) refuses(room, user string, issuedMs, gen int64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	rec, ok := k.kicks[kickKey(room, user)]
	if !ok || k.now().Sub(rec.seen) > kickWindow {
		return false
	}
	if gen > 0 && rec.gen > 0 {
		return gen <= rec.gen
	}
	return issuedMs <= rec.atMs+kickSkewMargin.Milliseconds()
}
