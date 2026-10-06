// Package secrets provides application-level encryption for sensitive data stored in RQLite.
//
// Ciphertext is AES-256-GCM. Keys are HKDF-SHA256 of an IKM (the encryption
// root) and a purpose label. The IKM used to be the cluster secret; it is now
// its own value so stored secrets can rotate without partitioning IPFS-Cluster
// or the mesh bearer.
//
// Two on-disk envelopes exist:
//
//	enc:<base64>                 — legacy; still the default write until an
//	                               operator rotate rewrites the rows
//	enc:v1:<keyid>:<base64>      — versioned; keyid selects the generation
//
// Decrypt fails closed on anything else. Callers that still hold a leftover
// plaintext column (deployment env, TURN) check IsEncrypted themselves and
// never send unprefixed input here.
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

const (
	// encryptedPrefix is the legacy envelope. New writes keep using it until
	// an operator rotate, so a mixed-version rolling upgrade can still read
	// what a new binary writes.
	encryptedPrefix = "enc:"

	// versionedPrefix is the envelope that carries a key id.
	versionedPrefix = "enc:v1:"
)

// DeriveKey derives a 32-byte AES-256 key from ikm using HKDF-SHA256.
// The purpose string provides domain separation (e.g., "turn-encryption").
//
// ikm is the encryption root, not the cluster secret, for stored ciphertext.
// A nil salt is RFC 5869-legal for high-entropy IKM and is kept so existing
// rows stay decryptable; a salt would be a key change and belongs inside a
// rotate, not a silent upgrade.
func DeriveKey(ikm, purpose string) ([]byte, error) {
	if ikm == "" {
		return nil, fmt.Errorf("encryption root is empty")
	}
	reader := hkdf.New(sha256.New, []byte(ikm), nil, []byte(purpose))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("HKDF key derivation failed: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using the given key.
// Returns a base64-encoded string prefixed with "enc:" (the legacy envelope).
func Encrypt(plaintext string, key []byte) (string, error) {
	sealed, err := seal(plaintext, key)
	if err != nil {
		return "", err
	}
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// EncryptVersioned encrypts plaintext and wraps it as enc:v1:<keyID>:<base64>.
func EncryptVersioned(plaintext, keyID string, key []byte) (string, error) {
	if keyID == "" {
		return "", fmt.Errorf("refusing to write a versioned envelope with an empty key id")
	}
	if strings.ContainsAny(keyID, ":\n\r") {
		return "", fmt.Errorf("key id %q contains a separator", keyID)
	}
	sealed, err := seal(plaintext, key)
	if err != nil {
		return "", err
	}
	return versionedPrefix + keyID + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

func seal(plaintext string, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Envelope is a parsed ciphertext. Version 0 is the legacy enc: form.
type Envelope struct {
	Version int
	KeyID   string
	Data    []byte
}

// ParseEnvelope splits a stored value into its envelope fields.
// Unprefixed input is an error: Decrypt fails closed.
func ParseEnvelope(ciphertext string) (Envelope, error) {
	if strings.HasPrefix(ciphertext, versionedPrefix) {
		rest := strings.TrimPrefix(ciphertext, versionedPrefix)
		keyID, encoded, ok := strings.Cut(rest, ":")
		if !ok || keyID == "" || encoded == "" {
			return Envelope{}, fmt.Errorf("malformed versioned envelope")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Envelope{}, fmt.Errorf("failed to decode ciphertext: %w", err)
		}
		return Envelope{Version: 1, KeyID: keyID, Data: data}, nil
	}
	if strings.HasPrefix(ciphertext, encryptedPrefix) {
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
		if err != nil {
			return Envelope{}, fmt.Errorf("failed to decode ciphertext: %w", err)
		}
		return Envelope{Version: 0, Data: data}, nil
	}
	return Envelope{}, fmt.Errorf("ciphertext has no enc: prefix")
}

// Decrypt decrypts an enc:-prefixed or enc:v1:<id>:-prefixed ciphertext.
// Unprefixed input is an error, not plaintext.
func Decrypt(ciphertext string, key []byte) (string, error) {
	env, err := ParseEnvelope(ciphertext)
	if err != nil {
		return "", err
	}
	return open(env.Data, key)
}

// DecryptAny tries each key in order. Used while two generations are in flight.
func DecryptAny(ciphertext string, keys ...[]byte) (string, error) {
	var last error
	tried := 0
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		tried++
		plain, err := Decrypt(ciphertext, key)
		if err == nil {
			return plain, nil
		}
		last = err
	}
	if tried == 0 {
		return "", fmt.Errorf("no decryption key")
	}
	return "", last
}

func open(data, key []byte) (string, error) {
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
	plain, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key or corrupted data): %w", err)
	}
	return string(plain), nil
}

// IsEncrypted reports whether a stored value is in either envelope.
// Callers use this to tell leftover plaintext (deployment env, TURN) from
// ciphertext without asking Decrypt, which fails closed on plaintext.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encryptedPrefix)
}
