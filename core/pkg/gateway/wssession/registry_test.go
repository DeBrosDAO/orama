package wssession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// revokedJTIs denies the tokens it names.
type revokedJTIs map[string]bool

func (d revokedJTIs) Revoked(c *auth.JWTClaims) bool     { return d[c.Jti] }
func (d revokedJTIs) RefreshRevocations(context.Context) {}

// closeLog records what a socket was closed with.
type closeLog struct {
	mu    sync.Mutex
	codes []int
}

func (l *closeLog) closer() Closer {
	return func(code int, _ string) {
		l.mu.Lock()
		l.codes = append(l.codes, code)
		l.mu.Unlock()
	}
}

func (l *closeLog) got() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.codes...)
}

var now = time.Unix(1_800_000_000, 0)

func token(jti string, exp time.Time) *auth.JWTClaims {
	return &auth.JWTClaims{Sub: "0xwallet", Jti: jti, Iat: now.Add(-time.Minute).Unix(), Exp: exp.Unix()}
}

func TestRegister_noClaimsIsNotRegistered(t *testing.T) {
	r := NewRegistry(nil)
	var log closeLog
	s := r.Register(nil, log.closer())
	if s != nil || r.Len() != 0 {
		t.Fatal("a socket with no token was registered; there is nothing to hold it to")
	}
	// Every method is safe on the nil a keyless socket gets.
	if s.Expired(now) {
		t.Error("a keyless socket reported expired")
	}
	if !errors.Is(s.Refresh(token("x", now)), ErrNotRefreshable) {
		t.Error("a keyless socket accepted a token; that would hand it an identity")
	}
	s.Unregister()
}

func TestSweep_closesASocketWhoseTokenExpired(t *testing.T) {
	r := NewRegistry(nil)
	var expiredLog, graceLog, liveLog closeLog
	r.Register(token("a", now.Add(-ExpiryGrace-time.Second)), expiredLog.closer())
	r.Register(token("b", now.Add(-ExpiryGrace+time.Second)), graceLog.closer())
	r.Register(token("c", now.Add(time.Minute)), liveLog.closer())

	if n := r.Sweep(now, revokedJTIs{}); n != 1 {
		t.Errorf("closed %d sockets, want 1", n)
	}
	if got := expiredLog.got(); len(got) != 1 || got[0] != CloseExpired {
		t.Errorf("expired socket closed with %v, want [%d]", got, CloseExpired)
	}
	if len(graceLog.got()) != 0 {
		t.Error("a socket inside the grace window was closed; the client had no time to refresh")
	}
	if len(liveLog.got()) != 0 {
		t.Error("a socket with a live token was closed")
	}
}

// The bug: nothing closed an open socket when its session was revoked.
func TestSweep_closesASocketWhoseTokenWasRevoked(t *testing.T) {
	r := NewRegistry(nil)
	var revoked, other closeLog
	r.Register(token("gone", now.Add(time.Minute)), revoked.closer())
	r.Register(token("kept", now.Add(time.Minute)), other.closer())

	r.Sweep(now, revokedJTIs{"gone": true})

	if got := revoked.got(); len(got) != 1 || got[0] != CloseRevoked {
		t.Errorf("revoked socket closed with %v, want [%d]", got, CloseRevoked)
	}
	if len(other.got()) != 0 {
		t.Error("a socket whose token was not revoked was closed")
	}
}

func TestSweep_closesASocketOnce(t *testing.T) {
	r := NewRegistry(nil)
	var log closeLog
	r.Register(token("gone", now.Add(time.Minute)), log.closer())

	r.Sweep(now, revokedJTIs{"gone": true})
	r.Sweep(now.Add(SweepInterval), revokedJTIs{"gone": true})

	if got := log.got(); len(got) != 1 {
		t.Errorf("closed %d times, want once: the handler may not have unregistered it yet", len(got))
	}
}

func TestUnregister_stopsTheSweepFromReachingIt(t *testing.T) {
	r := NewRegistry(nil)
	var log closeLog
	s := r.Register(token("gone", now.Add(time.Minute)), log.closer())
	s.Unregister()

	r.Sweep(now, revokedJTIs{"gone": true})
	if len(log.got()) != 0 || r.Len() != 0 {
		t.Error("a socket that had gone was still swept")
	}
}

func TestRefresh_holdsTheSocketToTheNewToken(t *testing.T) {
	r := NewRegistry(nil)
	var log closeLog
	s := r.Register(token("old", now.Add(-ExpiryGrace-time.Minute)), log.closer())
	if !s.Expired(now) {
		t.Fatal("precondition: the old token is past its grace")
	}

	if err := s.Refresh(token("new", now.Add(15*time.Minute))); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if s.Expired(now) {
		t.Error("a refreshed socket still reported the old token's expiry")
	}
	// Revoking the token it was opened with no longer reaches it; revoking
	// the one it holds now does.
	r.Sweep(now, revokedJTIs{"old": true})
	if len(log.got()) != 0 {
		t.Error("revoking the replaced token closed the socket")
	}
	r.Sweep(now, revokedJTIs{"new": true})
	if got := log.got(); len(got) != 1 || got[0] != CloseRevoked {
		t.Errorf("revoking the current token closed with %v, want [%d]", got, CloseRevoked)
	}
}

// The bug: auth.refresh on an open socket could switch it to another subject.
func TestRefresh_refusesAnotherSubject(t *testing.T) {
	r := NewRegistry(nil)
	s := r.Register(token("a", now.Add(time.Minute)), (&closeLog{}).closer())

	other := token("b", now.Add(time.Hour))
	other.Sub = "0xsomeone-else"
	if err := s.Refresh(other); !errors.Is(err, ErrSubjectChanged) {
		t.Fatalf("refresh to another subject returned %v, want ErrSubjectChanged", err)
	}
	if got := s.Claims(); got.Sub != "0xwallet" || got.Jti != "a" {
		t.Errorf("a refused refresh changed the socket to %+v", got)
	}
	if !errors.Is(s.Refresh(nil), ErrNoClaims) {
		t.Error("a refresh with no claims was accepted")
	}
}

func TestExpired_boundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		exp  int64
		want bool
	}{
		{"no exp has nothing to enforce", 0, false},
		{"negative exp has nothing to enforce", -5, false},
		{"well before exp", now.Add(10 * time.Minute).Unix(), false},
		{"past exp, inside grace", now.Add(-30 * time.Second).Unix(), false},
		{"exactly at exp+grace", now.Add(-ExpiryGrace).Unix(), false},
		{"past exp+grace", now.Add(-ExpiryGrace - time.Second).Unix(), true},
		{"long expired", now.Add(-24 * time.Hour).Unix(), true},
	} {
		if got := expired(tc.exp, now); got != tc.want {
			t.Errorf("%s: expired(%d) = %v, want %v", tc.name, tc.exp, got, tc.want)
		}
	}
}

// countingDenier records the order a sweep asks in.
type countingDenier struct {
	mu        sync.Mutex
	refreshes int
	revoked   map[string]bool
	askedAt   []int // refresh count at each Revoked call
}

func (d *countingDenier) RefreshRevocations(context.Context) {
	d.mu.Lock()
	d.refreshes++
	d.mu.Unlock()
}

func (d *countingDenier) Revoked(c *auth.JWTClaims) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.askedAt = append(d.askedAt, d.refreshes)
	return d.revoked[c.Jti]
}

func TestRun_reloadsTheListThenSweepsUntilCancelled(t *testing.T) {
	r := NewRegistry(nil)
	closed := make(chan int, 1)
	r.Register(token("gone", time.Now().Add(time.Hour)), func(code int, _ string) { closed <- code })
	d := &countingDenier{revoked: map[string]bool{"gone": true}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, d, time.Millisecond)
		close(done)
	}()

	select {
	case code := <-closed:
		if code != CloseRevoked {
			t.Errorf("closed with %d, want %d", code, CloseRevoked)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sweeper never closed a revoked socket")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweeper did not stop when its context was cancelled")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.askedAt) == 0 || d.askedAt[0] == 0 {
		t.Error("a sweep asked about revocations before reloading the list; " +
			"it would miss a revocation recorded on another gateway for up to another interval")
	}
}

// Every gateway runs a sweeper, most of them idle. Reloading the list for no
// socket would be a registry query per gateway per interval for nothing.
func TestRun_anIdleGatewayDoesNotReloadTheList(t *testing.T) {
	r := NewRegistry(nil)
	d := &countingDenier{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r.Run(ctx, d, time.Millisecond)

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refreshes != 0 {
		t.Errorf("an empty registry reloaded the revocation list %d times", d.refreshes)
	}
}

// A client that stops reading makes its close take up to CloseFrameTimeout.
// That must not hold up closing everybody else's revoked sockets.
func TestSweep_aStalledCloseDoesNotHoldUpTheOthers(t *testing.T) {
	r := NewRegistry(nil)
	release := make(chan struct{})
	stalled := make(chan struct{})
	r.Register(token("stuck", now.Add(time.Minute)), func(int, string) {
		close(stalled)
		<-release
	})
	closedOther := make(chan struct{})
	r.Register(token("other", now.Add(time.Minute)), func(int, string) { close(closedOther) })

	swept := make(chan int, 1)
	go func() { swept <- r.Sweep(now, revokedJTIs{"stuck": true, "other": true}) }()

	select {
	case <-closedOther:
	case <-time.After(5 * time.Second):
		t.Fatal("one stalled close held up closing another revoked socket")
	}
	<-stalled
	close(release)
	if n := <-swept; n != 2 {
		t.Errorf("the sweep reported %d closed, want 2", n)
	}
}

// A socket and a new request must learn of a revocation within the same bound:
// the list's staleness, which is what the docs promise for both.
func TestSweepInterval_isTheRevocationListsStaleness(t *testing.T) {
	if SweepInterval != auth.RevocationRefreshInterval {
		t.Errorf("SweepInterval = %s, want the revocation list's %s", SweepInterval, auth.RevocationRefreshInterval)
	}
}

// feat-422: a refresh keeps the device too. The function was told which
// device is calling, and revoking that device must reach this socket.
func TestRefresh_refusesAnotherDevice(t *testing.T) {
	r := NewRegistry(nil)
	opened := token("a", now.Add(time.Minute))
	opened.Did = "device-1"
	s := r.Register(opened, (&closeLog{}).closer())

	for name, did := range map[string]string{"another device": "device-2", "no device": ""} {
		next := token("b", now.Add(time.Hour))
		next.Did = did
		if err := s.Refresh(next); !errors.Is(err, ErrDeviceChanged) {
			t.Errorf("%s: refresh returned %v, want ErrDeviceChanged", name, err)
		}
	}
	same := token("c", now.Add(time.Hour))
	same.Did = "device-1"
	if err := s.Refresh(same); err != nil {
		t.Errorf("a refresh from the same device was refused: %v", err)
	}
}
