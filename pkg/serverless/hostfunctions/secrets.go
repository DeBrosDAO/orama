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

// DBSecretsManager implements SecretsManager using the database.
type DBSecretsManager struct {
	db            rqlite.Client
	encryptionKey []byte // 32-byte AES-256 key
	logger        *zap.Logger
}

// Ensure DBSecretsManager implements SecretsManager.
var _ serverless.SecretsManager = (*DBSecretsManager)(nil)

// NewDBSecretsManager creates a secrets manager backed by the database.
func NewDBSecretsManager(db rqlite.Client, encryptionKeyHex string, logger *zap.Logger) (*DBSecretsManager, error) {
	var key []byte
	if encryptionKeyHex != "" {
		var err error
		key, err = hex.DecodeString(encryptionKeyHex)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid encryption key: must be 32 bytes hex-encoded")
		}
	} else {
		// Generate a random key if none provided
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate encryption key: %w", err)
		}
		logger.Warn("Generated random secrets encryption key - secrets will not persist across restarts")
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
