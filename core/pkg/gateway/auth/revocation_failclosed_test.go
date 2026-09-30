package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// failRegistry makes every SELECT fail.
func failRegistry(db *revocationDB, fail bool) {
	db.mu.Lock()
	db.failAlways = fail
	db.mu.Unlock()
}

func selectsOf(db *revocationDB) int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.selects
}

func denyCheck(list *RevocationList, claims *JWTClaims) (bool, error) {
	return list.Denies(claims, []string{"ak_key:ns"})
}

// A failed reload used to stamp the list fresh, so a registry outage made the
// stale copy look current and a revocation made during it was never seen.
func TestRevocationList_aFailedReloadDoesNotRefreshTheList(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if _, err := denyCheck(list, claims); err != nil {
		t.Fatalf("first load: %v", err)
	}
	loadedAt := list.lastRefresh

	failRegistry(db, true)
	*clock = clock.Add(RevocationRefreshInterval + time.Second)
	if err := list.Refresh(context.Background()); err == nil {
		t.Fatal("a failed reload reported success")
	}
	if !list.lastRefresh.Equal(loadedAt) {
		t.Errorf("lastRefresh moved from %s to %s on a failed reload", loadedAt, list.lastRefresh)
	}
}

// The regression: past the bound, with the registry down, the request is
// refused retryably. Before the fix it was allowed, on a list stamped fresh.
func TestRevocationList_aListOlderThanTheBoundThatCannotBeRefreshedRefuses(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if _, err := denyCheck(list, claims); err != nil {
		t.Fatalf("first load: %v", err)
	}

	failRegistry(db, true)
	*clock = clock.Add(RevocationStaleness + time.Second)
	denied, err := denyCheck(list, claims)
	if !errors.Is(err, ErrRevocationsUnavailable) {
		t.Fatalf("err = %v, want ErrRevocationsUnavailable; the request would have been allowed on an unknown state", err)
	}
	if denied {
		t.Error("an unknown state reported a verdict")
	}

	// Every later request inside the outage meets the same answer.
	*clock = clock.Add(RevocationRefreshInterval)
	if _, err := denyCheck(list, claims); !errors.Is(err, ErrRevocationsUnavailable) {
		t.Errorf("err = %v on a later request, want ErrRevocationsUnavailable", err)
	}
}

// A list that is still inside the bound keeps answering while the registry is
// down: a blip shorter than the bound costs nothing.
func TestRevocationList_aListInsideTheBoundKeepsAnsweringWhileTheRegistryIsDown(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "ak_key:ns", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if _, err := denyCheck(list, claims); err != nil {
		t.Fatalf("first load: %v", err)
	}

	failRegistry(db, true)
	*clock = clock.Add(RevocationStaleness - time.Second)
	denied, err := denyCheck(list, claims)
	if err != nil {
		t.Fatalf("a list %s old was refused: %v", RevocationStaleness-time.Second, err)
	}
	if !denied {
		t.Error("the revocation was forgotten during a short outage")
	}
}

// A gateway that has never read the list knows nothing, and "nothing" is not
// "nothing is revoked".
func TestRevocationList_aListNeverLoadedWithAFailingRegistryRefuses(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	failRegistry(db, true)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix()}

	if _, err := denyCheck(list, claims); !errors.Is(err, ErrRevocationsUnavailable) {
		t.Fatalf("err = %v, want ErrRevocationsUnavailable", err)
	}
	if _, err := list.DeniesSubject("ak_key:ns"); !errors.Is(err, ErrRevocationsUnavailable) {
		t.Fatalf("DeniesSubject err = %v, want ErrRevocationsUnavailable", err)
	}
}

func TestRevocationList_recoversWhenTheRegistryReturns(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	failRegistry(db, true)
	if _, err := denyCheck(list, claims); err == nil {
		t.Fatal("a failing registry was answered from")
	}

	// Revoked while the registry was out.
	db.mu.Lock()
	db.rows = append(db.rows, revocation{subject: "ak_key:ns", issuedBefore: clock.Unix(), expiresAt: clock.Unix() + 3600})
	db.mu.Unlock()
	failRegistry(db, false)
	*clock = clock.Add(revocationRetryInterval)

	denied, err := denyCheck(list, claims)
	if err != nil {
		t.Fatalf("the list did not recover: %v", err)
	}
	if !denied {
		t.Error("the revocation made during the outage was not applied after it")
	}
}

// A registry that is down must not mean a query per request, even with every
// request refused.
func TestRevocationList_doesNotRetryOnEveryRequestWhileTheRegistryIsDown(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix()}
	failRegistry(db, true)
	if _, err := denyCheck(list, claims); err == nil {
		t.Fatal("a failing registry was answered from")
	}
	before := selectsOf(db)

	for i := 0; i < 50; i++ {
		if _, err := denyCheck(list, claims); !errors.Is(err, ErrRevocationsUnavailable) {
			t.Fatalf("request %d: err = %v", i, err)
		}
	}
	if extra := selectsOf(db) - before; extra != 0 {
		t.Errorf("%d extra queries for 50 requests inside the retry interval", extra)
	}

	*clock = clock.Add(revocationRetryInterval)
	_, _ = denyCheck(list, claims)
	if extra := selectsOf(db) - before; extra != 1 {
		t.Errorf("%d queries once the retry interval passed, want 1", extra)
	}
}

// The registry client may ignore the context it is given. The reload's
// deadline must hold anyway, or every stale request blocks forever.
func TestRevocationList_aHungReloadReleasesItsWaitersAtTheDeadline(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if _, err := denyCheck(list, claims); err != nil {
		t.Fatalf("first load: %v", err)
	}
	loadedAt := list.lastRefresh
	list.reloadTimeout = 100 * time.Millisecond

	// The row the hung read would return, were its result ever applied.
	db.mu.Lock()
	db.rows = append(db.rows, revocation{subject: "ak_key:ns", issuedBefore: clock.Unix(), expiresAt: clock.Unix() + 3600})
	release := make(chan struct{})
	db.onSelect = func() { <-release } // ignores the context, like the gorqlite client
	db.mu.Unlock()
	*clock = clock.Add(RevocationStaleness + time.Second)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = denyCheck(list, claims) }(i)
	}
	waitOrFail(t, &wg, 3*time.Second, "requests were still waiting on a hung reload long after its deadline")
	for i, err := range errs {
		if !errors.Is(err, ErrRevocationsUnavailable) {
			t.Errorf("waiter %d: err = %v, want ErrRevocationsUnavailable", i, err)
		}
	}

	close(release)
	db.mu.Lock() // returns once the abandoned read has finished
	db.mu.Unlock()
	list.mu.RLock()
	defer list.mu.RUnlock()
	if !list.lastRefresh.Equal(loadedAt) || len(list.bySubject) != 0 {
		t.Error("the abandoned read's late result was applied")
	}
	if list.flight != nil {
		t.Error("the hung flight was never cleared")
	}
}

// A read that finishes after a newer one has been applied answers from an
// older moment, and must not put the older list back.
func TestRevocationList_aSlowerOlderReadDoesNotOverwriteANewerList(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	ctx := context.Background()

	snapshotted := make(chan struct{})
	release := make(chan struct{})
	db.mu.Lock()
	db.afterSnapshot = func() { close(snapshotted); <-release }
	db.mu.Unlock()
	older := make(chan error, 1)
	go func() { older <- list.Refresh(ctx) }()
	<-snapshotted // the older read holds a snapshot with no revocation in it

	db.mu.Lock()
	db.afterSnapshot = nil
	db.rows = append(db.rows, revocation{subject: "ak_key:ns", issuedBefore: clock.Unix(), expiresAt: clock.Unix() + 3600})
	db.mu.Unlock()
	if err := list.Refresh(ctx); err != nil {
		t.Fatalf("newer Refresh: %v", err)
	}

	close(release)
	if err := <-older; err != nil {
		t.Fatalf("older Refresh: %v", err)
	}
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if !mustDenies(list, claims, []string{"ak_key:ns"}) {
		t.Error("an older read replaced the newer list, dropping a revocation")
	}
}

// A reload whose SELECT began before a local insert committed would replace the
// maps without it; the gateway that recorded the revocation must keep honouring
// it.
func TestRevocationList_aLocalInsertSurvivesAReloadInFlight(t *testing.T) {
	list, db, clock := newTestRevocations(t)

	snapshotted := make(chan struct{})
	release := make(chan struct{})
	db.mu.Lock()
	db.afterSnapshot = func() { close(snapshotted); <-release }
	db.mu.Unlock()
	reloaded := make(chan error, 1)
	go func() { reloaded <- list.Refresh(context.Background()) }()
	<-snapshotted

	db.mu.Lock()
	db.afterSnapshot = nil
	db.mu.Unlock()
	if err := list.RevokeToken(context.Background(), "jti-1", clock.Unix()+3600, "test"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	close(release)
	if err := <-reloaded; err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if !mustDenies(list, &JWTClaims{Sub: "0xa", Jti: "jti-1"}, nil) {
		t.Error("the reload dropped a revocation this gateway had just recorded")
	}
}

// A local entry older than a completed read is in the table by then, and is
// not carried forward for ever.
func TestRevocationList_localInsertsAreDroppedOnceAReadCoversThem(t *testing.T) {
	list, _, clock := newTestRevocations(t)
	if err := list.RevokeToken(context.Background(), "jti-1", clock.Unix()+3600, "test"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	*clock = clock.Add(time.Second)
	if err := list.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	list.mu.RLock()
	defer list.mu.RUnlock()
	if len(list.local) != 0 {
		t.Errorf("%d local entries kept after a read that began after them", len(list.local))
	}
	if _, ok := list.byJTI["jti-1"]; !ok {
		t.Error("the revocation is not in the list the read produced")
	}
}
