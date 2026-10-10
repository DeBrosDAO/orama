package releasepub

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// KeyID is the TUF key id of an ed25519 public key: the SHA-256, in hex, of the
// key's canonical JSON,
//
//	{"keytype":"ed25519","keyval":{"public":"<64 hex digits>"},"scheme":"ed25519"}
//
// (object keys sorted, no whitespace). The RootWallet agent returns the public
// key and no key id, so the id a root lists the key under is computed here; a
// client looks the key up by it.
func KeyID(pub ed25519.PublicKey) (string, error) {
	if len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("an ed25519 public key is %d bytes, got %d", ed25519.PublicKeySize, len(pub))
	}
	canonical := `{"keytype":"ed25519","keyval":{"public":"` + hex.EncodeToString(pub) + `"},"scheme":"ed25519"}`
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}
