package auth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// KeyInfo is the non-secret metadata for one API key, returned by ListKeys.
// It never contains the key material (only the id, label, scopes, timestamps).
type KeyInfo struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Scopes string `json:"scopes"`
	// ExpiresAt is when the key stops working. Every key has one.
	ExpiresAt string `json:"expires_at"`
	// RotatedFrom is the key this one replaces, so a rotation in progress is
	// visible as a succession rather than as two unrelated keys.
	RotatedFrom int64  `json:"rotated_from,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
	RevokedAt   string `json:"revoked_at,omitempty"`
}

// KeyLifetime is how long a key lives unless the caller asks for something
// else.
//
// A key had no expiry at all: minting one produced a bearer token that worked
// until somebody remembered to revoke it, which is a thing nobody remembers.
// Ninety days is short enough that a key leaked and forgotten stops working on
// its own, and long enough that rotating is a quarterly chore rather than a
// weekly one.
const KeyLifetime = 90 * 24 * time.Hour

// MaxKeyLifetime is the longest a key may live. Past a year the expiry is not
// doing anything a revocation would not do better.
const MaxKeyLifetime = 365 * 24 * time.Hour

// KeyOptions is what a mint may vary.
type KeyOptions struct {
	Label string
	// Lifetime is how long the key lives. Zero means KeyLifetime.
	Lifetime time.Duration
	// RotatedFrom is the key this one replaces, when it is a rotation.
	RotatedFrom int64
}

// IssueScopedKey mints a fresh API key for a namespace with an explicit,
// already-normalized grant set (bugboard #148). Unlike GetOrCreateAPIKey it is
// NOT wallet-linked and always creates a NEW key, so a namespace can hold
// several keys with different scopes (e.g. app-runtime + admin). Returns the
// raw key (shown once) and its row id.
func (s *Service) IssueScopedKey(ctx context.Context, namespace, storedScopes string, opts KeyOptions) (string, int64, error) {
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

	lifetime := opts.Lifetime
	if lifetime == 0 {
		lifetime = KeyLifetime
	}
	if lifetime < 0 || lifetime > MaxKeyLifetime {
		return "", 0, fmt.Errorf("a key lives between a moment and %d days; %s was asked for",
			int(MaxKeyLifetime.Hours()/24), lifetime)
	}
	expiresAt := time.Now().Add(lifetime).UTC()

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}

	// The label in the key says how bad a leak of it is; the scopes column is
	// what decides what it may reach.
	rawKey, err := NewKey(KeyTypeFor(storedScopes))
	if err != nil {
		return "", 0, err
	}
	hashedKey := s.HashAPIKey(rawKey)

	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	var rotatedFrom interface{}
	if opts.RotatedFrom != 0 {
		rotatedFrom = opts.RotatedFrom
	}
	if _, err := db.Query(internalCtx,
		"INSERT INTO api_keys(key, name, namespace_id, scopes, expires_at, rotated_from) VALUES (?, ?, ?, ?, ?, ?)",
		hashedKey, opts.Label, nsID, storedScopes, expiresAt.Format(sqliteTime), rotatedFrom,
	); err != nil {
		return "", 0, fmt.Errorf("failed to store api key: %w", err)
	}

	// Record the key's membership of the namespace (hashed, mirroring
	// GetOrCreateAPIKey) so the authorization middleware recognizes it. A key
	// with no grant is refused everywhere, so a failure here is the caller's
	// problem and not something to swallow.
	if err := s.grantServiceAccount(ctx, db, nsID, namespace, hashedKey, RoleForScopes(storedScopes)); err != nil {
		return "", 0, err
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
		Metadata: map[string]string{
			"label":      opts.Label,
			"scopes":     storedScopes,
			"expires_at": expiresAt.Format(sqliteTime),
		},
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
	// Defense-in-depth: end the key's membership too, so no stale path can
	// match it.
	if hashedKey != "" {
		if err := s.revokeServiceAccount(ctx, db, nsID, hashedKey); err != nil {
			return fmt.Errorf("key %d was revoked but its grant was not, so a stale path could still match it: %w", id, err)
		}
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
		if err := s.revokeServiceAccount(ctx, db, nsID, h); err != nil {
			return len(hashes), fmt.Errorf("the legacy keys were revoked but their grants were not: %w", err)
		}

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
		"SELECT id, name, scopes, created_at, last_used_at, revoked_at, expires_at, rotated_from FROM api_keys WHERE namespace_id = ? ORDER BY id ASC", nsID)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	var out []KeyInfo
	if res == nil {
		return out, nil
	}
	for _, row := range res.Rows {
		if len(row) < 8 {
			continue
		}
		out = append(out, KeyInfo{
			ID:          toInt64(row[0]),
			Name:        getStringVal(row[1]),
			Scopes:      getStringVal(row[2]),
			CreatedAt:   getStringVal(row[3]),
			LastUsedAt:  getStringVal(row[4]),
			RevokedAt:   getStringVal(row[5]),
			ExpiresAt:   getStringVal(row[6]),
			RotatedFrom: toInt64(row[7]),
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

const (
	// DefaultRotationOverlap is how long a rotated key keeps working.
	//
	// Rotating by minting a successor and revoking the original in the same
	// breath is an outage: whatever is deployed with the old key stops the
	// moment the new one exists, and there is no window to roll the new one
	// out. A week is long enough to redeploy and short enough that the old key
	// is not forgotten.
	DefaultRotationOverlap = 7 * 24 * time.Hour

	// MaxRotationOverlap is the longest overlap on offer. Past a month there
	// are two live keys, not a rotation in progress.
	MaxRotationOverlap = 30 * 24 * time.Hour
)

// RotateOptions is what a rotation may vary.
type RotateOptions struct {
	// Lifetime is how long the successor lives. Zero means KeyLifetime.
	Lifetime time.Duration
	// Overlap is how long the original keeps working. Zero means
	// DefaultRotationOverlap; a zero-length overlap is asked for explicitly by
	// revoking instead.
	Overlap time.Duration
}

// RotateKey mints a successor to a key and shortens the original's life to the
// overlap. Returns the new raw key (shown once) and its id.
//
// The original is not revoked. Its expiry is brought forward instead, so it
// keeps working for exactly the overlap and then stops on its own — which is
// what makes this a rotation rather than a replacement plus an outage. An
// operator who wants it gone now revokes it.
func (s *Service) RotateKey(ctx context.Context, namespace string, id int64, opts RotateOptions) (string, int64, error) {
	if s.db == nil {
		return "", 0, fmt.Errorf("rotating a key requires the rqlite client (SetRqliteClient): without an affected-row count a rotation that changed nothing looks like one that worked")
	}
	overlap := opts.Overlap
	if overlap == 0 {
		overlap = DefaultRotationOverlap
	}
	if overlap < 0 || overlap > MaxRotationOverlap {
		return "", 0, fmt.Errorf("an overlap is between nothing and %d days",
			int(MaxRotationOverlap.Hours()/24))
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}

	internalCtx := client.WithInternalAuth(ctx)
	db := s.keyORM().Database()

	// The successor carries the original's grants and label: a rotation is the
	// same credential with new material, and quietly widening or narrowing it
	// would make rotating a thing people avoid.
	sel, err := db.Query(internalCtx,
		"SELECT scopes, name FROM api_keys WHERE id = ? AND namespace_id = ? AND revoked_at IS NULL LIMIT 1",
		id, nsID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read key %d in namespace %q: %w", id, namespace, err)
	}
	if sel == nil || sel.Count == 0 || len(sel.Rows) == 0 || len(sel.Rows[0]) < 2 {
		return "", 0, fmt.Errorf("no active key with id %d in namespace %q", id, namespace)
	}
	scopes, label := getStringVal(sel.Rows[0][0]), getStringVal(sel.Rows[0][1])

	rawKey, newID, err := s.IssueScopedKey(ctx, namespace, scopes, KeyOptions{
		Label:       label,
		Lifetime:    opts.Lifetime,
		RotatedFrom: id,
	})
	if err != nil {
		return "", 0, err
	}

	// Only ever shorten. A key already expiring inside the overlap must not be
	// given more life by being rotated.
	res, err := s.db.Exec(internalCtx,
		`UPDATE api_keys SET expires_at = ?
		  WHERE id = ? AND namespace_id = ? AND revoked_at IS NULL AND expires_at > ?`,
		time.Now().Add(overlap).UTC().Format(sqliteTime), id, nsID,
		time.Now().Add(overlap).UTC().Format(sqliteTime))
	if err != nil {
		return "", 0, fmt.Errorf("key %d was rotated but the original's life was not shortened, "+
			"so both keys are live for the original's full term: %w", id, err)
	}
	if _, err := res.RowsAffected(); err != nil {
		return "", 0, fmt.Errorf("key %d was rotated but the original's new expiry could not be confirmed: %w", id, err)
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Action:    AuditKeyRotated,
		Resource:  fmt.Sprintf("key %d", id),
		Result:    AuditSuccess,
		Metadata: map[string]string{
			"successor": fmt.Sprintf("%d", newID),
			"overlap":   overlap.String(),
		},
	})
	return rawKey, newID, nil
}
