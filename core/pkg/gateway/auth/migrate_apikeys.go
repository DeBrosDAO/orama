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
		_, _ = db.Query(internalCtx,
			"UPDATE namespace_ownership SET owner_id = ? WHERE owner_type = 'api_key' AND owner_id = ?",
			hashed, raw)
		n++
	}
	return n, nil
}
