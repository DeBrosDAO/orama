package sfu

import (
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
)

func nowMs() int64 { return time.Now().UnixMilli() }

// peerOf returns the room's peer of user, or nil.
func peerOf(s *Server, room, user string) *Peer {
	r := s.roomManager.GetRoom(room)
	if r == nil {
		return nil
	}
	if peers := r.peersOfUser(user); len(peers) > 0 {
		return peers[0]
	}
	return nil
}

func muteUser(t *testing.T, s *Server, room, user string, muted bool, atMs int64) {
	t.Helper()
	control(t, s, ctrlauth.MutePath, ctrlauth.MuteRequest{Room: room, UserID: user, Muted: muted, AtMs: atMs})
}

// NEW-1: a ticket minted before a mute carries Muted=false; the user
// reconnects with it after the mute, with no peer in the room for the mute to find.
func TestMute_rejoinWithAPreMuteTicketStaysMuted(t *testing.T) {
	s := newAuthServer(t)
	stale := testTicket(t, s, "r1", "alice") // issued unmuted
	stale.IssuedAtMs = nowMs() - 1000

	muteUser(t, s, "r1", "alice", true, nowMs()) // nobody of alice's is in the room
	joinAs(t, s, stale, "")

	if p := peerOf(s, "r1", "alice"); p == nil || !p.muted.Load() {
		t.Fatal("a user muted after its ticket was issued rejoined unmuted")
	}
}

func TestMute_joinInFlightWhenTheMuteLandsEndsMuted(t *testing.T) {
	s := newAuthServer(t)
	tk := testTicket(t, s, "r1", "alice")
	conn := dialSignalAs(t, s, "room=r1", tk) // upgraded, no join frame yet
	time.Sleep(5 * time.Millisecond)

	muteUser(t, s, "r1", "alice", true, nowMs())
	sendJoin(t, conn, "r1")
	readType(t, conn, MessageTypeWelcome)

	if p := peerOf(s, "r1", "alice"); p == nil || !p.muted.Load() {
		t.Fatal("a join in flight when the mute landed ended unmuted")
	}
}

func TestMute_unmuteThenRejoinWithAPreUnmuteTicketEndsUnmuted(t *testing.T) {
	s := newAuthServer(t)
	stale := testTicket(t, s, "r1", "alice")
	stale.Muted = true // issued while muted
	stale.IssuedAtMs = nowMs() - 2000

	muteUser(t, s, "r1", "alice", true, nowMs()-1500)
	muteUser(t, s, "r1", "alice", false, nowMs())
	joinAs(t, s, stale, "")

	if p := peerOf(s, "r1", "alice"); p == nil || p.muted.Load() {
		t.Fatal("a user unmuted after its ticket was issued rejoined muted")
	}
}

func TestMute_aTicketIssuedAfterTheMuteIsAuthoritative(t *testing.T) {
	s := newAuthServer(t)
	muteUser(t, s, "r1", "alice", true, nowMs()-int64(kickSkewMargin/time.Millisecond)-5000)
	fresh := testTicket(t, s, "r1", "alice") // issued unmuted: the gateway had read the unmute
	joinAs(t, s, fresh, "")

	if p := peerOf(s, "r1", "alice"); p == nil || p.muted.Load() {
		t.Fatal("the log overrode a ticket issued after the change")
	}
}

func TestMute_otherUsersAndRoomsAreNotTouchedByTheLog(t *testing.T) {
	s := newAuthServer(t)
	muteUser(t, s, "r1", "alice", true, nowMs())
	joinAs(t, s, testTicket(t, s, "r1", "bob"), "")
	joinAs(t, s, testTicket(t, s, "r2", "alice"), "")

	if peerOf(s, "r1", "bob").muted.Load() || peerOf(s, "r2", "alice").muted.Load() {
		t.Fatal("a mute leaked to another user or room")
	}
}

func TestMute_aLateOlderMuteDoesNotOverwriteANewerOne(t *testing.T) {
	s := newAuthServer(t)
	alice, _ := joinAs(t, s, testTicket(t, s, "r1", "alice"), "")
	_ = alice
	at := nowMs()
	muteUser(t, s, "r1", "alice", false, at)
	muteUser(t, s, "r1", "alice", true, at-500) // arrives late

	if peerOf(s, "r1", "alice").muted.Load() {
		t.Fatal("an older mute arriving late overwrote the newer unmute")
	}
}

func TestMuteLog_usesTheSFUsClockAndTheGatewaySkewMargin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := newMuteLog()
	m.now = func() time.Time { return now }
	at := now.UnixMilli()
	m.record("r", "alice", true, at)

	cases := []struct {
		name     string
		user     string
		issuedMs int64
		later    time.Duration
		want     bool
	}{
		{"issued before the mute", "alice", at - 1000, 0, true},
		{"issued within the skew margin after it", "alice", at + kickSkewMargin.Milliseconds(), 0, true},
		{"issued after the mute beyond the margin", "alice", at + kickSkewMargin.Milliseconds() + 1, 0, false},
		{"received after the window", "alice", at - 1000, muteWindow + time.Second, false},
		{"another user", "bob", at - 1000, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now = time.Unix(1_800_000_000, 0).Add(c.later)
			if _, got := m.correction("r", c.user, c.issuedMs); got != c.want {
				t.Fatalf("correction = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMuteLog_dropsEntriesOlderThanTheWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := newMuteLog()
	m.now = func() time.Time { return now }
	m.record("r", "old", true, now.UnixMilli())
	now = now.Add(muteWindow + time.Second)
	m.record("r", "alice", true, now.UnixMilli())
	if _, ok := m.mutes[kickKey("r", "old")]; ok {
		t.Error("a mute past the window was kept")
	}
}

// LOW-A: a stamp made for one SFU does not verify at another.
func TestControl_aStampForAnotherSFUIsRefused(t *testing.T) {
	s := newAuthServer(t)
	body := []byte(`{"room":"r1","user_id":"alice","at_ms":1}`)
	for _, target := range []string{"10.0.0.2:8443", "", s.config.ListenAddr + "0"} {
		mac := ctrlauth.Sign(s.controlKey, target, http.MethodPost, ctrlauth.KickPath, body, time.Now())
		if w := controlRaw(s, ctrlauth.KickPath, body, mac); w.Code != http.StatusUnauthorized {
			t.Errorf("a stamp for %q: status = %d, want 401", target, w.Code)
		}
	}
	mac := ctrlauth.Sign(s.controlKey, s.config.ListenAddr, http.MethodPost, ctrlauth.KickPath, body, time.Now())
	if w := controlRaw(s, ctrlauth.KickPath, body, mac); w.Code != http.StatusOK {
		t.Errorf("a stamp for this SFU: status = %d, want 200", w.Code)
	}
}
