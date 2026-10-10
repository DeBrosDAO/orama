package sfu

import (
	"sync"
	"time"
)

// muteWindow is how long after a mute this SFU still corrects a join: a ticket
// lives ctrlauth.TicketTTL, and the margin covers the gateways' clocks. It is
// the kick log's window, for the same reason.
const muteWindow = kickWindow

// muteLog remembers the latest mute or unmute of a user in a room. A ticket
// carries the mute state the gateway read when it issued it, a snapshot; a mute
// that lands afterwards finds no peer to change when the user is reconnecting
// or its join is in flight, so the snapshot would win. A join whose ticket was
// issued at or before the change takes the logged state instead, on the same
// two clocks as the kick log: the receipt window is this SFU's, the comparison
// with the ticket's issue time is the gateways' (kickSkewMargin). Entries older
// than the window can correct nothing, and are dropped.
type muteLog struct {
	mu    sync.Mutex
	now   func() time.Time
	seq   uint64
	mutes map[string]muteRecord // room + "\x00" + user
}

type muteRecord struct {
	muted bool
	atMs  int64     // the gateway's clock when the state was recorded, unix ms
	seen  time.Time // this SFU's clock when it received it
	seq   uint64    // changes whenever the record does
}

func newMuteLog() *muteLog { return &muteLog{now: time.Now, mutes: make(map[string]muteRecord)} }

// record logs the state a mute request sets and reports whether it is the
// newest known, by the gateway's clock. A request older than the one on record
// is a late arrival and changes nothing, on the log or in the room.
func (m *muteLog) record(room, user string, muted bool, atMs int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for key, rec := range m.mutes {
		if now.Sub(rec.seen) > muteWindow {
			delete(m.mutes, key)
		}
	}
	key := kickKey(room, user)
	if prev, ok := m.mutes[key]; ok && prev.atMs > atMs {
		return false
	}
	m.seq++
	m.mutes[key] = muteRecord{muted: muted, atMs: atMs, seen: now, seq: m.seq}
	return true
}

// correction returns the logged state for (room, user) when a ticket issued at
// issuedMs, received now, predates it.
func (m *muteLog) correction(room, user string, issuedMs int64) (muteRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.mutes[kickKey(room, user)]
	if !ok || m.now().Sub(rec.seen) > muteWindow || issuedMs > rec.atMs+kickSkewMargin.Milliseconds() {
		return muteRecord{}, false
	}
	return rec, true
}

// current returns the sequence of the record for (room, user), 0 when none.
func (m *muteLog) current(room, user string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutes[kickKey(room, user)].seq
}
