package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ErrNamespaceUnknown is returned when a challenge names a namespace that does
// not exist.
//
// /v1/auth/challenge used to create it: an unauthenticated POST with any name
// at all inserted a namespaces row, so squatting a name was free and signing in
// to a name nobody had taken silently created it. Verifying that signature then
// triggered real cluster provisioning, so an anonymous caller could create
// infrastructure. Creating a namespace is its own authenticated call now.
type ErrNamespaceUnknown struct {
	Namespace string
}

func (e *ErrNamespaceUnknown) Error() string {
	return fmt.Sprintf("namespace %q does not exist", e.Namespace)
}

// ErrTooManyOutstandingNonces is returned when a wallet already has as many
// unanswered challenges as it is allowed.
type ErrTooManyOutstandingNonces struct {
	Namespace string
	Limit     int
}

func (e *ErrTooManyOutstandingNonces) Error() string {
	return fmt.Sprintf("wallet already has %d unanswered challenges in namespace %q",
		e.Limit, e.Namespace)
}

const (
	// maxOutstandingNonces is how many unanswered, unexpired challenges one
	// wallet may hold in one namespace.
	//
	// A client asks for one and answers it. More than a handful means either a
	// client that is not reading the answer or someone filling the table for a
	// wallet they do not own — the row is written for whatever wallet the body
	// names, and nothing proves the caller owns it.
	maxOutstandingNonces = 10

	// nonceReapInterval is how often spent and expired challenges are removed.
	// They are useless the moment they expire; a nonce is claimed by exact
	// match, so a stale row can only take up space.
	nonceReapInterval = 10 * time.Minute

	// nonceReapAge is how long a used or expired row is kept before removal,
	// so a replay attempt just after expiry still finds the row and is
	// refused for the right reason rather than for not existing.
	nonceReapAge = time.Hour
)

// checkOutstandingNonces refuses a wallet that already holds its limit.
func (s *Service) checkOutstandingNonces(internalCtx context.Context, db client.DatabaseClient, nsID interface{}, wallet, namespace string) error {
	res, err := db.Query(internalCtx,
		`SELECT COUNT(*) FROM nonces
		  WHERE namespace_id = ? AND wallet = ?
		    AND used_at IS NULL AND expires_at > datetime('now')`,
		nsID, wallet)
	if err != nil {
		// Not being able to count is not permission to skip the ceiling.
		return fmt.Errorf("failed to count outstanding challenges for namespace %q: %w", namespace, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil
	}
	if toInt64(res.Rows[0][0]) >= int64(maxOutstandingNonces) {
		return &ErrTooManyOutstandingNonces{Namespace: namespace, Limit: maxOutstandingNonces}
	}
	return nil
}

// StartNonceReaper removes spent and expired challenges on a ticker, and stops
// when ctx is done.
//
// Without it the table only grows: every challenge ever issued stays in a
// Raft-replicated table forever, and the ones that matter — unexpired and
// unanswered — are a vanishing fraction of it.
func (s *Service) StartNonceReaper(ctx context.Context) {
	if s.orm == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(nonceReapInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reapNonces(ctx)
			}
		}
	}()
}

// reapNonces deletes challenges that can no longer be claimed.
func (s *Service) reapNonces(ctx context.Context) {
	res, err := s.orm.Database().Query(client.WithInternalAuth(ctx),
		`DELETE FROM nonces
		  WHERE expires_at < datetime('now', ?)
		     OR (used_at IS NOT NULL AND used_at < datetime('now', ?))`,
		reapCutoff(), reapCutoff())
	if err != nil {
		s.logger.ComponentWarn("gateway", "could not remove spent challenges; the nonces table will keep growing")
		return
	}
	_ = res
}

// reapCutoff is the SQLite modifier for how far back to reap.
func reapCutoff() string {
	return fmt.Sprintf("-%d seconds", int(nonceReapAge.Seconds()))
}
