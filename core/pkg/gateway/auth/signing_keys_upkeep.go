package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// Keeping the index gateways' unbound keys in signing_keys honest.
//
// Every index gateway (one per node) publishes a key of its own, unbound, and
// stamps it while it runs. Releases before the credential change published a
// new key on every restart and never retired the old ones, so the table holds
// keys that no gateway signs with and that nothing retires: each one is a
// public key that verifies a token for ANY namespace if its private half ever
// turns up. A key's row is retired once nobody has stamped it for
// unusedKeyRetention; a live peer's key is never in that state, because its
// gateway stamps it every signingKeyHeartbeatInterval.

// signingKeyHeartbeatInterval is how often a gateway stamps the key it signs
// with. A missed tick or two (a leaderless RQLite, a slow write) costs nothing.
const signingKeyHeartbeatInterval = 10 * time.Minute

// unusedKeyRetention is how long a key must have gone unstamped before it is
// retired. The longest token any key signs is MaxTokenLifetime; the rest is
// margin for a peer that is down for a while, whose key comes back (Publish
// clears retired_at) when it restarts, and for a peer not yet upgraded to
// stamp its key.
const unusedKeyRetention = 24 * time.Hour

// RetireUnusedKeys retires every unbound key other than keepKID that has not
// been stamped for unusedKeyRetention, as of now. It returns how many it
// retired.
//
// A key's last stamp is its last_seen_at, or created_at where nothing ever
// stamped it. Anything older than unusedKeyRetention is older than
// MaxTokenLifetime, so no token it signed is still valid. Namespace-bound keys
// are left alone: they belong to a tenant's gateway, which this one cannot
// vouch for.
func (s *SigningKeys) RetireUnusedKeys(ctx context.Context, keepKID string, now time.Time) (int, error) {
	db := s.database()
	if db == nil {
		return 0, nil
	}
	cutoff := now.Add(-unusedKeyRetention).UTC().Format(sqliteTime)
	retiredAt := now.UTC().Format(sqliteTime)

	// Decided and written in one statement, so a stamp that lands in between
	// is not overridden.
	const stale = `namespace IS NULL AND retired_at IS NULL AND kid != ? AND COALESCE(last_seen_at, created_at) < ?`
	res, err := db.Query(client.WithInternalAuth(ctx), `SELECT kid FROM signing_keys WHERE `+stale, keepKID, cutoff)
	if err != nil {
		return 0, fmt.Errorf("read the unused index signing keys: %w", err)
	}
	if res == nil || len(res.Rows) == 0 {
		return 0, nil
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`UPDATE signing_keys SET retired_at = ? WHERE `+stale, retiredAt, keepKID, cutoff); err != nil {
		return 0, fmt.Errorf("retire the unused index signing keys: %w", err)
	}
	return len(res.Rows), nil
}

// Stamp records that the key is in use, and brings it back if a peer retired
// it: a gateway that still signs with a key must have it verifiable.
func (s *SigningKeys) Stamp(ctx context.Context, kid string) error {
	db := s.database()
	if db == nil {
		return nil
	}
	if _, err := db.Query(client.WithInternalAuth(ctx),
		`UPDATE signing_keys SET last_seen_at = CURRENT_TIMESTAMP, retired_at = NULL WHERE kid = ?`, kid); err != nil {
		return fmt.Errorf("stamp the signing key %s: %w", kid, err)
	}
	return nil
}

// RetireUnusedIndexKeys retires the index gateways' keys that are no longer in
// use. It does nothing on a namespace gateway, whose key is the only one it may
// speak for.
func (s *Service) RetireUnusedIndexKeys(ctx context.Context) (int, error) {
	if s.edSigningKey == nil || s.edKeyNamespace != "" {
		return 0, nil
	}
	return s.signingKeys.RetireUnusedKeys(ctx, s.edKeyID, time.Now())
}

// StartSigningKeyHeartbeat stamps this gateway's key every
// signingKeyHeartbeatInterval until ctx ends. The key in use is read each tick,
// so a rotation is followed.
func (s *Service) StartSigningKeyHeartbeat(ctx context.Context) {
	if s.edSigningKey == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(signingKeyHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := s.signingKeys.Stamp(ctx, s.SigningKID()); err != nil && s.logger != nil {
				s.logger.ComponentWarn(logging.ComponentGeneral,
					"could not stamp this gateway's signing key; it is retired if this goes on for a day", zap.Error(err))
			}
		}
	}()
}
