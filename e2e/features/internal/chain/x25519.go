//go:build e2e_fleet

package chain

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// NewX25519 makes a fresh X25519 key pair and returns both halves as the 64
// hex characters `orama global validator` takes (--recipient is the public
// half, --identity-file holds the private one).
func NewX25519(t testing.TB) (pubHex, privHex string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate an X25519 key: %v", err)
	}
	return hex.EncodeToString(k.PublicKey().Bytes()), hex.EncodeToString(k.Bytes())
}
