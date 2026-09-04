package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// revocationDB is an in-memory stand-in for the revoked_tokens table.
type revocationDB struct {
	client.DatabaseClient

	mu         sync.Mutex
	rows       []revocation
	selects    int
	failNext   bool
	failAlways bool
	lastDelete string
}

func (d *revocationDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch {
	case strings.HasPrefix(strings.TrimSpace(sql), "INSERT INTO revoked_tokens"):
		rev := revocation{}
		if args[0] != nil {
			rev.jti, _ = args[0].(string)
		}
		if args[1] != nil {
			rev.subject, _ = args[1].(string)
		}
		rev.issuedBefore = toInt64(args[2])
		rev.expiresAt = toInt64(args[3])
		d.rows = append(d.rows, rev)
		return &client.QueryResult{Count: 1}, nil

	case strings.HasPrefix(strings.TrimSpace(sql), "SELECT jti"):
		d.selects++
		if d.failAlways || d.failNext {
			d.failNext = false
			return nil, errString("the registry did not answer")
		}
		cutoff := toInt64(args[0])
		out := &client.QueryResult{Columns: []string{"jti", "subject", "issued_before", "expires_at"}}
		for _, r := range d.rows {
			if r.expiresAt <= cutoff {
				continue
			}
			out.Rows = append(out.Rows, []interface{}{r.jti, r.subject, r.issuedBefore, r.expiresAt})
		}
		out.Count = int64(len(out.Rows))
		return out, nil

	case strings.HasPrefix(strings.TrimSpace(sql), "DELETE FROM revoked_tokens"):
		// The fake does not interpret SQL, so a prune whose predicate was
		// changed to match nothing would still look like it worked. Record the
		// statement so a test can check what was actually asked for.
		d.lastDelete = strings.Join(strings.Fields(sql), " ")
		cutoff := toInt64(args[0])
		kept := d.rows[:0]
		removed := 0
		for _, r := range d.rows {
			if r.expiresAt <= cutoff {
				removed++
				continue
			}
			kept = append(kept, r)
		}
		d.rows = kept
		return &client.QueryResult{Count: int64(removed)}, nil
	}
	return nil, errString("unexpected sql: " + sql)
}

type revocationNet struct {
	client.NetworkClient
	db *revocationDB
}

func (n *revocationNet) Database() client.DatabaseClient { return n.db }

func newTestRevocations(t *testing.T) (*RevocationList, *revocationDB, *time.Time) {
	t.Helper()
	db := &revocationDB{}
	list := NewRevocationList(&revocationNet{db: db}, nil)
	clock := time.Unix(1_000_000, 0)
	list.now = func() time.Time { return clock }
	return list, db, &clock
}

// The fifteen-minute window: a key was revoked and every token already
// exchanged from it went on working until it expired.
func TestRevocationList_revokingASubjectDeniesTokensAlreadyIssued(t *testing.T) {
	list, _, clock := newTestRevocations(t)

	issuedEarlier := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60, Jti: "token-1"}
	if list.Denies(issuedEarlier, []string{"ak_key:ns"}) {
		t.Fatal("a token was denied before anything was revoked")
	}

	if err := list.RevokeSubject(context.Background(), "ak_key:ns", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	if !list.Denies(issuedEarlier, []string{"ak_key:ns"}) {
		t.Fatal("a token issued before the revocation is still accepted")
	}
}

// A token minted after the revocation is a new grant — a fresh login, or a new
// key — and the revocation of the old one does not reach it.
func TestRevocationList_aTokenIssuedAfterTheRevocationIsNotDenied(t *testing.T) {
	list, _, clock := newTestRevocations(t)

	if err := list.RevokeSubject(context.Background(), "0xwallet", "logged out everywhere", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	later := &JWTClaims{Sub: "0xwallet", Iat: clock.Unix() + 1} // the next second
	if list.Denies(later, []string{"0xwallet"}) {
		t.Error("a token minted after the revocation was denied; signing in again would not work")
	}
}

// Logging out dropped the refresh token and left the access token valid.
func TestRevocationList_revokingOneTokenLeavesTheOthers(t *testing.T) {
	list, _, clock := newTestRevocations(t)

	mine := &JWTClaims{Sub: "0xwallet", Jti: "session-a", Iat: clock.Unix(), Exp: clock.Unix() + 900}
	other := &JWTClaims{Sub: "0xwallet", Jti: "session-b", Iat: clock.Unix(), Exp: clock.Unix() + 900}

	if err := list.RevokeToken(context.Background(), mine.Jti, mine.Exp, "logged out"); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if !list.Denies(mine, []string{"0xwallet"}) {
		t.Error("the token that was logged out is still accepted")
	}
	if list.Denies(other, []string{"0xwallet"}) {
		t.Error("logging out of one session ended another")
	}
}

// The subject is looked up case-insensitively, because a wallet is written
// both ways in this codebase.
func TestRevocationList_subjectMatchingIgnoresCase(t *testing.T) {
	list, _, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "0xWALLET", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	claims := &JWTClaims{Sub: "0xwallet", Iat: clock.Unix() - 1}
	if !list.Denies(claims, []string{"0xWaLLeT"}) {
		t.Error("the same wallet in different case was not denied")
	}
}

// A token minted before tokens carried a jti is still covered by a revocation
// of its subject, which is what makes revoking a key work for them.
func TestRevocationList_aTokenWithNoIDIsStillCoveredBySubject(t *testing.T) {
	list, _, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "ak_old:ns", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	legacy := &JWTClaims{Sub: "ak_old:ns", Iat: clock.Unix() - 1} // no Jti
	if !list.Denies(legacy, []string{"ak_old:ns"}) {
		t.Error("a token minted before jti existed escaped its key's revocation")
	}
}

// The in-memory copy is refreshed on a timer; the interval is how long a
// revocation may still be honoured late. It used to be the token's whole
// lifetime.
func TestRevocationList_picksUpARevocationMadeElsewhere(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}

	list.Denies(claims, []string{"ak_key:ns"}) // first load

	// Another gateway records a revocation straight into the table.
	db.mu.Lock()
	db.rows = append(db.rows, revocation{subject: "ak_key:ns", issuedBefore: clock.Unix(), expiresAt: clock.Unix() + 3600})
	db.mu.Unlock()

	if list.Denies(claims, []string{"ak_key:ns"}) {
		t.Error("the list refreshed sooner than its interval; that is a query per request")
	}

	*clock = clock.Add(revocationRefreshInterval + time.Second)
	if !list.Denies(claims, []string{"ak_key:ns"}) {
		t.Errorf("a revocation made elsewhere was not honoured within %s", revocationRefreshInterval)
	}
}

// Forgetting the revocations because one query failed would turn a database
// blip into every revoked token working again.
func TestRevocationList_keepsTheListWhenAReloadFails(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "ak_key:ns", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}
	claims := &JWTClaims{Sub: "ak_key:ns", Iat: clock.Unix() - 60}
	if !list.Denies(claims, []string{"ak_key:ns"}) {
		t.Fatal("the revocation did not apply")
	}

	db.mu.Lock()
	db.failNext = true
	db.mu.Unlock()
	*clock = clock.Add(revocationRefreshInterval + time.Second)

	if !list.Denies(claims, []string{"ak_key:ns"}) {
		t.Error("a failed reload cleared the list, so every revoked token would work again")
	}
}

// A database that is down must not mean a query per request.
func TestRevocationList_doesNotRetryOnEveryRequestWhileTheDatabaseIsDown(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	claims := &JWTClaims{Sub: "0xwallet", Iat: clock.Unix()}

	db.mu.Lock()
	db.failAlways = true
	db.mu.Unlock()
	list.Denies(claims, []string{"0xwallet"})

	db.mu.Lock()
	before := db.selects
	db.mu.Unlock()

	for i := 0; i < 50; i++ {
		list.Denies(claims, []string{"0xwallet"})
	}

	db.mu.Lock()
	after := db.selects
	db.mu.Unlock()
	if after != before {
		t.Errorf("%d extra queries for 50 requests while the database was failing", after-before)
	}
}

// A revocation whose tokens have all expired denies nothing.
func TestRevocationList_prunesExpiredRows(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "ak_key:ns", "revoked", time.Minute); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}

	*clock = clock.Add(2 * time.Minute)
	if err := list.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	db.mu.Lock()
	remaining := len(db.rows)
	statement := db.lastDelete
	db.mu.Unlock()
	if remaining != 0 {
		t.Errorf("%d rows remain", remaining)
	}
	if !strings.Contains(statement, "expires_at <= ?") {
		t.Errorf("the prune does not select expired rows: %s", statement)
	}
}

// A revocation that has not expired must survive the prune, or revoking
// something would stop applying the first time the pruner ran.
func TestRevocationList_pruneLeavesLiveRows(t *testing.T) {
	list, db, clock := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "ak_key:ns", "revoked", time.Hour); err != nil {
		t.Fatalf("RevokeSubject: %v", err)
	}

	*clock = clock.Add(time.Minute)
	if err := list.Prune(context.Background()); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	db.mu.Lock()
	remaining := len(db.rows)
	db.mu.Unlock()
	if remaining != 1 {
		t.Errorf("%d rows remain, want the live one", remaining)
	}
}

func TestRevocationList_refusesWhatItCannotRecord(t *testing.T) {
	var nilList *RevocationList
	if err := nilList.RevokeSubject(context.Background(), "x", "", time.Hour); err == nil {
		t.Error("a nil list reported a revocation as recorded")
	}
	if err := nilList.RevokeToken(context.Background(), "jti", 0, ""); err == nil {
		t.Error("a nil list reported a token revocation as recorded")
	}
	if nilList.Denies(&JWTClaims{Sub: "x"}, []string{"x"}) {
		t.Error("a nil list denied a token")
	}

	list, _, _ := newTestRevocations(t)
	if err := list.RevokeSubject(context.Background(), "  ", "", time.Hour); err == nil {
		t.Error("an empty subject was accepted; it would match nothing and report success")
	}
	if err := list.RevokeToken(context.Background(), "", 0, ""); err == nil {
		t.Error("an empty token id was accepted")
	}
}

// An empty jti or subject must be stored as NULL, or it would match every
// token that has neither.
func TestRevocationList_storesAbsentFieldsAsNull(t *testing.T) {
	list, db, _ := newTestRevocations(t)
	if err := list.RevokeToken(context.Background(), "only-a-jti", 999, ""); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.rows) != 1 {
		t.Fatalf("%d rows", len(db.rows))
	}
	if db.rows[0].subject != "" {
		t.Errorf("subject = %q, want empty", db.rows[0].subject)
	}
}
