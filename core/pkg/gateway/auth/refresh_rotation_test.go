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
	claimsByToken  map[string]string // hashedToken -> custom_claims JSON (bugboard #548)
	// claimsBySubject: subject -> last-known-good custom_claims JSON. Serves the
	// lastKnownCustomClaims lookup (bugboard #143): the anti-fragmentation reuse
	// reads the most recent stored account_id for a wallet at login.
	claimsBySubject map[string]string
	// graceableTokens: hashedToken -> subject for tokens that are revoked but
	// still inside the reuse-grace window (bugboard #125). The grace SELECT
	// (detected by the grace_used_at predicate) reads from here.
	graceableTokens map[string]string
	inserted        int               // count of INSERTs (new refresh-token rows)
	subjects        map[string]string // subject -> last hashed token inserted
	// selectErrRemaining: number of upcoming "SELECT subject" calls that
	// should return selectErr (simulates a transient rqlite leader outage).
	// Decremented per matching call; 0 = serve normally (bugboard #125).
	selectErr           error
	selectErrRemaining  int
	selectAttemptsTaken int
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
	// lastKnownCustomClaims lookup (bugboard #143): SELECT custom_claims by
	// subject. Distinguished from the token-keyed selects by selecting
	// custom_claims (not subject) and keying on subject (args[1]).
	if containsCI(sql, "SELECT custom_claims") && containsCI(sql, "FROM refresh_tokens") {
		if len(args) < 2 {
			return &client.QueryResult{Count: 0}, nil
		}
		subj, _ := args[1].(string)
		if cc, ok := m.claimsBySubject[subj]; ok && cc != "" {
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{cc}}}, nil
		}
		return &client.QueryResult{Count: 0}, nil
	}
	// Grace-path SELECT (bugboard #125): SELECT subject for a recently-revoked,
	// grace-available token. Distinguished from the active-path SELECT by the
	// grace_used_at predicate. Must be checked BEFORE the generic handler.
	if containsCI(sql, "SELECT subject") && containsCI(sql, "FROM refresh_tokens") && containsCI(sql, "grace_used_at") {
		if len(args) < 2 {
			return &client.QueryResult{Count: 0}, nil
		}
		hashedTok, _ := args[1].(string)
		if subj, ok := m.graceableTokens[hashedTok]; ok && subj != "" {
			claims := ""
			if m.claimsByToken != nil {
				claims = m.claimsByToken[hashedTok]
			}
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{subj, claims}}}, nil
		}
		return &client.QueryResult{Count: 0}, nil
	}
	// SELECT subject (+ custom_claims, bugboard #548) for the lookup.
	if containsCI(sql, "SELECT subject") && containsCI(sql, "FROM refresh_tokens") {
		m.selectAttemptsTaken++
		if m.selectErrRemaining > 0 {
			m.selectErrRemaining--
			return nil, m.selectErr
		}
		if len(args) < 2 {
			return &client.QueryResult{Count: 0}, nil
		}
		hashedTok, _ := args[1].(string)
		if subj, ok := m.subjectByToken[hashedTok]; ok && subj != "" {
			claims := ""
			if m.claimsByToken != nil {
				claims = m.claimsByToken[hashedTok]
			}
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{subj, claims}}}, nil
		}
		return &client.QueryResult{Count: 0}, nil
	}
	// RevokeToken UPDATE that ALSO burns the grace slot (bugboard #125
	// logout-bypass fix). Reflect it by clearing the token's grace eligibility
	// so a follow-on grace SELECT misses it. (The rotation grace CAS goes
	// through the rqlite Exec mock, not here, so there's no collision.)
	if containsCI(sql, "UPDATE refresh_tokens") && containsCI(sql, "grace_used_at") && len(args) >= 2 {
		if key, ok := args[1].(string); ok && m.graceableTokens != nil {
			delete(m.graceableTokens, key) // single-token: key is the hashed token
			for tok, subj := range m.graceableTokens {
				if subj == key { // revoke-all: key is the subject
					delete(m.graceableTokens, tok)
				}
			}
		}
		return &client.QueryResult{Count: 1}, nil
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
			// custom_claims is the LAST arg (bugboard #548) — capture it so
			// rotation-propagation tests can assert it carries forward.
			if m.claimsByToken == nil {
				m.claimsByToken = map[string]string{}
			}
			if cc, ok := args[len(args)-1].(string); ok {
				m.claimsByToken[hashedTok] = cc
			}
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
	// graceCASNext: programmable RowsAffected for the grace CAS (UPDATE ... SET
	// grace_used_at). 1 = won the single-use grace; 0 = already consumed
	// (bugboard #125). Defaults to "win once per token".
	graceCASNext  []int64
	graceConsumed map[string]bool
	graceCASCalls int
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

	// Grace CAS (bugboard #125): UPDATE ... SET grace_used_at, single-use.
	if containsCI(sql, "SET grace_used_at") && len(args) >= 2 {
		m.graceCASCalls++
		hashedTok, _ := args[1].(string)
		if m.graceConsumed == nil {
			m.graceConsumed = map[string]bool{}
		}
		var affected int64
		if len(m.graceCASNext) > 0 {
			affected = m.graceCASNext[0]
			m.graceCASNext = m.graceCASNext[1:]
			if affected == 1 {
				m.graceConsumed[hashedTok] = true
			}
		} else if !m.graceConsumed[hashedTok] {
			m.graceConsumed[hashedTok] = true
			affected = 1
		} else {
			affected = 0
		}
		return &rotationFakeResult{affected: affected}, nil
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

// Bugboard #125: a TRANSIENT rqlite error on the lookup (leader briefly
// unavailable during a rolling restart) must surface as ErrRefreshTransient
// (→ 503, retryable) — NOT "invalid or expired" (→ 401, full SIWE re-auth,
// impossible on a locked device answering a VoIP-woken call).
func TestRefreshToken_transientSelectError_returnsTransient(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "valid-but-leader-down"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	// Every lookup attempt across the whole retry window errors.
	ormDB.selectErr = errors.New("rqlite: leadership lost")
	ormDB.selectErrRemaining = 99

	_, _, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if !errors.Is(err, ErrRefreshTransient) {
		t.Fatalf("err = %v, want ErrRefreshTransient (a valid token must not 401 during a leader outage)", err)
	}
}

// The lookup is retried, so a brief blip recovers transparently within one
// refresh call (no client-visible failure at all).
func TestRefreshToken_selectRecoversAfterRetry(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "valid-blips-then-ok"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	ormDB.selectErr = errors.New("rqlite: leadership lost")
	ormDB.selectErrRemaining = refreshSelectRetries - 1 // fail all but the last attempt

	access, newRefresh, subj, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken should recover after transient blips: %v", err)
	}
	if access == "" || newRefresh == "" || subj != "0xWALLET" {
		t.Errorf("recovered refresh incomplete: access=%q newRefresh=%q subj=%q", access, newRefresh, subj)
	}
}

// A transient error on the CAS write (revoke) is also retryable, not a 401.
func TestRefreshToken_transientUpdateError_returnsTransient(t *testing.T) {
	s, ormDB, rq := newRotationTestService(t)
	const refresh = "valid-cas-write-down"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	rq.execErrNext = []error{errors.New("rqlite: write failed, no leader")}

	_, _, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if !errors.Is(err, ErrRefreshTransient) {
		t.Fatalf("err = %v, want ErrRefreshTransient on a transient CAS write error", err)
	}
}

// A genuinely unknown token must remain a hard invalid (401), NOT be masked as
// transient — the distinction is the whole point of the #125 fix.
func TestRefreshToken_unknownToken_isNotTransient(t *testing.T) {
	s, _, _ := newRotationTestService(t)
	_, _, _, _, err := s.RefreshToken(context.Background(), "never-existed", "anchat-test")
	if err == nil {
		t.Fatal("expected error for unknown token")
	}
	if errors.Is(err, ErrRefreshTransient) {
		t.Errorf("unknown token must be a genuine invalid (401), not transient (503): %v", err)
	}
}

// mockClaimsResolver is a fixed claims-provider stand-in for the mint tests.
type mockClaimsResolver struct{ claims map[string]string }

func (m mockClaimsResolver) ResolveClaims(_ context.Context, _, _ string) map[string]string {
	return m.claims
}

// ----------------------------------------------------------------------------
// Bugboard #143 — durable anti-fragmentation: last-known-good custom-claims
// reuse at login. When a namespace's claims provider fails open (transient
// cold-WASM stall) yielding no account_id, that login's push devices would
// register under the wallet Sub instead of the stable account_id and never
// receive fan-out. IssueTokens must reuse the last account_id the wallet ever
// resolved so the device stays on the stable identity.
// ----------------------------------------------------------------------------

// lastKnownCustomClaims returns the most recent stored claims for a subject.
func TestLastKnownCustomClaims_returnsStoredClaims(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	ormDB.claimsBySubject = map[string]string{"0xWALLET": `{"account_id":"uuid-X"}`}

	nsID, err := s.ResolveNamespaceID(context.Background(), "anchat-test")
	if err != nil {
		t.Fatalf("ResolveNamespaceID: %v", err)
	}
	got := s.lastKnownCustomClaims(context.Background(), nsID, "0xWALLET")
	if got["account_id"] != "uuid-X" {
		t.Fatalf("expected last-known account_id, got %v", got)
	}
}

// No stored history → nil (fail-soft, mints without custom claims).
func TestLastKnownCustomClaims_emptyReturnsNil(t *testing.T) {
	s, _, _ := newRotationTestService(t)
	nsID, _ := s.ResolveNamespaceID(context.Background(), "anchat-test")
	if got := s.lastKnownCustomClaims(context.Background(), nsID, "0xNOHISTORY"); got != nil {
		t.Fatalf("expected nil for no history, got %v", got)
	}
}

// The core #143 fix: the provider returns EMPTY at login (transient failure),
// but the wallet has a stored account_id — the minted access token must still
// carry it, and the new refresh row must persist it.
func TestIssueTokens_reusesLastKnownClaims_whenResolverEmpty(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	// Namespace HAS a provider, but it fails open (returns nil this login).
	s.SetClaimsResolver(mockClaimsResolver{claims: nil})
	// The wallet resolved account_id on a PRIOR login (stored in refresh_tokens).
	ormDB.claimsBySubject = map[string]string{"0xWALLET": `{"account_id":"uuid-X"}`}

	access, refresh, _, err := s.IssueTokens(context.Background(), "0xWALLET", "anchat-test")
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	claims, err := s.ParseAndVerifyJWT(access)
	if err != nil {
		t.Fatalf("ParseAndVerifyJWT: %v", err)
	}
	if claims.Custom["account_id"] != "uuid-X" {
		t.Errorf("access token lost account_id after reuse; custom=%v", claims.Custom)
	}
	if got := ormDB.claimsByToken[sha256Hex(refresh)]; got != `{"account_id":"uuid-X"}` {
		t.Errorf("reused claims not persisted to new refresh row; got %q", got)
	}
}

// Guard: with NO provider configured (claimsResolver == nil), the reuse path is
// skipped entirely — single-credential apps are unaffected even if an unrelated
// stored blob exists for the subject.
func TestIssueTokens_noResolver_doesNotReuse(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	// No SetClaimsResolver call → claimsResolver is nil.
	ormDB.claimsBySubject = map[string]string{"0xWALLET": `{"account_id":"uuid-X"}`}

	access, _, _, err := s.IssueTokens(context.Background(), "0xWALLET", "anchat-test")
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	claims, err := s.ParseAndVerifyJWT(access)
	if err != nil {
		t.Fatalf("ParseAndVerifyJWT: %v", err)
	}
	if _, present := claims.Custom["account_id"]; present {
		t.Errorf("no-provider app must not reuse stored claims; custom=%v", claims.Custom)
	}
}

// Guard: when the resolver DOES return claims, reuse must not run (the fresh
// resolution wins). Store a DIFFERENT stale value and assert the fresh one is used.
func TestIssueTokens_resolverNonEmpty_skipsReuse(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	s.SetClaimsResolver(mockClaimsResolver{claims: map[string]string{"account_id": "fresh"}})
	ormDB.claimsBySubject = map[string]string{"0xWALLET": `{"account_id":"stale"}`}

	access, _, _, err := s.IssueTokens(context.Background(), "0xWALLET", "anchat-test")
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	claims, err := s.ParseAndVerifyJWT(access)
	if err != nil {
		t.Fatalf("ParseAndVerifyJWT: %v", err)
	}
	if claims.Custom["account_id"] != "fresh" {
		t.Errorf("fresh resolution must win over stored; custom=%v", claims.Custom)
	}
}

// Bugboard #548: claims resolved at IssueTokens (login) must be stored with the
// refresh token AND replayed into the rotated access token — so account_id
// survives the 15-min refresh without re-invoking the provider.
func TestRefreshToken_propagatesCustomClaims(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	s.SetClaimsResolver(mockClaimsResolver{claims: map[string]string{"account_id": "u-999"}})

	// Login mint — IssueTokens resolves + stores the claims with the refresh row.
	_, refresh, _, err := s.IssueTokens(context.Background(), "0xWALLET", "anchat-test")
	if err != nil {
		t.Fatalf("IssueTokens: %v", err)
	}
	if got := ormDB.claimsByToken[sha256Hex(refresh)]; got != `{"account_id":"u-999"}` {
		t.Fatalf("claims not stored with refresh token; got %q", got)
	}

	// Refresh — the rotated access token must carry account_id, and the NEW
	// refresh row must propagate the stored claims.
	access, newRefresh, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	claims, err := s.ParseAndVerifyJWT(access)
	if err != nil {
		t.Fatalf("ValidateJWT: %v", err)
	}
	if claims.Custom["account_id"] != "u-999" {
		t.Errorf("rotated access token lost account_id; custom=%v", claims.Custom)
	}
	if got := ormDB.claimsByToken[sha256Hex(newRefresh)]; got != `{"account_id":"u-999"}` {
		t.Errorf("rotation did not propagate claims to the new row; got %q", got)
	}

	// Second rotation hop (N+1 → N+2): the claim must survive repeated
	// rotations, not just the first — the propagation is the whole point.
	access2, _, _, _, err := s.RefreshToken(context.Background(), newRefresh, "anchat-test")
	if err != nil {
		t.Fatalf("second RefreshToken: %v", err)
	}
	claims2, err := s.ParseAndVerifyJWT(access2)
	if err != nil {
		t.Fatalf("ParseAndVerifyJWT (2nd): %v", err)
	}
	if claims2.Custom["account_id"] != "u-999" {
		t.Errorf("account_id lost across the second rotation; custom=%v", claims2.Custom)
	}
}

// ----------------------------------------------------------------------------
// Bugboard #154 — an empty-claims session (claims resolved empty at login
// mint, e.g. a JWT minted before the account row existed) must SELF-HEAL on
// refresh by re-resolving the provider, instead of replaying emptiness for the
// whole ~30-day refresh chain. Scoped to empty-claims sessions only — a healthy
// session must never re-invoke the provider on the hot path.
// ----------------------------------------------------------------------------

// countingClaimsResolver records how many times it is invoked so tests can
// prove the empty-only scoping (healthy sessions must not call it).
type countingClaimsResolver struct {
	mu     sync.Mutex
	calls  int
	claims map[string]string
}

func (c *countingClaimsResolver) ResolveClaims(_ context.Context, _, _ string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.claims
}

// The core #154 fix: an empty-claims session re-resolves on refresh; once the
// provider can answer (account row has committed), the rotated access token
// carries account_id and the new refresh row persists it.
func TestRefreshToken_reResolvesEmptyClaims_onRefresh(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "empty-claims-refresh"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET" // stored claims empty
	resolver := &countingClaimsResolver{claims: map[string]string{"account_id": "uuid-healed"}}
	s.SetClaimsResolver(resolver)

	access, newRefresh, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if resolver.calls != 1 {
		t.Errorf("provider calls = %d, want 1 (empty-claims session must re-resolve)", resolver.calls)
	}
	claims, err := s.ParseAndVerifyJWT(access)
	if err != nil {
		t.Fatalf("ParseAndVerifyJWT: %v", err)
	}
	if claims.Custom["account_id"] != "uuid-healed" {
		t.Errorf("rotated access token did not pick up re-resolved account_id; custom=%v", claims.Custom)
	}
	if got := ormDB.claimsByToken[sha256Hex(newRefresh)]; got != `{"account_id":"uuid-healed"}` {
		t.Errorf("re-resolved claims not persisted to new refresh row; got %q", got)
	}
}

// A HEALTHY session (non-empty stored claims) must NOT invoke the provider on
// refresh — the empty-only guard keeps the hot path free of provider cost.
func TestRefreshToken_healthySession_doesNotReResolve(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "healthy-refresh"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	ormDB.claimsByToken = map[string]string{sha256Hex(refresh): `{"account_id":"u-1"}`}
	resolver := &countingClaimsResolver{claims: map[string]string{"account_id": "SHOULD-NOT-APPEAR"}}
	s.SetClaimsResolver(resolver)

	access, _, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if resolver.calls != 0 {
		t.Errorf("provider calls = %d, want 0 (healthy session must not re-resolve)", resolver.calls)
	}
	claims, _ := s.ParseAndVerifyJWT(access)
	if claims.Custom["account_id"] != "u-1" {
		t.Errorf("healthy session lost/changed account_id; custom=%v", claims.Custom)
	}
}

// Re-resolve is fail-open: if the provider is STILL empty at refresh time, the
// rotation must succeed (no error), just without claims.
func TestRefreshToken_emptyClaims_providerStillEmpty_succeeds(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "still-empty-refresh"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	resolver := &countingClaimsResolver{claims: nil}
	s.SetClaimsResolver(resolver)

	access, _, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken must succeed even when re-resolve yields empty: %v", err)
	}
	if resolver.calls != 1 {
		t.Errorf("provider calls = %d, want 1", resolver.calls)
	}
	claims, _ := s.ParseAndVerifyJWT(access)
	if _, present := claims.Custom["account_id"]; present {
		t.Errorf("no claims expected when provider empty; custom=%v", claims.Custom)
	}
}

// A namespace with NO provider (claimsResolver nil) must not attempt re-resolve
// and must rotate normally.
func TestRefreshToken_emptyClaims_noResolver_untouched(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	const refresh = "no-resolver-refresh"
	ormDB.subjectByToken[sha256Hex(refresh)] = "0xWALLET"
	// No SetClaimsResolver → claimsResolver nil; the guard skips re-resolution.

	access, _, _, _, err := s.RefreshToken(context.Background(), refresh, "anchat-test")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	claims, _ := s.ParseAndVerifyJWT(access)
	if len(claims.Custom) != 0 {
		t.Errorf("expected no custom claims; custom=%v", claims.Custom)
	}
}

// ----------------------------------------------------------------------------
// Bugboard #125 — bounded, single-use refresh-token reuse grace (RFC 9700
// §4.13.2). A rotation response lost in transit must NOT dead-end in a 401.
// ----------------------------------------------------------------------------

// A just-rotated token (revoked, within grace, grace not consumed) is accepted
// ONCE more and mints a fresh session — recovering a client whose rotation
// response was lost. The revoke CAS is skipped (the token is already revoked),
// so this must NOT surface the replay tripwire.
func TestRefreshToken_reuseGrace_recoversLostResponse(t *testing.T) {
	s, ormDB, rq := newRotationTestService(t)

	const lostTok = "rotated-but-response-lost"
	// NOT in the active set (already revoked) ...
	// ... but eligible for grace (revoked recently, grace unused).
	ormDB.graceableTokens = map[string]string{sha256Hex(lostTok): "0xWALLET"}

	access, newRefresh, subj, exp, err := s.RefreshToken(context.Background(), lostTok, "anchat-test")
	if err != nil {
		t.Fatalf("grace recovery should succeed, got error: %v", err)
	}
	if access == "" || newRefresh == "" {
		t.Error("grace recovery must mint a fresh access + refresh token")
	}
	if newRefresh == lostTok {
		t.Error("grace recovery must rotate to a NEW refresh token")
	}
	if subj != "0xWALLET" {
		t.Errorf("subject = %q, want 0xWALLET", subj)
	}
	if exp <= 0 {
		t.Errorf("expiration not set: %d", exp)
	}
	// The single-use grace CAS must have been claimed exactly once.
	if rq.graceCASCalls != 1 {
		t.Errorf("grace CAS calls = %d, want 1", rq.graceCASCalls)
	}
	// And a fresh refresh-token row was inserted.
	if ormDB.inserted != 1 {
		t.Errorf("expected 1 INSERT for the recovered session, got %d", ormDB.inserted)
	}
}

// The grace is SINGLE-USE: once the grace_used_at CAS is lost (already
// consumed, e.g. a replay after the legitimate client already recovered), the
// token must 401 — a stolen token cannot be replayed at leisure.
func TestRefreshToken_reuseGrace_singleUse_secondAttemptIs401(t *testing.T) {
	s, ormDB, rq := newRotationTestService(t)

	const tok = "already-grace-consumed"
	ormDB.graceableTokens = map[string]string{sha256Hex(tok): "0xWALLET"}
	// Force the grace CAS to report "already consumed".
	rq.graceCASNext = []int64{0}

	_, _, _, _, err := s.RefreshToken(context.Background(), tok, "anchat-test")
	if err == nil {
		t.Fatal("a consumed grace must NOT recover — expected an invalid-token error")
	}
	if !containsCI(err.Error(), "invalid or expired") {
		t.Errorf("want invalid/expired 401, got %v", err)
	}
	if ormDB.inserted != 0 {
		t.Errorf("no new session should be minted when grace is consumed; inserts=%d", ormDB.inserted)
	}
}

// A genuinely bad token (not active AND not grace-eligible) still 401s — the
// grace path must not turn unknown tokens into sessions.
func TestRefreshToken_noGrace_genuineBadToken_stays401(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)
	// graceableTokens left empty: nothing is grace-eligible.

	_, _, _, _, err := s.RefreshToken(context.Background(), "never-seen-this-token", "anchat-test")
	if err == nil {
		t.Fatal("a never-seen token must be rejected")
	}
	if !containsCI(err.Error(), "invalid or expired") {
		t.Errorf("want invalid/expired 401, got %v", err)
	}
	if ormDB.inserted != 0 {
		t.Errorf("no session should be minted for a bad token; inserts=%d", ormDB.inserted)
	}
}

// Security regression (bugboard #125 logout-bypass): a token explicitly revoked
// via RevokeToken (logout) must NOT be recoverable through the reuse grace, even
// within the 60s window. RevokeToken burns grace_used_at so the grace predicate
// (grace_used_at IS NULL) excludes it.
func TestRevokeToken_burnsGrace_blocksLogoutBypass(t *testing.T) {
	s, ormDB, _ := newRotationTestService(t)

	const tok = "logged-out-token"
	// Within the revoke window it WOULD be grace-eligible...
	ormDB.graceableTokens = map[string]string{sha256Hex(tok): "0xWALLET"}

	// ...until the user logs out.
	if err := s.RevokeToken(context.Background(), "anchat-test", tok, false, ""); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	// A refresh with the just-logged-out token must be rejected, not resurrected.
	_, _, _, _, err := s.RefreshToken(context.Background(), tok, "anchat-test")
	if err == nil {
		t.Fatal("LOGOUT-BYPASS: a logged-out token was resurrected via reuse grace")
	}
	if !containsCI(err.Error(), "invalid or expired") {
		t.Errorf("want 401 invalid/expired, got %v", err)
	}
	if ormDB.inserted != 0 {
		t.Errorf("no session should be minted for a logged-out token; inserts=%d", ormDB.inserted)
	}
}
