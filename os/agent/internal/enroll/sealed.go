// Package enroll implements the one-time enrollment exchange between a booting
// OramaOS node and the cluster's gateway.
package enroll

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

// Enrollment hands a booting node the cluster secret, the swarm key and its
// WireGuard configuration — everything needed to be a member of the cluster.
// That exchange used to happen over plaintext HTTP on the node's public IP,
// with no authentication in either direction:
//
//   - the node served its registration code to whoever asked first, so the
//     secret meant to prove the operator's identity was published;
//   - the config endpoint accepted any POST, so anyone who reached the node
//     first could enrol it into their own cluster, with their own WireGuard
//     peers;
//   - and the cluster secret crossed the network in the clear.
//
// The registration code is the only secret both ends share before the node is
// a member of anything, and the operator carries it from the node's console to
// the gateway. So it is what authenticates the exchange and what the payload is
// encrypted under. The code is never sent anywhere it could be read: the
// gateway proves it knows it, rather than asking for it.
//
// Phase 1 of the auth redesign replaces this with a node principal credential
// issued at join. Until then this is a code the operator physically carried,
// used as a code should be.

const (
	// HeaderEnrollmentCode carries the registration code the operator read off
	// the node's console. It proves the caller is the gateway the operator
	// spoke to, and it keys the payload.
	HeaderEnrollmentCode = "X-Orama-Enrollment-Code"

	// sealPurpose is the HKDF domain separator, so the key that encrypts an
	// enrollment payload is unrelated to anything else derived from the code.
	sealPurpose = "orama-enrollment-seal-v1"
)

// sealKey derives the AES-256 key both ends use, from the registration code.
func sealKey(code string) ([]byte, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("no enrollment code: there is nothing to derive a key from")
	}
	reader := hkdf.New(sha256.New, []byte(code), nil, []byte(sealPurpose))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("deriving the enrollment key failed: %w", err)
	}
	return key, nil
}

// Seal encrypts an enrollment payload under the registration code.
//
// The output is base64(nonce || ciphertext || tag), which travels as the whole
// HTTP body. A passive observer on the path sees a blob; the cluster secret
// inside it used to be readable with tcpdump.
func Seal(code string, plaintext []byte) (string, error) {
	key, err := sealKey(code)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating a nonce failed: %w", err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, plaintext, nil)), nil
}

// Open decrypts what Seal produced.
//
// A wrong code fails here rather than yielding garbage: GCM authenticates, so
// this doubles as the check that the caller knew the code.
func Open(code, sealed string) ([]byte, error) {
	key, err := sealKey(code)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sealed))
	if err != nil {
		return nil, fmt.Errorf("the enrollment payload is not base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("the enrollment payload is too short to contain a nonce")
	}

	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("the enrollment payload did not decrypt: either the "+
			"registration code is wrong or the payload was altered: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("creating the cipher failed: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("creating GCM failed: %w", err)
	}
	return gcm, nil
}
