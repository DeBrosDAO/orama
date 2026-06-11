package auth

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Bug #68 / RFC 9700 §4.12: every /v1/auth/refresh call must atomically
// rotate the refresh token. These tests lock that contract in.

// ----------------------------------------------------------------------------
// Mock plumbing
// ----------------------------------------------------------------------------

// rotationMockOrm provides the SELECT path for refresh-token rotation:
// the first read returns the subject of the supplied refresh token.
type rotationMockOrm struct {
	client.NetworkClient
	db *rotationMockORMDB
}

func (m *rotationMockOrm) Database() client.DatabaseClient { return m.db }

type rotationMockORMDB struct {
	client.DatabaseClient
	mu             sync.Mutex
	subjectByToken map[string]string // hashedToken -> subject (nil/missing = "invalid")
	inserted       int               // count of INSERTs (new refresh-token rows)
	subjects       map[string]string // subject -> last hashed token inserted
}

func (m *rotationMockORMDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// ResolveNamespaceID call — return synthetic ns id.
	if containsCI(sql, "namespaces") && containsCI(sql, "INSERT OR IGNORE") {
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{{int64(1)}}}, nil
	}
	if containsCI(sql, "SELECT id FROM namespaces") {
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{{int64(1)}}}, nil
	}
	// SELECT subject for the refresh-token lookup.
	if containsCI(sql, "SELECT subject FROM refresh_tokens") {
		if len(args) < 2 {
			return &client.QueryResult{Count: 0}, nil
		}
		hashedTok, _ := args[1].(string)
		if subj, ok := m.subjectByToken[hashedTok]; ok && subj != "" {
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{subj}}}, nil
		}
		return &client.QueryResult{Count: 0}, nil
	}
	// INSERT new refresh_tokens row.
	if containsCI(sql, "INSERT INTO refresh_tokens") {
		m.inserted++
		if len(args) >= 3 {
			subj, _ := args[1].(string)
			hashedTok, _ := args[2].(string)
			if m.subjects == nil {
				m.subjects = map[string]string{}
			}
			m.subjects[subj] = hashedTok
			// Make the new row queryable for follow-on tests (e.g. happy path).
			if m.subjectByToken == nil {
				m.subjectByToken = map[string]string{}
			}
			m.subjectByToken[hashedTok] = subj
		}
		return &client.QueryResult{Count: 1}, nil
	}
	return &client.QueryResult{Count: 0}, nil
}

// rotationMockRqlite is the lower-level client used for the CAS UPDATE.
// Returns programmable RowsAffected so tests can simulate "we won the CAS"
// (rowsAffected=1) vs "we lost the race" (rowsAffected=0).
type rotationMockRqlite struct {
	rqlite.Client // embed; calling un-implemented methods panics — fine for tests

	mu                sync.Mutex
	revokedTokens     map[string]bool // hashed token -> revoked
	updateCalls       int
	rowsAffectedNext  []int64 // programmable per-call values; pop from front. Defaults to "revoke if unrevoked".
	execErrNext       []error // programmable per-call errors
	parallelExecGuard sync.Mutex
}

func (m *rotationMockRqlite) Exec(_ context.Context, sql string, args ...interface{}) (sql.Result, error) {
	// Simulate single-writer serialization (rqlite Raft serializes writes).
	m.parallelExecGuard.Lock()
	defer m.parallelExecGuard.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++

	// Pop programmable error first
	if len(m.execErrNext) > 0 {
		e := m.execErrNext[0]
		m.execErrNext = m.execErrNext[1:]
		if e != nil {
			return nil, e
		}
	}

	// Default UPDATE behavior: matches if token is currently unrevoked.
	if containsCI(sql, "UPDATE refresh_tokens SET revoked_at") && len(args) >= 2 {
		hashedTok, _ := args[1].(string)
		if m.revokedTokens == nil {
			m.revokedTokens = map[string]bool{}
		}
		var affected int64
		if len(m.rowsAffectedNext) > 0 {
			affected = m.rowsAffectedNext[0]
			m.rowsAffectedNext = m.rowsAffectedNext[1:]
			if affected == 1 {
				m.revokedTokens[hashedTok] = true
			}
		} else if !m.revokedTokens[hashedTok] {
			m.revokedTokens[hashedTok] = true
			affected = 1
		} else {
			affected = 0
		}
		return &rotationFakeResult{affected: affected}, nil
	}

	return &rotationFakeResult{affected: 0}, nil
}

type rotationFakeResult struct{ affected int64 }

func (r *rotationFakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r *rotationFakeResult) RowsAffected() (int64, error) { return r.affected, nil }

// containsCI is a tiny case-insensitive substring check; keeps the mock
// independent of strings package quirks.
func containsCI(s, substr string) bool {
	return indexCI(s, substr) >= 0
}

func indexCI(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			a, b := s[i+j], substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func newRotationTestService(t *testing.T) (*Service, *rotationMockORMDB, *rotationMockRqlite) {
	t.Helper()
	s := createDualKeyService(t)
	ormDB := &rotationMockORMDB{
		subjectByToken: map[string]string{},
	}
	s.orm = &rotationMockOrm{db: ormDB}
	rqliteMock := &rotationMockRqlite{
		revokedTokens: map[string]bool{},
	}
	s.SetRqliteClient(rqliteMock)
	return s, ormDB, rqliteMock
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestRefreshToken_HappyPath_rotatesAndReturnsNewToken(t *testing.T) {
	s, ormDB, rq := newRotationTestService(t)

	// Pre-seed: a valid refresh token for "0xWALLET" in "anchat-test".
	const oldRefresh = "old-refresh-token"
	ormDB.subjectByToken[sha256Hex(oldRefresh)] = "0xWALLET"

	access, newRefresh, subj, exp, err := s.RefreshToken(context.Background(), oldRefresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if access == "" {
		t.Error("access token empty")
	}
	if newRefresh == "" {
		t.Error("new refresh token empty")
	}
	if newRefresh == oldRefresh {
		t.Error("refresh token NOT rotated — same value returned (RFC 9700 §4.12 violation)")
	}
	if subj != "0xWALLET" {
		t.Errorf("subject = %q, want %q", subj, "0xWALLET")
	}
	if exp <= 0 {
		t.Errorf("expiration not set: %d", exp)
	}

	// The old token's CAS should have been won, so the mock recorded it revoked.
	if !rq.revokedTokens[sha256Hex(oldRefresh)] {
		t.Error("old refresh token not marked revoked after rotation")
	}
	// And a new INSERT happened.
	if ormDB.inserted != 1 {
		t.Errorf("expected 1 INSERT for new refresh token, got %d", ormDB.inserted)
	}
}

func TestRefreshToken_CASLost_returnsReplayError(t *testing.T) {
	// Simulates: SELECT sees the token as valid, but the UPDATE matches 0
	// rows (a concurrent caller rotated it in between, or it was already
	// revoked under our feet). MUST return ErrRefreshTokenReplay so the
	// handler can log a security event and return 401.
	s, ormDB, rq := newRotationTestService(t)

	const stolen = "stolen-refresh-token"
	ormDB.subjectByToken[sha256Hex(stolen)] = "0xVICTIM"

	// Force the next UPDATE to claim "0 rows affected" — race lost.
	rq.rowsAffectedNext = []int64{0}

	_, _, _, _, err := s.RefreshToken(context.Background(), stolen, "anchat-test")
	if !errors.Is(err, ErrRefreshTokenReplay) {
		t.Fatalf("err = %v, want ErrRefreshTokenReplay", err)
	}

	// And no new INSERT happened — we bailed before minting.
	if ormDB.inserted != 0 {
		t.Errorf("expected 0 INSERTs after CAS loss, got %d", ormDB.inserted)
	}
}

func TestRefreshToken_InvalidToken_returnsAuthError(t *testing.T) {
	// No row exists for this token — SELECT returns 0 rows.
	s, _, _ := newRotationTestService(t)

	_, _, _, _, err := s.RefreshToken(context.Background(), "never-existed", "anchat-test")
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if errors.Is(err, ErrRefreshTokenReplay) {
		t.Error("invalid token must NOT be classified as replay (distinguishable error)")
	}
	if errors.Is(err, ErrRotationNotConfigured) {
		t.Error("invalid token must NOT surface as ErrRotationNotConfigured")
	}
}

func TestRefreshToken_NoRqliteClient_refusesToRotate(t *testing.T) {
	// A service constructed without SetRqliteClient cannot guarantee
	// atomicity. It MUST refuse rather than rotate non-atomically.
	s := createDualKeyService(t) // mockDatabaseClient via shared helper; no rqlite injected

	_, _, _, _, err := s.RefreshToken(context.Background(), "anything", "anchat-test")
	if !errors.Is(err, ErrRotationNotConfigured) {
		t.Fatalf("err = %v, want ErrRotationNotConfigured", err)
	}
}

// TestRefreshToken_ConcurrentRotation simulates two concurrent refresh
// attempts on the same stolen-or-shared token. Exactly ONE must succeed;
// the other must return ErrRefreshTokenReplay. This is the RFC 9700
// theft-detection tripwire in action.
func TestRefreshToken_ConcurrentRotation_exactlyOneWins(t *testing.T) {
	s, ormDB, rq := newRotationTestService(t)

	const sharedToken = "shared-refresh"
	ormDB.subjectByToken[sha256Hex(sharedToken)] = "0xSHARED"

	// 50 racers all calling RefreshToken with the same token.
	const racers = 50
	wins := make(chan error, racers)
	var startWg, endWg sync.WaitGroup
	startWg.Add(1)
	endWg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer endWg.Done()
			startWg.Wait() // launch all goroutines simultaneously
			_, _, _, _, err := s.RefreshToken(context.Background(), sharedToken, "anchat-test")
			wins <- err
		}()
	}
	startWg.Done() // GO
	endWg.Wait()
	close(wins)

	var successes, replays, others int
	for err := range wins {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRefreshTokenReplay):
			replays++
		default:
			others++
			t.Logf("unexpected error class: %v", err)
		}
	}

	// Exactly one winner; everyone else gets the replay tripwire.
	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1 (RFC 9700 theft tripwire)", successes)
	}
	if replays != racers-1 {
		t.Errorf("replays = %d, want %d", replays, racers-1)
	}
	if others != 0 {
		t.Errorf("unexpected error responses = %d", others)
	}

	// Exactly one INSERT for the new refresh token; everyone else bailed
	// before minting.
	if ormDB.inserted != 1 {
		t.Errorf("expected 1 new-token INSERT, got %d", ormDB.inserted)
	}
	// UPDATE was attempted by every racer.
	if rq.updateCalls < racers {
		t.Errorf("expected at least %d UPDATE calls (one per racer), got %d", racers, rq.updateCalls)
	}
}

// TestRefreshToken_RotatedTokenReplayFails — after a successful rotation,
// reusing the OLD refresh token must fail with the standard auth error
// (the SELECT in step 1 sees revoked_at IS NOT NULL → 0 rows).
func TestRefreshToken_RotatedTokenReplayFails(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)

	const oldRefresh = "rotate-me"
	ormDB.subjectByToken[sha256Hex(oldRefresh)] = "0xWALLET"

	// First call rotates successfully.
	_, newRefresh, _, _, err := s.RefreshToken(context.Background(), oldRefresh, "anchat-test")
	if err != nil {
		t.Fatalf("first RefreshToken: %v", err)
	}
	if newRefresh == "" {
		t.Fatal("first rotation produced empty new token")
	}

	// Simulate: the old token's row is now marked revoked, so subsequent
	// SELECTs return 0 rows. The mock approximates this by removing the
	// entry from subjectByToken (real DB would have revoked_at IS NOT NULL).
	delete(ormDB.subjectByToken, sha256Hex(oldRefresh))

	// Try to reuse the rotated-away token.
	_, _, _, _, err = s.RefreshToken(context.Background(), oldRefresh, "anchat-test")
	if err == nil {
		t.Fatal("expected error reusing rotated token, got nil")
	}
}
