package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowReload makes the registry block every SELECT until release is closed and
// counts them.
func slowReload(db *revocationDB) (started chan struct{}, release chan struct{}, reloads *int32) {
	started = make(chan struct{}, 64)
	release = make(chan struct{})
	reloads = new(int32)
	db.onSelect = func() {
		atomic.AddInt32(reloads, 1)
		started <- struct{}{}
		<-release
	}
	return started, release, reloads
}

func waitOrFail(t *testing.T, wg *sync.WaitGroup, within time.Duration, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatalf("%s", what)
	}
}

// A reload slower than the interval must not turn every request into its own
// blocking full-table read: one reload runs, the rest use the list they have.
func TestRevocationList_concurrentRequestsDuringASlowReloadCauseOneReload(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	list.Denies(claims, []string{"ak_key:ns"}) // first load

	*clock = clock.Add(RevocationRefreshInterval + time.Second) // stale, but inside RevocationStaleness
	started, release, reloads := slowReload(db)

	var starter sync.WaitGroup
	starter.Add(1)
	go func() { defer starter.Done(); list.Denies(claims, []string{"ak_key:ns"}) }()
	<-started

	var others sync.WaitGroup
	for i := 0; i < 50; i++ {
		others.Add(1)
		go func() { defer others.Done(); list.Denies(claims, []string{"ak_key:ns"}) }()
	}
	waitOrFail(t, &others, 2*time.Second, "requests blocked on a reload although their list was within the staleness bound")

	close(release)
	waitOrFail(t, &starter, 2*time.Second, "the reload never finished")
	if got := atomic.LoadInt32(reloads); got != 1 {
		t.Fatalf("reloads = %d, want exactly 1 for 51 concurrent requests", got)
	}
}

// The documented guarantee is RevocationStaleness: a request must not be served
// from a list older than that, so it waits for the running reload.
func TestRevocationList_aListOlderThanTheStalenessBoundWaitsForTheRunningReload(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	list.Denies(claims, []string{"ak_key:ns"}) // first load

	// Revoked elsewhere; only the reload can reveal it.
	db.mu.Lock()
	db.rows = append(db.rows, revocation{subject: "ak_key:ns", issuedBefore: clock.Unix(), expiresAt: clock.Unix() + 3600})
	db.mu.Unlock()

	*clock = clock.Add(RevocationStaleness + time.Second)
	started, release, reloads := slowReload(db)

	var starter sync.WaitGroup
	starter.Add(1)
	go func() { defer starter.Done(); list.Denies(claims, []string{"ak_key:ns"}) }()
	<-started

	var denied atomic.Bool
	var waiter sync.WaitGroup
	waiter.Add(1)
	go func() { defer waiter.Done(); denied.Store(list.Denies(claims, []string{"ak_key:ns"})) }()

	time.Sleep(100 * time.Millisecond) // the waiter must still be parked
	if denied.Load() {
		t.Fatal("a request was served before the running reload finished")
	}
	close(release)
	waitOrFail(t, &waiter, 2*time.Second, "the waiter never resumed")
	starter.Wait()

	if !denied.Load() {
		t.Error("the waiting request was served from the old list, not the reloaded one")
	}
	if got := atomic.LoadInt32(reloads); got != 1 {
		t.Fatalf("reloads = %d, want 1: the waiter must join the running reload, not start another", got)
	}
}
