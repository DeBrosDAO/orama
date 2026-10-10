package auth

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hangRegistry makes every SELECT block until the returned func is called, the
// way a registry client that ignores its context does, and counts the reads
// that reach it. The fake holds its own lock while blocked, so the count is
// kept outside it.
func hangRegistry(db *revocationDB) (release func(), reads *int32) {
	gate := make(chan struct{})
	var once sync.Once
	reads = new(int32)
	db.mu.Lock()
	db.onSelect = func() { atomic.AddInt32(reads, 1); <-gate }
	db.mu.Unlock()
	return func() { once.Do(func() { close(gate) }) }, reads
}

// loadedPastTheBound is a list that has read the table once and whose copy is
// now older than RevocationStaleness.
func loadedPastTheBound(t *testing.T) (*RevocationList, *revocationDB, *time.Time, *JWTClaims) {
	t.Helper()
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	mustDenies(list, claims, []string{"ak_key:ns"})
	*clock = clock.Add(RevocationStaleness + time.Second)
	return list, db, clock, claims
}

// A read abandoned at its deadline keeps running. Under a registry that never
// answers, every later attempt used to start another, leaking a goroutine and
// a connection per attempt for as long as the hang lasted.
func TestRevocationList_aHungRegistryLeavesAtMostOneReader(t *testing.T) {
	list, db, clock, claims := loadedPastTheBound(t)
	list.reloadTimeout = 20 * time.Millisecond
	release, reads := hangRegistry(db)
	defer release()

	for i := 0; i < 8; i++ {
		*clock = clock.Add(revocationRetryInterval) // past the retry limit: each attempt is allowed to reload
		if _, err := denyCheck(list, claims); !errors.Is(err, ErrRevocationsUnavailable) {
			t.Fatalf("attempt %d: err = %v, want ErrRevocationsUnavailable", i, err)
		}
		waitForReloadToEnd(t, list)
	}
	if got := atomic.LoadInt32(reads); got != 1 {
		t.Fatalf("%d reads reached the registry during 8 attempts under a hang, want 1", got)
	}

	// Once the hung read returns the list reads again.
	release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list.mu.RLock()
		stuck := list.abandoned
		list.mu.RUnlock()
		if !stuck {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the abandoned read never released its slot")
		}
		time.Sleep(time.Millisecond)
	}
	*clock = clock.Add(revocationRetryInterval)
	if _, err := denyCheck(list, claims); err != nil {
		t.Fatalf("the list did not recover after the hung read returned: %v", err)
	}
}

// The text a client can be shown must not carry why the registry failed: it
// names internal addresses.
func TestRevocationList_theUnavailableErrorNeverCarriesTheRegistrysError(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	db.mu.Lock()
	db.failText = "dial tcp 10.0.0.7:4001: connect: connection refused"
	db.mu.Unlock()
	failRegistry(db, true)

	for name, check := range map[string]func() error{
		"never loaded": func() error { _, err := denyCheck(list, claims); return err },
		"DeniesSubject": func() error {
			_, err := list.DeniesSubject("ak_key:ns")
			return err
		},
	} {
		err := check()
		if !errors.Is(err, ErrRevocationsUnavailable) {
			t.Fatalf("%s: err = %v, want ErrRevocationsUnavailable", name, err)
		}
		if strings.Contains(err.Error(), "10.0.0.7") || strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: the error a client may see carries the registry's error: %q", name, err)
		}
	}
}

// A request whose copy is inside the bound is answered from it, at once,
// however long the registry takes: the starter of the reload included.
func TestRevocationList_aRequestInsideTheBoundNeverWaitsForAHungRegistry(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	mustDenies(list, claims, []string{"ak_key:ns"})
	*clock = clock.Add(RevocationRefreshInterval + time.Second) // stale, inside the bound
	list.reloadTimeout = time.Minute                            // only not waiting can make this return
	release, _ := hangRegistry(db)
	defer release()

	done := make(chan error, 1)
	go func() { _, err := denyCheck(list, claims); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a copy inside the bound was refused: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a request whose copy was inside the bound waited for the registry")
	}
}

// A copy past the bound waits for the registry, and for no longer than one
// read is allowed to take.
func TestRevocationList_aRequestPastTheBoundWaitsNoLongerThanTheReadTimeout(t *testing.T) {
	list, db, _, claims := loadedPastTheBound(t)
	list.reloadTimeout = 50 * time.Millisecond
	release, _ := hangRegistry(db)
	defer release()

	start := time.Now()
	_, err := denyCheck(list, claims)
	if !errors.Is(err, ErrRevocationsUnavailable) {
		t.Fatalf("err = %v, want ErrRevocationsUnavailable", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the request waited %s on a hung registry, want about the read timeout", took)
	}
}

// The read timeout is what a request past the bound waits, so it must be well
// inside the bound itself, and end before the next reload is due.
func TestRevocationReloadTimeout_isShorterThanTheBoundAndTheInterval(t *testing.T) {
	if revocationReloadTimeout >= RevocationStaleness || revocationReloadTimeout >= RevocationRefreshInterval {
		t.Errorf("read timeout %s must be under the bound %s and the refresh interval %s",
			revocationReloadTimeout, RevocationStaleness, RevocationRefreshInterval)
	}
}

// Usable answers from the copy and never starts or waits for a reload.
func TestRevocationList_usableNeverTouchesTheRegistry(t *testing.T) {
	var nilList *RevocationList
	if nilList.Usable() {
		t.Error("a nil list reported itself usable")
	}
	list, db, clock := newTestRevocations(t)
	if list.Usable() {
		t.Error("a list never loaded reported itself usable")
	}
	mustDenies(list, &JWTClaims{Sub: "x"}, []string{"x"})
	before := selectsOf(db)
	if !list.Usable() {
		t.Error("a fresh list was not usable")
	}
	*clock = clock.Add(RevocationStaleness + time.Second)
	if list.Usable() {
		t.Error("a list past the bound reported itself usable")
	}
	if selectsOf(db) != before {
		t.Error("Usable read the registry")
	}
}
