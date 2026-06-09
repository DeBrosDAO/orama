package hostfunctions

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// secretsKeyBytes is the required length of the AES-256 encryption key.
const secretsKeyBytes = 32

// DBSecretsManager implements SecretsManager using the database.
type DBSecretsManager struct {
	db            rqlite.Client
	encryptionKey []byte // 32-byte AES-256 key
	logger        *zap.Logger
}

// Ensure DBSecretsManager implements SecretsManager.
var _ serverless.SecretsManager = (*DBSecretsManager)(nil)

// NewDBSecretsManager creates a secrets manager backed by the database.
//
// encryptionKeyHex must be a 32-byte AES-256 key, hex-encoded (64 chars).
//
// When encryptionKeyHex is empty the behaviour depends on allowEphemeral:
//   - allowEphemeral=false (production): returns an error. A misconfigured
//     node must fail loudly rather than silently generate a per-process
//     ephemeral key. With an ephemeral key, secrets encrypted by one
//     process cannot be decrypted by another (or after a restart), which
//     makes get_secret return garbage/errors (bugboard #837).
//   - allowEphemeral=true (tests/dev): generates a random per-process key
//     and logs a warning. Secrets will not persist across restarts.
func NewDBSecretsManager(db rqlite.Client, encryptionKeyHex string, allowEphemeral bool, logger *zap.Logger) (*DBSecretsManager, error) {
	var key []byte
	if encryptionKeyHex != "" {
		var err error
		key, err = hex.DecodeString(encryptionKeyHex)
		if err != nil || len(key) != secretsKeyBytes {
			return nil, fmt.Errorf("invalid secrets encryption key: must be %d bytes hex-encoded (%d hex chars)", secretsKeyBytes, secretsKeyBytes*2)
		}
	} else if allowEphemeral {
		// Generate a random per-process key (dev/test only).
		key = make([]byte, secretsKeyBytes)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate ephemeral secrets encryption key: %w", err)
		}
		logger.Warn("Generated random ephemeral secrets encryption key - secrets will NOT persist across restarts (dev/test only)")
	} else {
		return nil, fmt.Errorf("secrets encryption key is required: set secrets_encryption_key (see %s/secrets/secrets-encryption-key); without it secrets cannot be decrypted across processes or restarts (bugboard #837)", "~/.orama")
	}

	return &DBSecretsManager{
		db:            db,
		encryptionKey: key,
		logger:        logger,
	}, nil
}

// Set stores an encrypted secret.
func (s *DBSecretsManager) Set(ctx context.Context, namespace, name, value string) error {
	encrypted, err := s.encrypt([]byte(value))
	if err != nil {
		return fmt.Errorf("failed to encrypt secret: %w", err)
	}

	// Upsert the secret
	query := `
		INSERT INTO function_secrets (id, namespace, name, encrypted_value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, name) DO UPDATE SET
			encrypted_value = excluded.encrypted_value,
			updated_at = excluded.updated_at
	`

	id := fmt.Sprintf("%s:%s", namespace, name)
	now := time.Now()
	if _, err := s.db.Exec(ctx, query, id, namespace, name, encrypted, now, now); err != nil {
		return fmt.Errorf("failed to save secret: %w", err)
	}

	return nil
}

// Get retrieves a decrypted secret.
func (s *DBSecretsManager) Get(ctx context.Context, namespace, name string) (string, error) {
	query := `SELECT encrypted_value FROM function_secrets WHERE namespace = ? AND name = ?`

	var rows []struct {
		EncryptedValue []byte `db:"encrypted_value"`
	}
	if err := s.db.Query(ctx, &rows, query, namespace, name); err != nil {
		return "", fmt.Errorf("failed to query secret: %w", err)
	}

	if len(rows) == 0 {
		return "", serverless.ErrSecretNotFound
	}

	decrypted, err := s.decrypt(rows[0].EncryptedValue)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}

	return string(decrypted), nil
}

// List returns all secret names for a namespace.
func (s *DBSecretsManager) List(ctx context.Context, namespace string) ([]string, error) {
	query := `SELECT name FROM function_secrets WHERE namespace = ? ORDER BY name`

	var rows []struct {
		Name string `db:"name"`
	}
	if err := s.db.Query(ctx, &rows, query, namespace); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}

	return names, nil
}

// Delete removes a secret.
func (s *DBSecretsManager) Delete(ctx context.Context, namespace, name string) error {
	query := `DELETE FROM function_secrets WHERE namespace = ? AND name = ?`

	result, err := s.db.Exec(ctx, query, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return serverless.ErrSecretNotFound
	}

	return nil
}

// encrypt encrypts data using AES-256-GCM.
func (s *DBSecretsManager) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decrypt decrypts data using AES-256-GCM.
func (s *DBSecretsManager) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
