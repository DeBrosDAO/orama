// Package secrets provides application-level encryption for sensitive data stored in RQLite.
// Uses AES-256-GCM with HKDF key derivation from the cluster secret.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// Prefix for encrypted values to distinguish from plaintext during migration.
const encryptedPrefix = "enc:"

// DeriveKey derives a 32-byte AES-256 key from the cluster secret using HKDF-SHA256.
// The purpose string provides domain separation (e.g., "turn-encryption").
func DeriveKey(clusterSecret, purpose string) ([]byte, error) {
	if clusterSecret == "" {
		return nil, fmt.Errorf("cluster secret is empty")
	}
	reader := hkdf.New(sha256.New, []byte(clusterSecret), nil, []byte(purpose))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("HKDF key derivation failed: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using the given key.
// Returns a base64-encoded string prefixed with "enc:" for identification.
func Encrypt(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// nonce is prepended to ciphertext
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts an "enc:"-prefixed ciphertext string with AES-256-GCM.
// If the input is not prefixed with "enc:", it is returned as-is (plaintext passthrough
// for backward compatibility during migration).
func Decrypt(ciphertext string, key []byte) (string, error) {
	if !strings.HasPrefix(ciphertext, encryptedPrefix) {
		return ciphertext, nil // plaintext passthrough
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key or corrupted data): %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted returns true if the value has the "enc:" prefix.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encryptedPrefix)
}
