package tlsstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// sealedPrefix marks a sealed value and names its format.
	sealedPrefix = "v1."

	// sealAADPrefix binds a sealed value to the key it is stored under, so a
	// value moved to another key — a certificate's private key put where
	// another certificate's is read — does not open.
	sealAADPrefix = "orama-tls-store-v1\n"

	gcmNonceLen = 12
)

// Seal encrypts value for storage under key.
func Seal(sealKey []byte, key string, value []byte) (string, error) {
	aead, err := newAEAD(sealKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcmNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("draw a nonce to seal %s: %w", key, err)
	}
	out := aead.Seal(nonce, nonce, value, []byte(sealAADPrefix+key))
	return sealedPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value Seal made for key.
func Open(sealKey []byte, key, sealed string) ([]byte, error) {
	raw, err := sealedBytes(sealed)
	if err != nil {
		return nil, fmt.Errorf("stored value of %s: %w", key, err)
	}
	aead, err := newAEAD(sealKey)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, raw[:gcmNonceLen], raw[gcmNonceLen:], []byte(sealAADPrefix+key))
	if err != nil {
		return nil, fmt.Errorf("stored value of %s does not open with this cluster's key "+
			"(sealed under another cluster secret, or stored under another key)", key)
	}
	return plain, nil
}

// IsSealed reports whether value has the shape of a sealed value. The gateway
// stores nothing else, so a plaintext certificate or key is refused before it
// reaches the registry.
func IsSealed(value string) bool {
	_, err := sealedBytes(value)
	return err == nil
}

func sealedBytes(sealed string) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return nil, fmt.Errorf("not a sealed value (no %q prefix)", sealedPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("sealed value is not base64: %w", err)
	}
	if len(raw) < gcmNonceLen+16 {
		return nil, fmt.Errorf("sealed value is %d bytes, shorter than a nonce and a tag", len(raw))
	}
	return raw, nil
}

func newAEAD(sealKey []byte) (cipher.AEAD, error) {
	if len(sealKey) != keyLen {
		return nil, fmt.Errorf("TLS store seal key is %d bytes, want %d", len(sealKey), keyLen)
	}
	block, err := aes.NewCipher(sealKey)
	if err != nil {
		return nil, fmt.Errorf("TLS store seal key: %w", err)
	}
	return cipher.NewGCM(block)
}
