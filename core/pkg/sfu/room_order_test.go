package sfu

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"go.uber.org/zap"
)

// settle is how long a goroutine is given to reach the report it is blocked on.
const settle = 300 * time.Millisecond

// A peer's leave must never be queued before its join, or the namespace's
// subscribers see a member who never leaves. RemovePeer can run the moment
// AddPeer has put the peer in the room (a kick, a closing socket), so the join
// must be queued while the room's lock is still held: a report made after the
// unlock can be overtaken by the leave. The report blocks here on the
// reporter's own lock, which freezes both calls at the point that matters.
func TestRoom_membershipIsReportedUnderTheRoomLock(t *testing.T) {
	room := NewRoomManager(testConfig(), testLogger()).GetOrCreateRoom("order")
	rep := &reporter{queue: make(chan queuedEvent, 8), logger: zap.NewNop()}
	room.reporter = rep
	p := NewPeer("alice", nil, room, testLogger())

	rep.mu.Lock() // every report now waits
	added := make(chan error, 1)
	go func() { added <- room.AddPeer(p) }()
	time.Sleep(settle)
	if room.peersMu.TryLock() {
		room.peersMu.Unlock()
		rep.mu.Unlock()
		t.Fatal("the join is reported after the room's lock is released, so a leave can be queued before it")
	}
	rep.mu.Unlock()
	if err := <-added; err != nil {
		t.Fatal(err)
	}

	rep.mu.Lock()
	removed := make(chan struct{})
	go func() { room.RemovePeer(p.ID); close(removed) }()
	time.Sleep(settle)
	if room.peersMu.TryLock() {
		room.peersMu.Unlock()
		rep.mu.Unlock()
		t.Fatal("the leave is reported after the room's lock is released, so a rejoin's join can be queued before it")
	}
	rep.mu.Unlock()
	<-removed

	close(rep.queue)
	var got []string
	for q := range rep.queue {
		got = append(got, q.event.Type)
	}
	if len(got) != 2 || got[0] != ctrlauth.EventJoin || got[1] != ctrlauth.EventLeave {
		t.Fatalf("queued %v, want [join leave]", got)
	}
}
