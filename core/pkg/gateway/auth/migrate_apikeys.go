package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// MigratePlaintextAPIKeys HMAC-hashes leftover api_keys rows that still
// store the raw `ak_…` value (bugboard #163). Idempotent: already-hashed
// 64-hex rows are skipped (they do not match `ak_%`). Returns the number
// of rows rewritten.
//
// A failed UPDATE on one id aborts and returns the error so a half-batch
// is visible; callers must not continue serving as if the table is clean.
func (s *Service) MigratePlaintextAPIKeys(ctx context.Context) (int, error) {
	if s.apiKeyHMACSecret == "" {
		return 0, nil
	}
	orm := s.keyORM()
	if orm == nil {
		return 0, nil
	}
	db := orm.Database()
	if db == nil {
		return 0, nil
	}

	internalCtx := client.WithInternalAuth(ctx)
	res, err := db.Query(internalCtx, "SELECT id, key FROM api_keys WHERE key LIKE 'ak_%'")
	if err != nil {
		return 0, fmt.Errorf("list plaintext api keys: %w", err)
	}
	if res == nil || res.Count == 0 {
		return 0, nil
	}

	n := 0
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		id := row[0]
		raw, _ := row[1].(string)
		if !strings.HasPrefix(raw, "ak_") {
			continue
		}
		hashed := s.HashAPIKey(raw)
		if hashed == raw {
			return n, fmt.Errorf("HMAC secret produced a hash equal to the raw key; refusing to migrate")
		}
		if _, err := db.Query(internalCtx,
			"UPDATE api_keys SET key = ? WHERE id = ? AND key = ?", hashed, id, raw); err != nil {
			return n, fmt.Errorf("hash api key id %v: %w", id, err)
		}
		// The principal is the key, spelled the way the authorization gate
		// looks it up. Rehashing the key without moving its principal would
		// leave the key authenticating and holding no grant anywhere.
		if _, err := db.Query(internalCtx,
			"UPDATE OR IGNORE principals SET identifier = ? WHERE type = 'service_account' AND identifier = ?",
			hashed, raw); err != nil {
			return n, fmt.Errorf("api key id %v was hashed but its grants were left under the raw key, "+
				"so it would authenticate and be refused everywhere: %w", id, err)
		}
		n++
	}
	return n, nil
}

// RevokeOrphanedAPIKeys sets revoked_at on api_keys whose namespace_id no
// longer exists (bugboard #164). Idempotent. Returns the number of rows
// updated. Lookup already INNER JOINs namespaces, so these keys 401; revoking
// them makes the 401 mean "revoked" rather than "unknown" in diagnostics.
func (s *Service) RevokeOrphanedAPIKeys(ctx context.Context) (int, error) {
	orm := s.keyORM()
	if orm == nil {
		return 0, nil
	}
	db := orm.Database()
	if db == nil {
		return 0, nil
	}
	internalCtx := client.WithInternalAuth(ctx)
	res, err := db.Query(internalCtx, `
		UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP
		WHERE revoked_at IS NULL
		  AND namespace_id NOT IN (SELECT id FROM namespaces)`)
	if err != nil {
		return 0, fmt.Errorf("revoke orphaned api keys: %w", err)
	}
	if res == nil {
		return 0, nil
	}
	return int(res.Count), nil
}
