package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// gatedKeyReads is the real SQLite registry with every read of the published
// keys counted and, while gate is set, held until it is closed.
type gatedKeyReads struct {
	*sqliteDatabase
	reads atomic.Int32
	gate  atomic.Pointer[chan struct{}]
}

func (g *gatedKeyReads) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	if strings.Contains(query, "FROM signing_keys") && strings.HasPrefix(strings.TrimSpace(query), "SELECT") {
		g.reads.Add(1)
		if ch := g.gate.Load(); ch != nil {
			<-*ch
		}
	}
	return g.sqliteDatabase.Query(ctx, query, args...)
}

// gatedSigningKeys is a set loaded once at clock, over gatedKeyReads.
func gatedSigningKeys(t *testing.T) (*SigningKeys, *gatedKeyReads, *time.Time, SigningKey) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := rqlite.ApplyEmbeddedMigrations(t.Context(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	reads := &gatedKeyReads{sqliteDatabase: &sqliteDatabase{db: db}}
	keys := NewSigningKeys(func() client.DatabaseClient { return reads }, nil)
	clock := time.Now()
	keys.now = func() time.Time { return clock } // set before any reload goroutine starts
	pub, _ := newKey(t)
	key := SigningKey{KID: KeyIDFor(pub), Namespace: "acme", Public: pub}
	if err := keys.Publish(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := keys.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	reads.reads.Store(0)
	return keys, reads, &clock, key
}

func hold(g *gatedKeyReads) chan struct{} {
	ch := make(chan struct{})
	g.gate.Store(&ch)
	return ch
}

// Verifications arriving once the set is due for a reload are answered from it
// while one reload runs: they used to make a blocking read each, and every
// authenticated request stalled together for seconds (stagenet 2026-10-04).
func TestSigningKeys_aDueReloadDoesNotHoldVerifications(t *testing.T) {
	keys, reads, clock, key := gatedSigningKeys(t)
	*clock = clock.Add(signingKeyReloadInterval + time.Second)
	release := hold(reads)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := keys.Lookup(key.KID); !ok {
				t.Error("a known key was refused while a reload ran")
			}
		}()
	}
	waitOrFail(t, &wg, 2*time.Second, "verifications waited for a reload although the set was within its staleness bound")
	close(release)
	waitForFlight(t, keys)
	if got := reads.reads.Load(); got != 1 {
		t.Fatalf("registry reads = %d, want 1 for 50 verifications", got)
	}
}

// A set past signingKeyStaleness does not answer: a retired key must stop
// working within that bound, so the verification waits for the reload.
func TestSigningKeys_aSetPastItsBoundWaitsForTheReload(t *testing.T) {
	keys, reads, clock, key := gatedSigningKeys(t)
	*clock = clock.Add(signingKeyStaleness + time.Second)
	release := hold(reads)

	answered := make(chan bool, 1)
	go func() { _, ok := keys.Lookup(key.KID); answered <- ok }()
	select {
	case <-answered:
		t.Fatal("a set past its staleness bound answered without reloading")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case ok := <-answered:
		if !ok {
			t.Fatal("the key was refused after the reload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the verification never resumed after the reload")
	}
}

func TestSigningKeys_aFreshSetReadsNothing(t *testing.T) {
	keys, reads, _, key := gatedSigningKeys(t)
	if _, ok := keys.Lookup(key.KID); !ok {
		t.Fatal("a known key was refused")
	}
	if got := reads.reads.Load(); got != 0 {
		t.Fatalf("a fresh set read the registry %d times", got)
	}
}

// waitForFlight waits for the background reload to finish, so a test does not
// return while it still reads the registry.
func waitForFlight(t *testing.T, keys *SigningKeys) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		keys.mu.RLock()
		f := keys.flight
		keys.mu.RUnlock()
		if f == nil {
			return
		}
		<-f.done
	}
	t.Fatal("the background reload never finished")
}

// A failed read does not make the set fresh: the next verification past the
// retry interval tries again, rather than trusting a set nobody could read
// for another reload interval.
func TestSigningKeys_aFailedReadDoesNotFreshenTheSet(t *testing.T) {
	keys, reads, clock, key := gatedSigningKeys(t)
	keys.mu.RLock()
	loaded := keys.loadedAt
	keys.mu.RUnlock()
	_ = reads.sqliteDatabase.db.Close() // every read fails from here
	*clock = clock.Add(signingKeyReloadInterval + time.Second)
	keys.Lookup(key.KID)
	waitForFlight(t, keys)

	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if !keys.loadedAt.Equal(loaded) {
		t.Fatalf("a failed read moved loadedAt from %v to %v", loaded, keys.loadedAt)
	}
	if keys.lastAttempt.Before(*clock) {
		t.Fatal("the failed read was not recorded as an attempt")
	}
}

// A registry that does not answer holds a reload only until its deadline, and
// one hung read at a time: the client ignores the context, and a hung read
// used to hold every verification waiting on the reload for as long as it hung.
func TestSigningKeys_aHungReadIsAbandonedAtItsDeadline(t *testing.T) {
	keys, reads, _, _ := gatedSigningKeys(t)
	release := hold(reads)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := keys.Reload(ctx); err == nil {
		t.Fatal("a read the registry never answered was reported loaded")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the reload waited %s for a hung registry, want about its 100ms deadline", took)
	}

	before := reads.reads.Load()
	if err := keys.Reload(context.Background()); !errors.Is(err, errKeyReadStillRunning) {
		t.Fatalf("a second read while the first still hangs: %v, want errKeyReadStillRunning", err)
	}
	if reads.reads.Load() != before {
		t.Fatal("a second read was sent while the first still hung")
	}

	close(release)
	reads.gate.Store(nil)
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := keys.Reload(context.Background())
		if err == nil {
			break
		}
		if !errors.Is(err, errKeyReadStillRunning) || time.Now().After(deadline) {
			t.Fatalf("reads did not resume once the registry answered: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
