package credentials

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

// purposeNamespacePushCredentials is the HKDF "purpose" string for the
// per-provider credentials encryption key. Distinct from
// "namespace-push-config" (used by the legacy 026 columns) so a key
// compromise in one domain doesn't extend to the other.
const purposeNamespacePushCredentials = "namespace-push-credentials"

// rqliteStore is the production Store — persists credentials in the
// `namespace_push_credentials` table (migration 028) with AES-256-GCM
// encryption of the JSON blob.
type rqliteStore struct {
	db     rqlite.Client
	encKey []byte
	logger *zap.Logger
}

// NewRqliteStore wires the store to RQLite with a cluster-secret-
// derived encryption key. Returns an error if clusterSecret is empty —
// we refuse to operate without encryption, otherwise an operator-typo
// could ship plaintext p8 keys to disk.
func NewRqliteStore(db rqlite.Client, clusterSecret string, logger *zap.Logger) (Store, error) {
	if clusterSecret == "" {
		return nil, fmt.Errorf("credentials store: cluster secret required for credential encryption")
	}
	key, err := secrets.DeriveKey(clusterSecret, purposeNamespacePushCredentials)
	if err != nil {
		return nil, fmt.Errorf("credentials store: derive key: %w", err)
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &rqliteStore{db: db, encKey: key, logger: logger}, nil
}

// Get returns the credential, decrypting the JSON blob. Returns
// ErrNotFound if no row exists for (namespace, provider).
func (s *rqliteStore) Get(ctx context.Context, namespace, provider string) (*Credential, error) {
	if namespace == "" {
		return nil, ErrInvalidNamespace
	}
	if provider == "" {
		return nil, ErrInvalidProvider
	}
	const q = `SELECT namespace, provider, credentials_json, updated_at, updated_by
		FROM namespace_push_credentials
		WHERE namespace = ? AND provider = ? LIMIT 1`
	var rows []struct {
		Namespace       string `db:"namespace"`
		Provider        string `db:"provider"`
		CredentialsJSON string `db:"credentials_json"`
		UpdatedAt       int64  `db:"updated_at"`
		UpdatedBy       string `db:"updated_by"`
	}
	if err := s.db.Query(ctx, &rows, q, namespace, provider); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("credentials Get: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	r := rows[0]
	plain, err := secrets.Decrypt(r.CredentialsJSON, s.encKey)
	if err != nil {
		return nil, fmt.Errorf("credentials Get: decrypt: %w", err)
	}
	return &Credential{
		Namespace: r.Namespace,
		Provider:  r.Provider,
		JSON:      []byte(plain),
		UpdatedAt: r.UpdatedAt,
		UpdatedBy: r.UpdatedBy,
	}, nil
}

// Upsert writes or replaces the credential row. The JSON blob is
// AES-256-GCM-encrypted before storage. The caller is responsible for
// validating the JSON against the provider's schema BEFORE calling
// Upsert — this method does not invoke the Validator registry.
func (s *rqliteStore) Upsert(ctx context.Context, cred Credential) error {
	if cred.Namespace == "" {
		return ErrInvalidNamespace
	}
	if cred.Provider == "" {
		return ErrInvalidProvider
	}
	if len(cred.JSON) == 0 {
		return fmt.Errorf("credentials Upsert: empty JSON payload")
	}
	enc, err := secrets.Encrypt(string(cred.JSON), s.encKey)
	if err != nil {
		return fmt.Errorf("credentials Upsert: encrypt: %w", err)
	}
	updatedAt := cred.UpdatedAt
	if updatedAt == 0 {
		updatedAt = time.Now().Unix()
	}
	const q = `INSERT INTO namespace_push_credentials
		(namespace, provider, credentials_json, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(namespace, provider) DO UPDATE SET
			credentials_json = excluded.credentials_json,
			updated_at       = excluded.updated_at,
			updated_by       = excluded.updated_by`
	if _, err := s.db.Exec(ctx, q,
		cred.Namespace, cred.Provider, enc, updatedAt, cred.UpdatedBy,
	); err != nil {
		return fmt.Errorf("credentials Upsert: %w", err)
	}
	return nil
}

// Delete clears the (namespace, provider) row. Idempotent.
func (s *rqliteStore) Delete(ctx context.Context, namespace, provider string) error {
	if namespace == "" {
		return ErrInvalidNamespace
	}
	if provider == "" {
		return ErrInvalidProvider
	}
	const q = `DELETE FROM namespace_push_credentials WHERE namespace = ? AND provider = ?`
	if _, err := s.db.Exec(ctx, q, namespace, provider); err != nil {
		return fmt.Errorf("credentials Delete: %w", err)
	}
	return nil
}

// ListProviders returns the provider names that have a row for the
// namespace. Used by the credentials-summary endpoint to render the
// "what's configured" view without leaking secret material.
func (s *rqliteStore) ListProviders(ctx context.Context, namespace string) ([]string, error) {
	if namespace == "" {
		return nil, ErrInvalidNamespace
	}
	const q = `SELECT provider FROM namespace_push_credentials WHERE namespace = ?`
	var rows []struct {
		Provider string `db:"provider"`
	}
	if err := s.db.Query(ctx, &rows, q, namespace); err != nil {
		return nil, fmt.Errorf("credentials ListProviders: %w", err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Provider
	}
	return out, nil
}
