package push

// config_store.go — per-namespace push provider configuration with
// encrypted credential storage.
//
// Tenants set their own ntfy / expo credentials via the HTTP config
// endpoint; this store persists them in RQLite (`namespace_push_config`),
// encrypting sensitive fields at rest using a key derived from the
// cluster secret. The gateway YAML config remains as a global fallback —
// a row here OVERRIDES the YAML for that namespace, absent rows inherit
// YAML defaults.
//
// See bug #220 follow-up. Pattern intentionally generic (mirrors
// push_devices encryption + storage), so the next "tenant should
// self-serve this knob" need (rate limits, custom domains, etc.) can
// drop a similar table + handler in.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// purposeNamespacePushConfig is the HKDF "purpose" string for the
// per-namespace push-config encryption key. Distinct from other purposes
// so a key compromise in one domain doesn't compromise this one.
const purposeNamespacePushConfig = "namespace-push-config"

// Config is the in-memory representation of one namespace's push provider
// configuration. Sensitive fields are PLAINTEXT in this struct — encryption
// happens at the storage boundary.
type Config struct {
	Namespace       string
	NtfyBaseURL     string
	NtfyAuthToken   string
	ExpoAccessToken string
	UpdatedAt       int64
	UpdatedBy       string
}

// IsEmpty returns true when this config has no provider credentials set —
// the namespace is effectively "no push" and the manager should fall back
// to the gateway YAML defaults (or refuse if those are also empty).
func (c *Config) IsEmpty() bool {
	if c == nil {
		return true
	}
	return c.NtfyBaseURL == "" && c.ExpoAccessToken == ""
}

// Redacted returns a copy of this Config safe for return over HTTP — the
// auth-token / access-token fields are zeroed out and replaced with a
// boolean (`HasNtfyAuthToken` / `HasExpoAccessToken`) at the response
// shape level. Use this to avoid ever sending the secrets back.
type RedactedConfig struct {
	Namespace          string `json:"namespace"`
	NtfyBaseURL        string `json:"ntfy_base_url,omitempty"`
	HasNtfyAuthToken   bool   `json:"has_ntfy_auth_token"`
	HasExpoAccessToken bool   `json:"has_expo_access_token"`
	UpdatedAt          int64  `json:"updated_at,omitempty"`
	UpdatedBy          string `json:"updated_by,omitempty"`
}

// Redacted returns the redacted view safe for GET responses.
func (c *Config) Redacted() RedactedConfig {
	if c == nil {
		return RedactedConfig{}
	}
	return RedactedConfig{
		Namespace:          c.Namespace,
		NtfyBaseURL:        c.NtfyBaseURL,
		HasNtfyAuthToken:   c.NtfyAuthToken != "",
		HasExpoAccessToken: c.ExpoAccessToken != "",
		UpdatedAt:          c.UpdatedAt,
		UpdatedBy:          c.UpdatedBy,
	}
}

// ConfigStore is the persistence layer for per-namespace push config.
// Errors returned for "no row for namespace" → ErrConfigNotFound.
type ConfigStore interface {
	Get(ctx context.Context, namespace string) (*Config, error)
	Upsert(ctx context.Context, cfg Config) error
	Delete(ctx context.Context, namespace string) error
}

// ErrConfigNotFound is returned by Get when no row exists for the
// namespace. Callers fall back to YAML defaults in that case.
var ErrConfigNotFound = errors.New("push config not found for namespace")

// rqliteConfigStore implements ConfigStore over RQLite + pkg/secrets.
type rqliteConfigStore struct {
	db     rqlite.Client
	encKey []byte
	logger *zap.Logger
}

// NewRqliteConfigStore wires the store to RQLite with a cluster-secret-
// derived encryption key for credential fields.
func NewRqliteConfigStore(db rqlite.Client, clusterSecret string, logger *zap.Logger) (ConfigStore, error) {
	if clusterSecret == "" {
		return nil, fmt.Errorf("push config store: cluster secret required for credential encryption")
	}
	key, err := secrets.DeriveKey(clusterSecret, purposeNamespacePushConfig)
	if err != nil {
		return nil, fmt.Errorf("push config store: derive key: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &rqliteConfigStore{db: db, encKey: key, logger: logger}, nil
}

// Get returns the namespace's config, decrypting sensitive fields. Returns
// ErrConfigNotFound if no row exists — caller decides whether to fall
// back to a default.
func (s *rqliteConfigStore) Get(ctx context.Context, namespace string) (*Config, error) {
	if namespace == "" {
		return nil, fmt.Errorf("push config: namespace required")
	}
	const q = `SELECT namespace, ntfy_base_url, ntfy_auth_token_encrypted,
		expo_access_token_encrypted, updated_at, updated_by
		FROM namespace_push_config WHERE namespace = ? LIMIT 1`

	var rows []struct {
		Namespace                string `db:"namespace"`
		NtfyBaseURL              string `db:"ntfy_base_url"`
		NtfyAuthTokenEncrypted   string `db:"ntfy_auth_token_encrypted"`
		ExpoAccessTokenEncrypted string `db:"expo_access_token_encrypted"`
		UpdatedAt                int64  `db:"updated_at"`
		UpdatedBy                string `db:"updated_by"`
	}
	if err := s.db.Query(ctx, &rows, q, namespace); err != nil {
		// gorqlite stdlib path collapses "0 rows" to nil err; only real
		// errors get here. ErrNoRows from sql is also treated below.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConfigNotFound
		}
		return nil, fmt.Errorf("push config: query: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrConfigNotFound
	}
	r := rows[0]

	cfg := &Config{
		Namespace:   r.Namespace,
		NtfyBaseURL: r.NtfyBaseURL,
		UpdatedAt:   r.UpdatedAt,
		UpdatedBy:   r.UpdatedBy,
	}
	if r.NtfyAuthTokenEncrypted != "" {
		v, err := secrets.Decrypt(r.NtfyAuthTokenEncrypted, s.encKey)
		if err != nil {
			return nil, fmt.Errorf("push config: decrypt ntfy auth token: %w", err)
		}
		cfg.NtfyAuthToken = v
	}
	if r.ExpoAccessTokenEncrypted != "" {
		v, err := secrets.Decrypt(r.ExpoAccessTokenEncrypted, s.encKey)
		if err != nil {
			return nil, fmt.Errorf("push config: decrypt expo access token: %w", err)
		}
		cfg.ExpoAccessToken = v
	}
	return cfg, nil
}

// Upsert writes or replaces the namespace's config. Sensitive fields are
// encrypted before storage. Empty strings for credential fields clear them.
func (s *rqliteConfigStore) Upsert(ctx context.Context, cfg Config) error {
	if cfg.Namespace == "" {
		return fmt.Errorf("push config: namespace required")
	}

	var ntfyEnc, expoEnc string
	if cfg.NtfyAuthToken != "" {
		v, err := secrets.Encrypt(cfg.NtfyAuthToken, s.encKey)
		if err != nil {
			return fmt.Errorf("push config: encrypt ntfy auth token: %w", err)
		}
		ntfyEnc = v
	}
	if cfg.ExpoAccessToken != "" {
		v, err := secrets.Encrypt(cfg.ExpoAccessToken, s.encKey)
		if err != nil {
			return fmt.Errorf("push config: encrypt expo access token: %w", err)
		}
		expoEnc = v
	}
	updatedAt := cfg.UpdatedAt
	if updatedAt == 0 {
		updatedAt = time.Now().Unix()
	}

	const q = `INSERT INTO namespace_push_config (
		namespace, ntfy_base_url, ntfy_auth_token_encrypted,
		expo_access_token_encrypted, updated_at, updated_by
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(namespace) DO UPDATE SET
		ntfy_base_url               = excluded.ntfy_base_url,
		ntfy_auth_token_encrypted   = excluded.ntfy_auth_token_encrypted,
		expo_access_token_encrypted = excluded.expo_access_token_encrypted,
		updated_at                  = excluded.updated_at,
		updated_by                  = excluded.updated_by`

	if _, err := s.db.Exec(ctx, q,
		cfg.Namespace, cfg.NtfyBaseURL, ntfyEnc, expoEnc, updatedAt, cfg.UpdatedBy,
	); err != nil {
		return fmt.Errorf("push config: upsert: %w", err)
	}
	return nil
}

// Delete clears the namespace's config row. After this the manager falls
// back to YAML defaults (or refuses if no defaults set).
func (s *rqliteConfigStore) Delete(ctx context.Context, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("push config: namespace required")
	}
	const q = `DELETE FROM namespace_push_config WHERE namespace = ?`
	if _, err := s.db.Exec(ctx, q, namespace); err != nil {
		return fmt.Errorf("push config: delete: %w", err)
	}
	return nil
}
