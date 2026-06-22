package push

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SecretsKeyPurpose is the HKDF purpose string for push token encryption.
// Used in pkg/secrets.DeriveKey to derive a domain-separated AES key.
const SecretsKeyPurpose = "push-device-tokens"

// TokenFingerprintPurpose is the HKDF purpose string for the token-exclusivity
// fingerprint key (bugboard #981). Domain-separated from the encryption key so
// the keyed fingerprint can never be confused with (or weaken) the AES key.
const TokenFingerprintPurpose = "push-device-token-fp"

// RqliteDeviceStore is a PushDeviceStore backed by RQLite + AES-256-GCM
// at-rest encryption of the push token.
type RqliteDeviceStore struct {
	db     rqlite.Client
	encKey []byte // derived once at construction (token encryption)
	fpKey  []byte // derived once at construction (token fingerprint, bugboard #981)
	logger *zap.Logger
}

// NewRqliteDeviceStore derives the per-cluster encryption + fingerprint keys
// from the cluster secret and returns a ready-to-use store. The cluster secret
// is the same one used for other at-rest encryption (see pkg/secrets).
func NewRqliteDeviceStore(db rqlite.Client, clusterSecret string, logger *zap.Logger) (*RqliteDeviceStore, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	key, err := secrets.DeriveKey(clusterSecret, SecretsKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive push-device key: %w", err)
	}
	fpKey, err := secrets.DeriveKey(clusterSecret, TokenFingerprintPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive push-device fingerprint key: %w", err)
	}
	return &RqliteDeviceStore{
		db:     db,
		encKey: key,
		fpKey:  fpKey,
		logger: logger.Named("push-store"),
	}, nil
}

// deviceRow is the scan target for SELECT queries.
type deviceRow struct {
	ID             string
	Namespace      string
	UserID         string
	DeviceID       string
	Provider       string
	TokenEncrypted string
	Platform       string
	AppVersion     string
	CreatedAt      int64
	UpdatedAt      int64
	LastSeen       int64
}

// selfRow is the scan target for resolving a row's identity + insertion order
// (SQLite rowid) after an upsert.
type selfRow struct {
	Rowid int64
	ID    string
}

// tokenFingerprint returns a deterministic keyed fingerprint of the plaintext
// token (HMAC-SHA256, hex). Because token_encrypted uses a random GCM nonce it
// cannot be matched in SQL; the fingerprint is what makes the same physical
// token identifiable across rows for token-exclusive eviction (bugboard #981).
func (s *RqliteDeviceStore) tokenFingerprint(token string) string {
	mac := hmac.New(sha256.New, s.fpKey)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// Upsert implements PushDeviceStore. It registers or updates the device and
// returns the persisted row id. As part of registration it enforces
// token-exclusivity (bugboard #981): any OTHER row in the same namespace
// carrying the same physical token is evicted, so one APNs/VoIP token maps to a
// single active owner.
func (s *RqliteDeviceStore) Upsert(ctx context.Context, dev PushDevice) (string, error) {
	if dev.Namespace == "" || dev.UserID == "" || dev.DeviceID == "" {
		return "", fmt.Errorf("namespace, user_id, device_id required")
	}
	if dev.Provider == "" {
		return "", fmt.Errorf("provider required")
	}
	if dev.Token == "" {
		return "", ErrEmptyToken
	}

	encToken, err := secrets.Encrypt(dev.Token, s.encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt token: %w", err)
	}
	tokenFP := s.tokenFingerprint(dev.Token)

	now := time.Now().Unix()
	if dev.CreatedAt == 0 {
		dev.CreatedAt = now
	}
	dev.UpdatedAt = now

	id := dev.ID
	if id == "" {
		id = uuid.New().String()
	}

	// SQLite UPSERT keyed on (namespace, user_id, device_id) per the migration's
	// UNIQUE constraint. On conflict we replace token + fingerprint + provider +
	// metadata while preserving the original id and created_at.
	query := `
		INSERT INTO push_devices
			(id, namespace, user_id, device_id, provider, token_encrypted, token_fp,
			 platform, app_version, created_at, updated_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, user_id, device_id) DO UPDATE SET
			provider = excluded.provider,
			token_encrypted = excluded.token_encrypted,
			token_fp = excluded.token_fp,
			platform = excluded.platform,
			app_version = excluded.app_version,
			updated_at = excluded.updated_at,
			last_seen = excluded.last_seen
	`
	_, err = s.db.Exec(ctx, query,
		id, dev.Namespace, dev.UserID, dev.DeviceID, dev.Provider, encToken, tokenFP,
		dev.Platform, dev.AppVer, dev.CreatedAt, dev.UpdatedAt, dev.LastSeen,
	)
	if err != nil {
		return "", fmt.Errorf("upsert push device: %w", err)
	}

	// Resolve the persisted row: on INSERT the id is `id` above, but on a
	// CONFLICT the pre-existing row's id is preserved, so re-read by the unique
	// key to return the real id (bugboard #981 ask 2). We also read the SQLite
	// rowid (monotonic by insertion order) to drive token-exclusive eviction.
	self, err := s.resolveSelf(ctx, dev.Namespace, dev.UserID, dev.DeviceID)
	if err != nil {
		// The write succeeded; fall back to the candidate id so registration
		// still returns a usable value instead of failing. Skip eviction since
		// we can't determine the keeper's insertion order safely.
		s.logger.Warn("resolve persisted device row failed; returning candidate id, skipping eviction",
			zap.String("namespace", dev.Namespace),
			zap.Error(err))
		return id, nil
	}

	// Token-exclusive eviction (bugboard #981). Best-effort: a failure here must
	// not fail the registration that already succeeded.
	if err := s.evictOtherOwnersOfToken(ctx, dev.Namespace, tokenFP, self.Rowid); err != nil {
		s.logger.Warn("token-exclusive eviction failed (registration still succeeded)",
			zap.String("namespace", dev.Namespace),
			zap.Error(err))
	}

	return self.ID, nil
}

// resolveSelf returns the stored row id + rowid for the unique (namespace,
// user_id, device_id) key.
func (s *RqliteDeviceStore) resolveSelf(ctx context.Context, namespace, userID, deviceID string) (selfRow, error) {
	var rows []selfRow
	err := s.db.Query(ctx, &rows,
		`SELECT rowid, id FROM push_devices WHERE namespace = ? AND user_id = ? AND device_id = ?`,
		namespace, userID, deviceID)
	if err != nil {
		return selfRow{}, fmt.Errorf("query device row: %w", err)
	}
	if len(rows) == 0 {
		return selfRow{}, fmt.Errorf("device not found after upsert")
	}
	return rows[0], nil
}

// evictOtherOwnersOfToken deletes every OTHER row carrying the same physical
// token within the namespace that was inserted BEFORE the just-registered row
// (keepRowid), leaving the most-recently-registered owner as the single one.
//
// Ordering is by SQLite rowid (monotonic insertion order), NOT updated_at:
// updated_at is unix seconds, so a fast account switch (logout A → login B
// within the same second) would otherwise tie and fail to evict A — exactly the
// cross-account case this fixes. rowid also makes concurrent same-token
// registrations by two users race-safe: rqlite serializes the inserts, so each
// gets a distinct rowid; "rowid < keepRowid" means the newest insertion is
// never eligible for eviction, so exactly one survivor remains regardless of
// interleaving (a naive "id != self" delete run by both could delete BOTH,
// leaving the device with no registration). A device legitimately re-claiming
// its own token after being evicted re-inserts with a fresh (higher) rowid and
// wins, which is correct — the currently-active account owns the device token.
func (s *RqliteDeviceStore) evictOtherOwnersOfToken(ctx context.Context, namespace, tokenFP string, keepRowid int64) error {
	if tokenFP == "" {
		return nil
	}
	res, err := s.db.Exec(ctx,
		`DELETE FROM push_devices WHERE namespace = ? AND token_fp = ? AND rowid < ?`,
		namespace, tokenFP, keepRowid)
	if err != nil {
		return fmt.Errorf("evict other owners of token: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.logger.Info("evicted stale owners of re-registered push token",
			zap.String("namespace", namespace),
			zap.Int64("evicted", n))
	}
	return nil
}

// BackfillTokenFP populates token_fp for rows registered before the bugboard
// #981 migration so token-exclusive eviction also covers pre-existing orphans
// (not just rows registered after the upgrade). Best-effort + idempotent: it
// only touches rows with a missing fingerprint, recomputes it from the
// decrypted token, and writes it back. Safe to run concurrently on multiple
// gateways — the fingerprint is deterministic, so concurrent writers converge.
// It does NOT evict; eviction stays a register-time decision.
func (s *RqliteDeviceStore) BackfillTokenFP(ctx context.Context) (int, error) {
	var rows []deviceRow
	err := s.db.Query(ctx, &rows,
		`SELECT id, token_encrypted FROM push_devices WHERE token_fp IS NULL OR token_fp = ''`)
	if err != nil {
		return 0, fmt.Errorf("query rows needing token_fp backfill: %w", err)
	}
	updated := 0
	for _, r := range rows {
		token, err := secrets.Decrypt(r.TokenEncrypted, s.encKey)
		if err != nil {
			s.logger.Warn("backfill: failed to decrypt token; skipping row",
				zap.String("device_row_id", r.ID),
				zap.Error(err))
			continue
		}
		fp := s.tokenFingerprint(token)
		if _, err := s.db.Exec(ctx,
			`UPDATE push_devices SET token_fp = ? WHERE id = ?`, fp, r.ID); err != nil {
			s.logger.Warn("backfill: failed to write token_fp; skipping row",
				zap.String("device_row_id", r.ID),
				zap.Error(err))
			continue
		}
		updated++
	}
	if updated > 0 {
		s.logger.Info("backfilled token_fp for pre-#981 push devices",
			zap.Int("updated", updated))
	}
	return updated, nil
}

// Delete implements PushDeviceStore.
func (s *RqliteDeviceStore) Delete(ctx context.Context, namespace, id string) error {
	if namespace == "" || id == "" {
		return fmt.Errorf("namespace and id required")
	}
	query := `DELETE FROM push_devices WHERE id = ? AND namespace = ?`
	res, err := s.db.Exec(ctx, query, id, namespace)
	if err != nil {
		return fmt.Errorf("delete push device: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("push device not found: %s", id)
	}
	return nil
}

// ListForUser implements PushDeviceStore. Returns devices with decrypted tokens.
// Caller MUST treat tokens as sensitive.
func (s *RqliteDeviceStore) ListForUser(ctx context.Context, namespace, userID string) ([]PushDevice, error) {
	if namespace == "" || userID == "" {
		return nil, nil
	}
	query := `
		SELECT id, namespace, user_id, device_id, provider, token_encrypted,
			COALESCE(platform, ''), COALESCE(app_version, ''),
			created_at, updated_at, COALESCE(last_seen, 0)
		FROM push_devices
		WHERE namespace = ? AND user_id = ?
	`
	var rows []deviceRow
	if err := s.db.Query(ctx, &rows, query, namespace, userID); err != nil {
		return nil, fmt.Errorf("query push devices: %w", err)
	}

	out := make([]PushDevice, 0, len(rows))
	for _, r := range rows {
		token, err := secrets.Decrypt(r.TokenEncrypted, s.encKey)
		if err != nil {
			s.logger.Warn("failed to decrypt push token; skipping device",
				zap.String("device_id", r.DeviceID),
				zap.Error(err))
			continue
		}
		out = append(out, PushDevice{
			ID:        r.ID,
			Namespace: r.Namespace,
			UserID:    r.UserID,
			DeviceID:  r.DeviceID,
			Provider:  r.Provider,
			Token:     token,
			Platform:  r.Platform,
			AppVer:    r.AppVersion,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			LastSeen:  r.LastSeen,
		})
	}
	return out, nil
}
