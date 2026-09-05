package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Revoking an API key stopped the key authenticating and did nothing to the
// JWTs already exchanged from it — up to fifteen minutes of full access after
// an operator had revoked the credential and been told it was done. Ending one
// session was not possible at all: logout dropped the refresh token and left
// the access token valid.
//
// A token is now checked against a list of revocations on every request. The
// list is small (only revocations whose tokens have not yet expired are in it)
// and is held in memory, refreshed on a timer, because a database round trip
// per request costs a cross-region hop and this runs on every authenticated
// call.
//
// The refresh interval is the staleness: a revocation takes effect within it.
// Fifteen minutes became ten seconds, which is the point.

const (
	// revocationRefreshInterval is how stale the in-memory list may be, and so
	// how long a revoked token may still be accepted.
	revocationRefreshInterval = 10 * time.Second

	// revocationPruneInterval is how often expired rows are deleted. They deny
	// nothing once past expires_at; this keeps the table the size of the
	// revocations still in flight.
	revocationPruneInterval = 1 * time.Hour
)

// revocation is one row: either a named token, or every token issued to a
// subject before a moment.
type revocation struct {
	jti          string
	subject      string
	issuedBefore int64
	expiresAt    int64
}

// RevocationList is the set of tokens this gateway refuses.
type RevocationList struct {
	orm    client.NetworkClient
	logger *logging.ColoredLogger

	mu          sync.RWMutex
	byJTI       map[string]int64 // jti -> expiry, so a stale entry can be dropped
	bySubject   map[string]int64 // subject -> issued_before
	lastRefresh time.Time
	loaded      bool

	// now is time.Now, replaced in tests.
	now func() time.Time
}

// NewRevocationList builds the list a Service consults.
func NewRevocationList(orm client.NetworkClient, logger *logging.ColoredLogger) *RevocationList {
	return &RevocationList{
		orm:       orm,
		logger:    logger,
		byJTI:     map[string]int64{},
		bySubject: map[string]int64{},
		now:       time.Now,
	}
}

// Denies reports whether a token must be refused.
//
// subjectKeys are the names this token's subject may have been revoked under.
// A wallet is revoked under itself; an API key is revoked under its hash,
// because that is what the revoking code has — a JWT exchanged from a key
// carries the raw key as its subject, and RevokeKey only ever sees the hash.
// The caller derives both rather than this list knowing how keys are hashed.
//
// A token with no jti was minted before tokens carried one. It is still
// checked against the subject revocations, which is what covers a revoked key.
func (r *RevocationList) Denies(claims *JWTClaims, subjectKeys []string) bool {
	if r == nil || claims == nil {
		return false
	}
	r.refreshIfStale()

	r.mu.RLock()
	defer r.mu.RUnlock()

	if claims.Jti != "" {
		if _, denied := r.byJTI[claims.Jti]; denied {
			return true
		}
	}
	for _, key := range subjectKeys {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		issuedBefore, denied := r.bySubject[key]
		if !denied {
			continue
		}
		// A token minted after the revocation is a new grant — a fresh login,
		// or a new key — and the revocation of the old one does not reach it.
		//
		// The boundary is inclusive because `iat` has one-second resolution: a
		// token minted in the same second as the revocation is exactly what an
		// operator revoking a key means to catch. The cost is that signing in
		// again within that same second is refused and has to be retried,
		// which is the right way round.
		if claims.Iat <= issuedBefore {
			return true
		}
	}
	return false
}

// RevokeSubject refuses every token already issued to a subject.
//
// ttl is how long the tokens it covers may still be valid; the row is pruned
// after that, because past it there is nothing left to deny.
func (r *RevocationList) RevokeSubject(ctx context.Context, subject, reason string, ttl time.Duration) error {
	if r == nil {
		return fmt.Errorf("no revocation list: a revoked credential's tokens would keep working")
	}
	subject = strings.ToLower(strings.TrimSpace(subject))
	if subject == "" {
		return fmt.Errorf("cannot revoke tokens for an empty subject")
	}
	now := r.now()
	return r.insert(ctx, revocation{
		subject:      subject,
		issuedBefore: now.Unix(),
		expiresAt:    now.Add(ttl).Unix(),
	}, reason)
}

// RevokeToken refuses one token.
func (r *RevocationList) RevokeToken(ctx context.Context, jti string, expiresAt int64, reason string) error {
	if r == nil {
		return fmt.Errorf("no revocation list: this token would keep working")
	}
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return fmt.Errorf("cannot revoke a token with no id; it was minted before tokens carried one")
	}
	return r.insert(ctx, revocation{jti: jti, expiresAt: expiresAt}, reason)
}

func (r *RevocationList) insert(ctx context.Context, rev revocation, reason string) error {
	db := r.database()
	if db == nil {
		return fmt.Errorf("no database: the revocation cannot be recorded, so the token would keep working")
	}
	internalCtx := client.WithInternalAuth(ctx)
	if _, err := db.Query(internalCtx,
		`INSERT INTO revoked_tokens(jti, subject, issued_before, expires_at, reason)
		 VALUES (?, ?, ?, ?, ?)`,
		nullable(rev.jti), nullable(rev.subject), rev.issuedBefore, rev.expiresAt, reason,
	); err != nil {
		return fmt.Errorf("record the revocation: %w", err)
	}

	// Apply it here immediately rather than waiting for the next refresh: the
	// gateway that performed the revocation should not be the last to honour
	// it.
	r.mu.Lock()
	if rev.jti != "" {
		r.byJTI[rev.jti] = rev.expiresAt
	}
	if rev.subject != "" {
		if existing, ok := r.bySubject[rev.subject]; !ok || rev.issuedBefore > existing {
			r.bySubject[rev.subject] = rev.issuedBefore
		}
	}
	r.mu.Unlock()
	return nil
}

// nullable turns "" into nil so the column holds NULL rather than an empty
// string, which would match an empty jti or subject.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// refreshIfStale reloads the list when the in-memory copy is older than the
// refresh interval.
func (r *RevocationList) refreshIfStale() {
	r.mu.RLock()
	fresh := r.loaded && r.now().Sub(r.lastRefresh) < revocationRefreshInterval
	r.mu.RUnlock()
	if fresh {
		return
	}
	r.Refresh(context.Background())
}

// Refresh reloads the list from the database.
//
// A failed load leaves the previous list in place and says so. It does not
// clear the list: forgetting the revocations because one query failed would
// turn a database blip into every revoked token working again.
func (r *RevocationList) Refresh(ctx context.Context) {
	db := r.database()
	if db == nil {
		return
	}

	now := r.now().Unix()
	internalCtx := client.WithInternalAuth(ctx)
	res, err := db.Query(internalCtx,
		"SELECT jti, subject, issued_before, expires_at FROM revoked_tokens WHERE expires_at > ?", now)
	if err != nil {
		if r.logger != nil {
			r.logger.ComponentWarn(logging.ComponentGeneral,
				"could not reload the token revocations; the previous list is still being applied",
				zap.Error(err))
		}
		// Back off anyway, so a database that is down does not mean a query
		// per request. The previous list stays in force; if there has never
		// been one, nothing is denied until the next attempt succeeds — the
		// same posture as the API-key cache, and the token still had to carry
		// a valid signature and an unexpired claim to get this far.
		r.mu.Lock()
		r.lastRefresh = r.now()
		r.loaded = true
		r.mu.Unlock()
		return
	}

	byJTI := map[string]int64{}
	bySubject := map[string]int64{}
	if res != nil {
		for _, row := range res.Rows {
			if len(row) < 4 {
				continue
			}
			jti, _ := row[0].(string)
			subject, _ := row[1].(string)
			issuedBefore := toInt64(row[2])
			expiresAt := toInt64(row[3])

			if jti != "" {
				byJTI[jti] = expiresAt
			}
			if subject != "" {
				subject = strings.ToLower(strings.TrimSpace(subject))
				if existing, ok := bySubject[subject]; !ok || issuedBefore > existing {
					bySubject[subject] = issuedBefore
				}
			}
		}
	}

	r.mu.Lock()
	r.byJTI = byJTI
	r.bySubject = bySubject
	r.lastRefresh = r.now()
	r.loaded = true
	r.mu.Unlock()
}

// Prune deletes rows whose tokens have all expired. Returns how many went.
func (r *RevocationList) Prune(ctx context.Context) error {
	db := r.database()
	if db == nil {
		return nil
	}
	internalCtx := client.WithInternalAuth(ctx)
	if _, err := db.Query(internalCtx, "DELETE FROM revoked_tokens WHERE expires_at <= ?", r.now().Unix()); err != nil {
		return fmt.Errorf("prune expired revocations: %w", err)
	}
	return nil
}

// StartPruning removes expired rows on a timer until ctx is done.
func (r *RevocationList) StartPruning(ctx context.Context) {
	if r == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(revocationPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Prune(ctx); err != nil && r.logger != nil {
					r.logger.ComponentWarn(logging.ComponentGeneral,
						"could not prune expired token revocations", zap.Error(err))
				}
			}
		}
	}()
}

func (r *RevocationList) database() client.DatabaseClient {
	if r == nil || r.orm == nil {
		return nil
	}
	return r.orm.Database()
}

// RevokeSession refuses one access token from now on.
//
// Logging out dropped the refresh token and left the access token valid until
// it expired, so "log me out" meant "stop me getting a new token" rather than
// "stop this one working". A token minted before tokens carried a jti cannot be
// named, and says so.
func (s *Service) RevokeSession(ctx context.Context, claims *JWTClaims) error {
	if claims == nil {
		return fmt.Errorf("no token to revoke")
	}
	if claims.Jti == "" {
		return fmt.Errorf("this token was issued before tokens carried an id and cannot be revoked on its own; " +
			"log out of every session instead")
	}
	return s.revocations.RevokeToken(ctx, claims.Jti, claims.Exp, "session ended")
}

// RevokeAllSessions refuses every access token already issued to a subject.
func (s *Service) RevokeAllSessions(ctx context.Context, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("no subject to revoke")
	}
	// Recorded under both names for the same reason the verifier looks under
	// both: a wallet subject is itself, a key subject is known by its hash.
	if err := s.revocations.RevokeSubject(ctx, subject, "all sessions ended", maxExchangedTokenLifetime); err != nil {
		return err
	}
	if hashed := s.HashAPIKey(subject); hashed != "" && hashed != subject {
		return s.revocations.RevokeSubject(ctx, hashed, "all sessions ended", maxExchangedTokenLifetime)
	}
	return nil
}

// Revocations exposes the list so the gateway can start its pruner and, in a
// test, drive a refresh.
func (s *Service) Revocations() *RevocationList { return s.revocations }

// DeniesSubject reports whether a credential presented under any of these
// subjects has been revoked.
//
// Denies compares a token's issue time against the revocation, because a token
// minted after it is a new grant. A raw API key has no issue time to compare —
// the string either is the revoked credential or is not — so any live
// revocation of the subject denies.
//
// This is what closes the window the API-key cache opens: a key's namespace and
// grants are cached for a minute, so a revoked key kept working for up to that
// long. The list is replicated and reloaded every ten seconds, so it is the
// shorter of the two.
func (r *RevocationList) DeniesSubject(subjects ...string) bool {
	if r == nil {
		return false
	}
	r.refreshIfStale()

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, subject := range subjects {
		subject = strings.ToLower(strings.TrimSpace(subject))
		if subject == "" {
			continue
		}
		if _, denied := r.bySubject[subject]; denied {
			return true
		}
	}
	return false
}
