package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ErrSignInKeySweepIncomplete is a policy that was recorded while the end
// users' sign-in keys were not all revoked. The policy holds; setting it again
// revokes the keys that are left.
var ErrSignInKeySweepIncomplete = errors.New("the session policy is set, but not every existing end-user sign-in key was revoked")

// liveEndUserSignInKeysSQL finds, in one statement, every unrevoked key a
// sign-in minted for a wallet that holds no control-plane role — no grant live
// the way GrantIn counts one: not revoked, not expired, its principal not
// disabled. That is each such wallet's current key (wallet_api_keys) and the
// chain of keys it rotated from. UNION
// rather than UNION ALL, so a chain that loops ends; the chain is followed
// through revoked keys, which still link the live ones before them.
//
// A key that expired longer ago than an exchanged token lives authenticates
// nothing and leaves no token alive, so it is not revoked: an end user who
// signs in daily holds a key per day, and the sweep writes only for the ones
// that still matter.
//
// The CTE sits in a subquery so the statement starts with SELECT, which every
// database adapter — the production client and the tests' — reads as a query.
const liveEndUserSignInKeysSQL = `SELECT k.id FROM api_keys AS k
 WHERE k.namespace_id = ? AND k.revoked_at IS NULL
   AND k.expires_at > datetime('now', ?)
   AND k.id IN (
     WITH RECURSIVE chain(id) AS (
       SELECT w.api_key_id FROM wallet_api_keys AS w
        WHERE w.namespace_id = ?
          AND w.wallet NOT IN (
            SELECT p.identifier FROM grants AS g JOIN principals AS p ON p.id = g.principal_id
             WHERE g.namespace_id = ? AND p.type = 'wallet'
               AND g.revoked_at IS NULL AND p.disabled_at IS NULL
               AND (g.expires_at IS NULL OR g.expires_at > datetime('now'))
               AND g.role IN ('owner', 'admin', 'developer'))
       UNION
       SELECT r.rotated_from FROM api_keys AS r JOIN chain AS c ON r.id = c.id
        WHERE r.rotated_from IS NOT NULL)
     SELECT id FROM chain)`

// revokeEndUserSignInKeys revokes every key a sign-in minted for a wallet that
// holds no control-plane role in the namespace, and reports how many.
//
// The keys are found in one query; each is then revoked the way any key is, so
// its exchanged tokens stop too. Only live keys are found, so running it again
// after a failure picks up where it stopped.
func (s *Service) revokeEndUserSignInKeys(ctx context.Context, namespace string) (int, error) {
	if s.keyORM() == nil {
		return 0, fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return 0, fmt.Errorf("resolve the namespace %q: %w", namespace, err)
	}
	live, err := s.keyORM().Database().Query(client.WithInternalAuth(ctx), liveEndUserSignInKeysSQL,
		nsID, fmt.Sprintf("-%d seconds", int64(maxExchangedTokenLifetime/time.Second)), nsID, nsID)
	if err != nil {
		return 0, fmt.Errorf("find the end users' sign-in keys in %q: %w", namespace, err)
	}
	revoked := 0
	for _, id := range idsOf(live) {
		err := s.RevokeKey(ctx, namespace, id)
		if errors.Is(err, ErrNoActiveKey) {
			// Revoked since the query, by a concurrent sweep or an operator:
			// what this sweep came to do is done.
			continue
		}
		if err != nil {
			return revoked, fmt.Errorf("revoke sign-in key %d in %q: %w", id, namespace, err)
		}
		revoked++
	}
	return revoked, nil
}

func idsOf(res *client.QueryResult) []int64 {
	if res == nil {
		return nil
	}
	out := make([]int64, 0, len(res.Rows))
	for _, row := range res.Rows {
		if len(row) > 0 {
			if id := cellInt64(row[0]); id > 0 {
				out = append(out, id)
			}
		}
	}
	return out
}
