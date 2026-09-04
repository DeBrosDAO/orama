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

// Enrollment hands a booting OramaOS node the cluster secret, the swarm key and
// its WireGuard configuration. That exchange used to happen over plaintext HTTP
// on the node's public IP with no authentication in either direction: the node
// served its registration code to whoever asked first, its config endpoint
// accepted any POST, and the cluster secret crossed the network in the clear.
//
// The registration code is the only secret both ends share before the node is a
// member of anything, and the operator carries it from the node's console to
// this gateway. So it authenticates the exchange and encrypts the payload, and
// it is never fetched from the node — this gateway proves it holds the code
// instead of asking for it.
//
// This is the mirror of os/agent/internal/enroll/sealed.go. The two are
// separate Go modules and cannot share the code, so a shared vector in
// contracts/ pins the format: change one side and the other side's test fails.
const (
	// HeaderEnrollmentCode carries the registration code the operator read off
	// the node's console.
	HeaderEnrollmentCode = "X-Orama-Enrollment-Code"

	// sealPurpose is the HKDF domain separator.
	sealPurpose = "orama-enrollment-seal-v1"
)

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

// Seal encrypts an enrollment payload under the registration code. The output
// is base64(nonce || ciphertext || tag) and travels as the whole HTTP body.
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

// Open decrypts what Seal produced. A wrong code fails here rather than
// yielding garbage, so this doubles as the check that the other end knew it.
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
