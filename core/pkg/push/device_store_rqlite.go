package push

import (
	"context"
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

// RqliteDeviceStore is a PushDeviceStore backed by RQLite + AES-256-GCM
// at-rest encryption of the push token.
type RqliteDeviceStore struct {
	db     rqlite.Client
	encKey []byte // derived once at construction
	logger *zap.Logger
}

// NewRqliteDeviceStore derives the per-cluster encryption key from the
// cluster secret and returns a ready-to-use store. The cluster secret is
// the same one used for other at-rest encryption (see pkg/secrets).
func NewRqliteDeviceStore(db rqlite.Client, clusterSecret string, logger *zap.Logger) (*RqliteDeviceStore, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	key, err := secrets.DeriveKey(clusterSecret, SecretsKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("derive push-device key: %w", err)
	}
	return &RqliteDeviceStore{
		db:     db,
		encKey: key,
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

// Upsert implements PushDeviceStore.
func (s *RqliteDeviceStore) Upsert(ctx context.Context, dev PushDevice) error {
	if dev.Namespace == "" || dev.UserID == "" || dev.DeviceID == "" {
		return fmt.Errorf("namespace, user_id, device_id required")
	}
	if dev.Provider == "" {
		return fmt.Errorf("provider required")
	}
	if dev.Token == "" {
		return ErrEmptyToken
	}

	encToken, err := secrets.Encrypt(dev.Token, s.encKey)
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}

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
	// UNIQUE constraint. On conflict we replace token + provider + metadata
	// while preserving the original id and created_at.
	query := `
		INSERT INTO push_devices
			(id, namespace, user_id, device_id, provider, token_encrypted,
			 platform, app_version, created_at, updated_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, user_id, device_id) DO UPDATE SET
			provider = excluded.provider,
			token_encrypted = excluded.token_encrypted,
			platform = excluded.platform,
			app_version = excluded.app_version,
			updated_at = excluded.updated_at,
			last_seen = excluded.last_seen
	`
	_, err = s.db.Exec(ctx, query,
		id, dev.Namespace, dev.UserID, dev.DeviceID, dev.Provider, encToken,
		dev.Platform, dev.AppVer, dev.CreatedAt, dev.UpdatedAt, dev.LastSeen,
	)
	if err != nil {
		return fmt.Errorf("upsert push device: %w", err)
	}

	// Token-exclusive registration (feat-135): a physical device (identified by
	// device_id) maps to exactly ONE active user. Evict any OTHER user's row for
	// the same (namespace, device_id). Rationale: when a user signs in on a device
	// a different account previously used, the previous account's row otherwise
	// stays live pointing at the SAME physical APNs/VoIP token — so the gateway
	// fans that account's pushes out to this device even after the user switched
	// away (the cross-account banner on multi-account-on-one-device setups). The
	// latest sign-in owns the physical device's push token. Eviction is
	// NAMESPACE-SCOPED (it can never touch another tenant's rows); token
	// encryption is non-deterministic (AES-GCM) so we match on the plaintext
	// device_id; the alert and ":voip" rows are evicted independently as each is
	// re-registered. Auto-cleans pre-existing orphans on the next sign-in.
	// Best-effort: a failed eviction must NOT fail the registration — the next
	// registration retries it.
	//
	// NOTE: this changes the default for every namespace on this gateway. It is a
	// sane, privacy-correct default (the latest sign-in claims the device's push
	// token), but a future tenant wanting "all signed-in accounts notified"
	// (Telegram-style) would need this gated behind a per-namespace flag. Tracked
	// on bugboard feat-135.
	evict := `DELETE FROM push_devices WHERE namespace = ? AND device_id = ? AND user_id != ?`
	if res, evErr := s.db.Exec(ctx, evict, dev.Namespace, dev.DeviceID, dev.UserID); evErr != nil {
		s.logger.Warn("token-exclusive eviction failed",
			zap.String("namespace", dev.Namespace),
			zap.String("device_id", dev.DeviceID),
			zap.Error(evErr))
	} else if n, _ := res.RowsAffected(); n > 0 {
		s.logger.Info("evicted stale device rows for re-registered device",
			zap.String("namespace", dev.Namespace),
			zap.String("device_id", dev.DeviceID),
			zap.String("owner", dev.UserID),
			zap.Int64("evicted", n))
	}
	return nil
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
