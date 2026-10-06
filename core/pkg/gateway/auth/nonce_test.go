package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// A wallet signature is only a single login if the gateway consumes the
// challenge it issued. These tests lock that contract in: a nonce works once,
// and every other case (replay, expiry, unknown, unknown namespace) is refused.

// ----------------------------------------------------------------------------
// Mock plumbing
// ----------------------------------------------------------------------------

// nonceRow models one row of the nonces table.
type nonceRow struct {
	namespaceID int64
	wallet      string
	nonce       string
	expiresAt   time.Time
	usedAt      *time.Time
}

// nonceMockStore backs both the namespace SELECT and the consuming UPDATE so a
// test can drive the whole path against one piece of state.
type nonceMockStore struct {
	mu sync.Mutex

	namespaceIDs map[string]int64 // namespace name -> id
	rows         []*nonceRow

	// execErr, when set, is returned by the next Exec — used to prove a
	// registry outage is reported as transient rather than as a bad challenge.
	execErr error

	// lastUpdateSQL records the statement used for consumption so a test can
	// assert the predicates that carry the security property are still there.
	lastUpdateSQL string

	// namespaceWrites counts INSERTs against namespaces. Consuming a challenge
	// must never create one.
	namespaceWrites int
}

func (m *nonceMockStore) addNamespace(name string, id int64) {
	if m.namespaceIDs == nil {
		m.namespaceIDs = map[string]int64{}
	}
	m.namespaceIDs[name] = id
}

func (m *nonceMockStore) addNonce(nsID int64, wallet, nonce string, expiresIn time.Duration) {
	m.rows = append(m.rows, &nonceRow{
		namespaceID: nsID,
		wallet:      wallet,
		nonce:       nonce,
		expiresAt:   time.Now().Add(expiresIn),
	})
}

func (m *nonceMockStore) rowFor(wallet, nonce string) *nonceRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.wallet == wallet && r.nonce == nonce {
			return r
		}
	}
	return nil
}

// nonceMockOrm serves the read-only namespace lookup.
type nonceMockOrm struct {
	client.NetworkClient
	db *nonceMockOrmDB
}

func (m *nonceMockOrm) Database() client.DatabaseClient { return m.db }

type nonceMockOrmDB struct {
	client.DatabaseClient
	store *nonceMockStore
}

func (m *nonceMockOrmDB) Query(_ context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	if containsCI(query, "INSERT") && containsCI(query, "namespaces") {
		m.store.namespaceWrites++
		return &client.QueryResult{Count: 1}, nil
	}
	if containsCI(query, "SELECT id FROM namespaces") {
		if len(args) < 1 {
			return &client.QueryResult{Count: 0}, nil
		}
		name, _ := args[0].(string)
		if id, ok := m.store.namespaceIDs[name]; ok {
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{id}}}, nil
		}
		return &client.QueryResult{Count: 0}, nil
	}
	return &client.QueryResult{Count: 0}, nil
}

// nonceMockRqlite performs the consuming UPDATE, modelling the predicates the
// real statement relies on: a row matches only while it is unused and unexpired.
type nonceMockRqlite struct {
	rqlite.Client
	store *nonceMockStore
}

func (m *nonceMockRqlite) Exec(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	m.store.mu.Lock()
	defer m.store.mu.Unlock()

	if m.store.execErr != nil {
		return nil, m.store.execErr
	}
	if !containsCI(query, "UPDATE nonces") {
		return &nonceFakeResult{}, nil
	}
	m.store.lastUpdateSQL = query

	if len(args) < 3 {
		return &nonceFakeResult{}, nil
	}
	nsID, _ := args[0].(int64)
	wallet, _ := args[1].(string)
	nonce, _ := args[2].(string)

	// Apply only the predicates the statement actually carries, so that
	// dropping one from the real SQL changes what these tests observe rather
	// than being silently compensated for by the mock.
	enforcesSingleUse := containsCI(query, "used_at IS NULL")
	enforcesExpiry := containsCI(query, "expires_at > datetime('now')")

	now := time.Now()
	for _, r := range m.store.rows {
		if r.namespaceID != nsID || r.wallet != wallet || r.nonce != nonce {
			continue
		}
		if enforcesSingleUse && r.usedAt != nil {
			return &nonceFakeResult{}, nil
		}
		if enforcesExpiry && !r.expiresAt.After(now) {
			return &nonceFakeResult{}, nil
		}
		r.usedAt = &now
		return &nonceFakeResult{affected: 1}, nil
	}
	return &nonceFakeResult{}, nil
}

type nonceFakeResult struct{ affected int64 }

func (r *nonceFakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r *nonceFakeResult) RowsAffected() (int64, error) { return r.affected, nil }

// newNonceTestService wires a Service against the store, with the rqlite client
// present so consumption is atomic.
func newNonceTestService(t *testing.T, store *nonceMockStore) *Service {
	t.Helper()
	svc, err := NewService(nil, &nonceMockOrm{db: &nonceMockOrmDB{store: store}}, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetRqliteClient(&nonceMockRqlite{store: store})
	return svc
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestConsumeNonce_ClaimsAFreshChallengeOnce(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)
	svc := newNonceTestService(t, store)

	if err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default"); err != nil {
		t.Fatalf("first claim should succeed, got %v", err)
	}
	if row := store.rowFor("0xwallet", "nonce-1"); row == nil || row.usedAt == nil {
		t.Fatal("claiming a challenge must mark it used")
	}
}

// The defect this whole change exists for: a captured (wallet, nonce,
// signature) tuple must not be a permanent credential.
func TestConsumeNonce_RejectsReplay(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)
	svc := newNonceTestService(t, store)

	if err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default"); err != nil {
		t.Fatalf("first claim should succeed, got %v", err)
	}
	err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default")
	if !errors.Is(err, ErrNonceInvalid) {
		t.Fatalf("replay must be rejected with ErrNonceInvalid, got %v", err)
	}
}

func TestConsumeNonce_RejectsExpiredChallenge(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "stale", -1*time.Second)
	svc := newNonceTestService(t, store)

	err := svc.ConsumeNonce(context.Background(), "0xwallet", "stale", "default")
	if !errors.Is(err, ErrNonceInvalid) {
		t.Fatalf("expired challenge must be rejected, got %v", err)
	}
}

func TestConsumeNonce_RejectsChallengeItNeverIssued(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	svc := newNonceTestService(t, store)

	err := svc.ConsumeNonce(context.Background(), "0xwallet", "invented", "default")
	if !errors.Is(err, ErrNonceInvalid) {
		t.Fatalf("unknown challenge must be rejected, got %v", err)
	}
}

// A failed authentication must not leave a namespace row behind, otherwise the
// endpoint becomes a way to create namespaces anonymously.
func TestConsumeNonce_RejectsUnknownNamespaceWithoutCreatingIt(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	svc := newNonceTestService(t, store)

	err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "victim-namespace")
	if !errors.Is(err, ErrNonceInvalid) {
		t.Fatalf("challenge for an unknown namespace must be rejected, got %v", err)
	}
	if store.namespaceWrites != 0 {
		t.Fatalf("consuming a challenge must not create namespaces, saw %d writes", store.namespaceWrites)
	}
}

// Without the lower-level client there is no affected-row count, so single use
// cannot be guaranteed. Refusing is the point: degrading to a non-atomic update
// would silently reopen the replay hole.
func TestConsumeNonce_RefusesWithoutAtomicSingleUse(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)

	svc, err := NewService(nil, &nonceMockOrm{db: &nonceMockOrmDB{store: store}}, "", "default")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// SetRqliteClient deliberately not called.

	err = svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default")
	if !errors.Is(err, ErrNonceConsumeNotConfigured) {
		t.Fatalf("expected ErrNonceConsumeNotConfigured, got %v", err)
	}
	if row := store.rowFor("0xwallet", "nonce-1"); row != nil && row.usedAt != nil {
		t.Fatal("a refused claim must not consume the challenge")
	}
}

// A registry outage is not an authentication failure; reporting it as one would
// tell a caller their signature was bad when it was fine.
func TestConsumeNonce_ReportsRegistryOutageAsTransient(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)
	store.execErr = errors.New("leader unavailable")
	svc := newNonceTestService(t, store)

	err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default")
	if !errors.Is(err, ErrNonceTransient) {
		t.Fatalf("expected ErrNonceTransient, got %v", err)
	}
	if errors.Is(err, ErrNonceInvalid) {
		t.Fatal("a registry outage must not be reported as an invalid challenge")
	}
}

// Wallets arrive checksummed on one call and lowercase on the next. Issue and
// claim must agree, or every login with a checksummed address breaks.
func TestConsumeNonce_MatchesWalletRegardlessOfCase(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xabcdef", "nonce-1", 5*time.Minute)
	svc := newNonceTestService(t, store)

	if err := svc.ConsumeNonce(context.Background(), "  0xAbCdEf  ", "nonce-1", "default"); err != nil {
		t.Fatalf("checksummed wallet should claim the same challenge, got %v", err)
	}
}

// CreateNonce files a challenge under nonceNamespace(); ConsumeNonce must look
// in the same place or a freshly issued challenge is unclaimable.
func TestConsumeNonce_UsesTheSameNamespaceDefaultAsIssuing(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("fallback-ns", 7)
	store.addNonce(7, "0xwallet", "nonce-1", 5*time.Minute)

	svc, err := NewService(nil, &nonceMockOrm{db: &nonceMockOrmDB{store: store}}, "", "fallback-ns")
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc.SetRqliteClient(&nonceMockRqlite{store: store})

	// An empty namespace must resolve to the service default on both sides.
	if got := svc.nonceNamespace(""); got != "fallback-ns" {
		t.Fatalf("nonceNamespace(\"\") = %q, want the service default", got)
	}
	if err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", ""); err != nil {
		t.Fatalf("empty namespace should claim from the service default, got %v", err)
	}
}

// Two requests racing with the same challenge: exactly one may win.
func TestConsumeNonce_ConcurrentClaimsYieldExactlyOneWinner(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)
	svc := newNonceTestService(t, store)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default")
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrNonceInvalid):
		default:
			t.Fatalf("unexpected error from concurrent claim: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", winners)
	}
}

// The security property lives in the WHERE clause. If either predicate is ever
// dropped the challenge stops being single-use or stops expiring, so pin them.
func TestConsumeNonce_StatementKeepsSingleUseAndExpiryPredicates(t *testing.T) {
	store := &nonceMockStore{}
	store.addNamespace("default", 1)
	store.addNonce(1, "0xwallet", "nonce-1", 5*time.Minute)
	svc := newNonceTestService(t, store)

	if err := svc.ConsumeNonce(context.Background(), "0xwallet", "nonce-1", "default"); err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	stmt := strings.Join(strings.Fields(store.lastUpdateSQL), " ")
	for _, predicate := range []string{"used_at IS NULL", "expires_at > datetime('now')"} {
		if !strings.Contains(stmt, predicate) {
			t.Fatalf("consuming statement lost the %q predicate: %s", predicate, stmt)
		}
	}
}
