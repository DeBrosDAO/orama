package ipfs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
)

// wrapMagic identifies ciphertext stored by Add so Get can pass through
// historical plaintext CIDs (feat-270).
var wrapMagic = []byte("ORMAW1")

// WrapPurpose is the HKDF label for the cluster-wide IPFS wrap key.
const WrapPurpose = "ipfs-wrap-v1"

func wrapPrivateBlob(name string) bool {
	n := strings.ToLower(name)
	if strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tgz") {
		return false
	}
	return true
}

func hasWrapEnvelope(b []byte) bool {
	return len(b) >= len(wrapMagic) && string(b[:len(wrapMagic)]) == string(wrapMagic)
}

func sealBlob(plaintext, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("ipfs wrap key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(wrapMagic)+gcm.NonceSize()+len(plaintext)+gcm.Overhead())
	out = append(out, wrapMagic...)
	return gcm.Seal(append(out, nonce...), nonce, plaintext, wrapMagic), nil
}

func openBlob(sealed, key []byte) ([]byte, error) {
	if !hasWrapEnvelope(sealed) {
		return sealed, nil
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ipfs wrap key missing; cannot decrypt")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	rest := sealed[len(wrapMagic):]
	if len(rest) < gcm.NonceSize() {
		return nil, fmt.Errorf("wrapped blob is too short")
	}
	nonce, ciphertext := rest[:gcm.NonceSize()], rest[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, wrapMagic)
}
