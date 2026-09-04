package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// KeyInfo is the non-secret metadata for one API key, returned by ListKeys.
// It never contains the key material (only the id, label, scopes, timestamps).
type KeyInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Scopes     string `json:"scopes"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

// IssueScopedKey mints a fresh API key for a namespace with an explicit,
// already-normalized grant set (bugboard #148). Unlike GetOrCreateAPIKey it is
// NOT wallet-linked and always creates a NEW key, so a namespace can hold
// several keys with different scopes (e.g. app-runtime + admin). Returns the
// raw key (shown once) and its row id.
func (s *Service) IssueScopedKey(ctx context.Context, namespace, storedScopes, label string) (string, int64, error) {
	if s.keyORM() == nil {
		return "", 0, fmt.Errorf("client not initialized")
	}
	if strings.TrimSpace(storedScopes) == "" {
		return "", 0, fmt.Errorf("refusing to issue a key with an empty scope set")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", 0, fmt.Errorf("namespace is required")
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}

	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, fmt.Errorf("failed to generate api key: %w", err)
	}
	rawKey := "ak_" + base64.RawURLEncoding.EncodeToString(buf) + ":" + namespace
	hashedKey := s.HashAPIKey(rawKey)

	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	if _, err := db.Query(internalCtx,
		"INSERT INTO api_keys(key, name, namespace_id, scopes) VALUES (?, ?, ?, ?)",
		hashedKey, label, nsID, storedScopes,
	); err != nil {
		return "", 0, fmt.Errorf("failed to store api key: %w", err)
	}

	// Record namespace ownership (hashed, mirroring GetOrCreateAPIKey) so the
	// authorization middleware recognizes the key as an owner of the namespace.
	// A key with no ownership row is refused everywhere, so a failure here is
	// the caller's problem and not something to swallow.
	if _, err := db.Query(internalCtx,
		"INSERT OR IGNORE INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'api_key', ?)",
		nsID, hashedKey,
	); err != nil {
		return "", 0, fmt.Errorf("failed to record key ownership of namespace %q: %w", namespace, err)
	}

	var id int64
	if rid, err := db.Query(internalCtx, "SELECT id FROM api_keys WHERE key = ? LIMIT 1", hashedKey); err == nil &&
		rid != nil && rid.Count > 0 && len(rid.Rows) > 0 && len(rid.Rows[0]) > 0 {
		id = toInt64(rid.Rows[0][0])
	}
	if id == 0 {
		return "", 0, fmt.Errorf("key stored but id could not be resolved")
	}

	// Recorded here rather than in the handler: a key minted by any path is a
	// key somebody will later ask about, and there was no record of any of it.
	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Action:    AuditKeyIssued,
		Resource:  fmt.Sprintf("key %d", id),
		Result:    AuditSuccess,
		Metadata:  map[string]string{"label": label, "scopes": storedScopes},
	})

	return rawKey, id, nil
}

// maxExchangedTokenLifetime is how long a JWT exchanged from an API key can
// live, and so how long a revocation of that key has to be remembered. Past it
// there is nothing left to deny and the row is pruned.
//
// It must be at least the TTL the exchange handler mints with. A margin is
// cheap — an extra row for an extra hour — and being short is not: the
// revocation would stop applying while a token it covers is still valid.
const maxExchangedTokenLifetime = 1 * time.Hour

// RevokeKey soft-revokes a single key by id within a namespace (bugboard #148).
// Revocation sets revoked_at (so the audit trail survives) and drops the
// ownership row; the key lookup filters revoked_at IS NULL, so the key stops
// authenticating within the 60s cache TTL. Returns an error if no matching
// active key was found.
func (s *Service) RevokeKey(ctx context.Context, namespace string, id int64) error {
	if s.keyORM() == nil {
		return fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	// Confirm the key exists, is in this namespace, and is not already revoked.
	sel, err := db.Query(internalCtx,
		"SELECT key FROM api_keys WHERE id = ? AND namespace_id = ? AND revoked_at IS NULL LIMIT 1", id, nsID)
	if err != nil {
		return fmt.Errorf("failed to look up key %d: %w", id, err)
	}
	if sel == nil || sel.Count == 0 || len(sel.Rows) == 0 || len(sel.Rows[0]) == 0 {
		return fmt.Errorf("no active key with id %d in namespace %q", id, namespace)
	}
	hashedKey := getStringVal(sel.Rows[0][0])

	if _, err := db.Query(internalCtx,
		"UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND namespace_id = ?", id, nsID); err != nil {
		return fmt.Errorf("failed to revoke key %d: %w", id, err)
	}
	// Defense-in-depth: also drop the ownership row so no stale path can match.
	if hashedKey != "" {
		_, _ = db.Query(internalCtx,
			"DELETE FROM namespace_ownership WHERE namespace_id = ? AND owner_type = 'api_key' AND owner_id = ?", nsID, hashedKey)
	}

	// The key stops authenticating here, but the JWTs already exchanged from
	// it do not — they were minted with the raw key as their subject and
	// verify on the signature alone. Revoking the key used to leave every one
	// of them working for the rest of its lifetime, so an operator was told
	// the credential was gone while it still had up to fifteen minutes of full
	// access.
	//
	// A revocation of the subject covers all of them at once. It is recorded
	// under the hash, which is what this function has; the verifier looks
	// under both the subject it was given and its hash.
	if hashedKey != "" {
		if err := s.revocations.RevokeSubject(ctx, hashedKey,
			fmt.Sprintf("api key %d revoked in namespace %s", id, namespace), maxExchangedTokenLifetime); err != nil {
			return fmt.Errorf("the key was revoked but the tokens already issued from it were not, "+
				"so they would keep working until they expire: %w", err)
		}
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Action:    AuditKeyRevoked,
		Resource:  fmt.Sprintf("key %d", id),
		Result:    AuditSuccess,
	})
	return nil
}

// RevokeAllLegacy sweep-revokes every legacy (NULL/empty-scope) key in a
// namespace — the omnipotent keys that predate scoping (bugboard #148 cutover).
// Scoped keys (non-empty scopes) are left untouched. Returns the count revoked.
func (s *Service) RevokeAllLegacy(ctx context.Context, namespace string) (int, error) {
	if s.keyORM() == nil {
		return 0, fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	const legacyPred = "namespace_id = ? AND revoked_at IS NULL AND (scopes IS NULL OR TRIM(scopes) = '')"

	// Collect the hashed keys first so we can drop their ownership rows and
	// report the count.
	sel, err := db.Query(internalCtx, "SELECT key FROM api_keys WHERE "+legacyPred, nsID)
	if err != nil {
		return 0, fmt.Errorf("failed to enumerate legacy keys: %w", err)
	}
	var hashes []string
	if sel != nil {
		for _, row := range sel.Rows {
			if len(row) > 0 {
				if h := getStringVal(row[0]); h != "" {
					hashes = append(hashes, h)
				}
			}
		}
	}

	if _, err := db.Query(internalCtx,
		"UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE "+legacyPred, nsID); err != nil {
		return 0, fmt.Errorf("failed to revoke legacy keys: %w", err)
	}
	for _, h := range hashes {
		_, _ = db.Query(internalCtx,
			"DELETE FROM namespace_ownership WHERE namespace_id = ? AND owner_type = 'api_key' AND owner_id = ?", nsID, h)

		// Same reason as RevokeKey: the key stops authenticating, and the
		// tokens exchanged from it do not until they expire.
		if err := s.revocations.RevokeSubject(ctx, h,
			"legacy key swept in namespace "+namespace, maxExchangedTokenLifetime); err != nil {
			return len(hashes), fmt.Errorf("the legacy keys were revoked but the tokens already issued "+
				"from them were not, so they would keep working until they expire: %w", err)
		}
	}

	if len(hashes) > 0 {
		s.audit.Record(ctx, AuditEvent{
			Namespace: namespace,
			Action:    AuditKeysRevokedBulk,
			Result:    AuditSuccess,
			Metadata:  map[string]string{"count": strconv.Itoa(len(hashes))},
		})
	}
	return len(hashes), nil
}

// ListKeys returns non-secret metadata for every key in a namespace (bugboard
// #148). It never returns the key material.
func (s *Service) ListKeys(ctx context.Context, namespace string) ([]KeyInfo, error) {
	if s.keyORM() == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	res, err := db.Query(internalCtx,
		"SELECT id, name, scopes, created_at, last_used_at, revoked_at FROM api_keys WHERE namespace_id = ? ORDER BY id ASC", nsID)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	var out []KeyInfo
	if res == nil {
		return out, nil
	}
	for _, row := range res.Rows {
		if len(row) < 6 {
			continue
		}
		out = append(out, KeyInfo{
			ID:         toInt64(row[0]),
			Name:       getStringVal(row[1]),
			Scopes:     getStringVal(row[2]),
			CreatedAt:  getStringVal(row[3]),
			LastUsedAt: getStringVal(row[4]),
			RevokedAt:  getStringVal(row[5]),
		})
	}
	return out, nil
}

func toInt64(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func getStringVal(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
